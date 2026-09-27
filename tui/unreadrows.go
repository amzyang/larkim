package tui

import (
	"context"
	"strings"

	"github.com/amzyang/larkim/store"
)

// sectionStarts is the index of each section's first message. Sections are
// laid out one chat after another, so a chat change is a section boundary —
// which holds through applyOutbox too, since a pending send is spliced into
// its own chat's stretch.
func sectionStarts(msgs []store.Message) []int {
	var out []int
	for i, x := range msgs {
		if i == 0 || msgs[i-1].ChatID != x.ChatID {
			out = append(out, i)
		}
	}
	return out
}

// nextSection is the first message of the section one step from idx, wrapping
// once the way n and N walk the chats list. -1 when the page holds no other.
func nextSection(starts []int, idx, step int) int {
	if len(starts) < 2 {
		return -1
	}
	at := 0
	for i, s := range starts {
		if s <= idx {
			at = i
		}
	}
	return starts[((at+step)%len(starts)+len(starts))%len(starts)]
}

// feedRule parts one chat's stretch from the next. It is the rule the search
// panel groups its hits under, so the two panels read the same way; the day
// rule below it is centred, which is what tells the two apart at a glance.
//
// It carries the index of the section's first message even though nothing can
// select it: every row answering which chat it belongs to is what lets the
// pinned rule be read straight off the viewport's top line.
func feedRule(label string, idx, w int) msgRow {
	r := searchRule(label, w)
	r.idx = idx
	return r
}

// feedNote is a dim line closing a section, or the page. It belongs to no
// message the cursor can reach, but takes an index all the same so the row
// still names the section it sits in.
func feedNote(text string, idx, w int) msgRow {
	return msgRow{text: fit(stDim.Render(text), w), idx: idx, plain: true}
}

// unreadMore counts the chats the page does not show: the ones past
// unreadFeedChats, and any whose section had nothing left to draw.
func unreadMore(chats []store.Chat, shown []unreadSection) int {
	in := make(map[string]bool, len(shown))
	for _, s := range shown {
		in[s.chatID] = true
	}
	n := 0
	for _, c := range chats {
		if c.UnreadCount > 0 && !in[c.ChatID] {
			n++
		}
	}
	return n
}

// renderFeedRows lays the page out: each chat's stretch under a rule naming
// it, in the order the sections come, and closes with what the page leaves
// out. chats is the whole listing, which is what says how much that is.
func renderFeedRows(msgs []store.Message, feed *unreadFeed, chats []store.Chat, st msgStyle) []msgRow {
	// The page runs across chats, so every block names its sender and no @ in
	// one is measured against the other half of a chat of two.
	st.p2p, st.peer = false, ""
	// The rule already says which chat a stretch belongs to; naming it on
	// every block as the search panel does would say it a second time.
	st.names = nil
	starts := sectionStarts(msgs)
	rows := make([]msgRow, 0, len(msgs)*3)
	for n, at := range starts {
		end := len(msgs)
		if n+1 < len(starts) {
			end = starts[n+1]
		}
		sec := feed.section(msgs[at].ChatID)
		rows = append(rows, feedRule(sec.label(), at, st.width))
		for _, r := range renderRows(msgs[at:end], st) {
			r.idx += at
			rows = append(rows, r)
		}
		if sec.cut {
			rows = append(rows, feedNote("  more below in "+sec.label()+" — Enter opens the chat", end-1, st.width))
		}
	}
	if n := unreadMore(chats, feed.sections); n > 0 {
		rows = append(rows, feedNote("  "+plural(n, "more chat", "more chats")+" waiting", len(msgs)-1, st.width))
	}
	return rows
}

// UnreadScreen is the terminal the page is drawn to. It is the message pane of
// a TUI with nothing else in it: no chats list beside it, no status line or
// composer under it, and no viewport, because the page is written out whole.
type UnreadScreen struct {
	// Width is the whole terminal: this pane has no neighbour and no border to
	// give columns up to.
	Width int
	// Height bounds a single picture rather than the page, which is as long as
	// it needs to be. It is the screen the reader sees at once, so one image
	// never buries the text around it — the cap Model.picHeight puts on the
	// TUI's own pane.
	Height int
	// CellW, CellH are the terminal's cell size in pixels, zero until it says.
	CellW, CellH int
	// Dark says which way the terminal's background leans, which is what picks
	// the palette a code block is coloured from.
	Dark bool
	// TTY says the page goes to the terminal itself rather than down a pipe,
	// which is the one thing the environment cannot say: KITTY_WINDOW_ID is set
	// for a redirect too, and pictures written into a file are noise.
	TTY bool
}

// UnreadRows is the messages the Unread panel would show, in the order it
// shows them: one chat's stretch after another, oldest backlog first.
func UnreadRows(ctx context.Context, d Deps) ([]store.Message, error) {
	chats, err := d.Store.ListChats(ctx, store.ChatQuery{Self: d.Self})
	if err != nil {
		return nil, err
	}
	_, msgs, _, err := gatherUnread(ctx, d.Store, d.Self, chats, nil)
	return msgs, err
}

// UnreadPage draws every chat that still owes the reader an answer, the way the
// TUI's Unread panel draws it. It is that panel with the interaction taken out,
// and it renders through the panel's own Model rather than a context built
// beside it, so a field the panes learn to draw reaches this page too.
// Empty when nothing is waiting.
func UnreadPage(ctx context.Context, d Deps, sc UnreadScreen) (string, error) {
	chats, err := d.Store.ListChats(ctx, store.ChatQuery{Self: d.Self})
	if err != nil {
		return "", err
	}
	sections, msgs, meta, err := gatherUnread(ctx, d.Store, d.Self, chats, nil)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "", nil
	}
	selfName, err := selfNameOf(ctx, d)
	if err != nil {
		return "", err
	}
	// Every message here is one the badge still counts, and there is no cursor
	// to walk the marker off, so all of them wear it.
	dots := make(map[string]bool, len(msgs))
	for _, x := range msgs {
		if isUnread(x) {
			dots[x.MessageID] = true
		}
	}
	// A page-shaped Model: what the rows are drawn from, and nothing the reader
	// would have pressed a key to reach. feed is what parts the page into
	// sections, and it is also what keeps msgStyleFor from reading a p2p peer
	// off a current chat — this page runs across chats and has none.
	m := Model{deps: d, meta: meta, chats: chats, msgs: msgs,
		feed: &unreadFeed{sections: sections}, dots: dots,
		dark: sc.Dark, selfName: selfName, pics: unreadPictures(d, sc)}
	st := m.msgStyleFor(sc.Width, meta)
	// The one measurement the panes cannot answer for: picHeight takes the
	// composer, the status line and the pane's own title off the terminal, and
	// this page carries none of them.
	st.height = sc.Height
	rows := renderFeedRows(msgs, m.feed, chats, st)
	var b strings.Builder
	// The pictures reach the terminal before the cells that name them.
	var claimed picSet
	claimed.takeRows(rows)
	b.WriteString(m.pics.prepare(claimed.pics))
	for _, r := range rows {
		line, _ := m.rowLine(r, sc.Width)
		// Padding is what makes a row highlight span the pane, and there is
		// nothing to highlight here. A placeholder cell is U+10EEEE and its
		// diacritics, so no picture is trimmed away with the blanks.
		b.WriteString(strings.TrimRight(line, " "))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// unreadPictures is the placer the page draws through, nil wherever there is
// nothing to draw with: output that is not the terminal itself, a terminal
// without the graphics protocol, or no data dir holding the files.
func unreadPictures(d Deps, sc UnreadScreen) *pictures {
	if !sc.TTY {
		return nil
	}
	p := newPictures(d.DataDir, d.env())
	p.setCellSize(sc.CellW, sc.CellH)
	return p
}
