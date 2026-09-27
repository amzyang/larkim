package tui

import (
	"context"
	"strings"
	"time"

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

// flatLine draws a row with no terminal behind it: the pane's own rowLine on a
// model that places nothing. Without a placer the rows carry no pictures, so
// every branch that would reach for one is dead and the stand-ins renderRows
// already wrote are all there is to draw.
func flatLine(r msgRow, w int) string {
	line, _ := Model{}.rowLine(r, w)
	return line
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

// UnreadPage draws every chat that still owes the reader an answer, the way
// the TUI's Unread panel draws it: the same sections, rules and blocks, minus
// the cursor. Empty when nothing is waiting.
func UnreadPage(ctx context.Context, d Deps, width int) (string, error) {
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
	// Every message here is one the badge still counts, and there is no
	// cursor to walk the marker off, so all of them wear it.
	dots := make(map[string]bool, len(msgs))
	for _, x := range msgs {
		if isUnread(x) {
			dots[x.MessageID] = true
		}
	}
	st := meta.style()
	st.width, st.height, st.self, st.now = width, 1, d.Self, time.Now()
	st.dataDir, st.dots = d.DataDir, dots
	rows := renderFeedRows(msgs, &unreadFeed{sections: sections}, chats, st)
	var b strings.Builder
	for _, r := range rows {
		// Padding is what makes a row highlight span the pane, and there is
		// nothing to highlight here.
		b.WriteString(strings.TrimRight(flatLine(r, width), " "))
		b.WriteByte('\n')
	}
	return b.String(), nil
}
