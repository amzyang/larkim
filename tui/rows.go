package tui

import (
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

const (
	// gutterWidth is the two columns every row of a message opens with.
	gutterWidth = 2
	// rail marks the reader's own messages down their left edge. It is not
	// the card frame's ▌, so a card of one's own still reads as a card.
	rail = "▎"
	// runSpan is how long a sender's block stays open, measured from the
	// message that opened it, so a name line still marks a sender coming back
	// rather than a whole day collapsing into one block.
	runSpan = 5 * time.Minute
)

// imgRef matches the two ways lark-cli names an image in rendered text: the
// markdown form inside a rich-text post, and the whole body of an image
// message.
var imgRef = regexp.MustCompile(`!\[[^\]\n]*\]\((img_[A-Za-z0-9_-]+)\)|\[Image: (img_[A-Za-z0-9_-]+)\]`)

// msgRow is one rendered line of a message list and the message it belongs to.
type msgRow struct {
	text string
	idx  int // index into the backing message slice
	// plain marks a row that belongs to no message — a day separator — so the
	// selection never paints it.
	plain bool
	// pic is set on the rows a picture occupies, picRow being which of its
	// rows this one is. Those rows carry an image rather than text, so they
	// are neither fitted nor highlighted, and their text is only known once
	// the terminal holds the picture. prefix is the gutter drawn to their
	// left, which a picture inside a card has to extend with the card's edge.
	pic    picture
	picRow int
	prefix string
}

// msgStyle is what the message rows need besides the messages themselves.
type msgStyle struct {
	width   int
	self    string
	now     time.Time
	quoted  string                      // the message an open draft replies to
	parents map[string]store.Message    // the messages these replies answer, by parent id
	suffix  map[string]string           // sender open id → account suffix
	res     map[string][]store.Resource // attachments, by message id
	outbox  map[string]outboxState      // the sends still on their way, by the id their rows carry
	// names labels each message's chat on its sender line. It is set for
	// search results, which run across chats; inside one chat, naming it on
	// every block says nothing.
	names map[string]string
	// height is how many rows the pane shows, which bounds a picture the way
	// width does.
	height int
	// place sizes a picture for the pane. Nil draws a text stand-in instead,
	// which is what a terminal without graphics gets.
	place func(path string, maxCols, maxRows int) picture
}

// inner is the width a message body has, once the gutter is taken off.
func (st msgStyle) inner() int { return st.width - gutterWidth }

// gutters are the two columns a message's rows open with. The first row
// carries the marker that belongs to the message as a whole — the draft's
// reply target, or the dot on an unread one — and the rest carry the rail.
type gutters struct{ first, rest string }

func (g *gutters) take() string {
	if g.first != "" {
		s := g.first
		g.first = ""
		return s
	}
	return g.rest
}

// gutterFor rails the reader's own messages, shaded by how far the send has
// got, and picks the marker the message's first row opens with. The unread
// dot is the block's, not the message's — a block is all read or all unread,
// so repeating it under the sender line would say nothing new.
func gutterFor(x store.Message, st msgStyle, opensBlock bool) gutters {
	rest := strings.Repeat(" ", gutterWidth)
	if x.SenderID == st.self {
		rest = railStyle(x, st).Render(rail) + " "
	}
	first := rest
	switch {
	case x.MessageID == st.quoted:
		first = stAccent.Render("↩") + " "
	case opensBlock && unread(x):
		first = stAccent.Render("●") + " "
	}
	return gutters{first: first, rest: rest}
}

// railStyle shades a send that has not landed yet. Absent from the outbox is
// the ordinary case — a message the store returned — which no zero value may
// stand in for.
func railStyle(x store.Message, st msgStyle) lipgloss.Style {
	state, ok := st.outbox[x.MessageID]
	switch {
	case !ok:
		return stOK
	case state == outFailed:
		return stErr
	default:
		return stDim
	}
}

// unread reports whether Feishu still has a message down as unread.
func unread(x store.Message) bool { return x.IsReadRemote != nil && !*x.IsReadRemote }

// renderRows lays messages out as blocks — a sender line followed by every
// body that sender wrote next — split into days.
func renderRows(msgs []store.Message, st msgStyle) []msgRow {
	var rows []msgRow
	day, prev, run := "", "", -1
	for i, x := range msgs {
		if d := msgDay(x.CreateMs, st.now); d != day {
			day, run = d, -1
			rows = append(rows, msgRow{text: daySeparator(d, st.width), idx: i, plain: true})
		}
		if x.MsgType == "system" {
			rows = append(rows, systemRows(x, i, st)...)
			prev, run = x.MessageID, -1
			continue
		}
		opensBlock := run < 0 || !mergeable(msgs[run], x, st)
		g := gutterFor(x, st, opensBlock)
		if opensBlock {
			rows = append(rows, msgRow{text: g.take() + headLine(x, st), idx: i})
			run = i
		}
		if q, ok := quoteRow(x, prev, i, st, &g); ok {
			rows = append(rows, q)
		}
		rows = append(rows, bodyRows(x, i, st, &g)...)
		prev = x.MessageID
	}
	return rows
}

// mergeable reports whether a message can hide under the sender line that
// opened the block above it: the same sender in the same chat, in the same
// read state, inside the block's span, with nothing of its own to say.
func mergeable(head, x store.Message, st msgStyle) bool {
	// The search pane lists hits newest first, so the gap runs backwards there
	// and only its magnitude can decide whether the block is still open.
	gap := x.CreateMs - head.CreateMs
	return head.SenderID == x.SenderID && head.SenderName == x.SenderName &&
		head.ChatID == x.ChatID && unread(head) == unread(x) &&
		max(gap, -gap) <= runSpan.Milliseconds() && !solo(x, st)
}

// solo reports whether a message carries something only a sender line of its
// own can show.
func solo(x store.Message, st msgStyle) bool {
	if (x.ThreadID != "" && x.MessagePosition >= 0) || x.EditedAt > 0 {
		return true
	}
	state, ok := st.outbox[x.MessageID]
	return ok && state == outFailed
}

// quoteRow names the message a reply answers, above its body, the way the
// Feishu client quotes it. A reply to the message right above says nothing
// the list does not already show, so that one is left out.
func quoteRow(x store.Message, prev string, idx int, st msgStyle, g *gutters) (msgRow, bool) {
	if x.ReplyTo == "" || x.ReplyTo == prev {
		return msgRow{}, false
	}
	row := func(s string) msgRow { return msgRow{text: g.take() + stDim.Render(s), idx: idx} }
	parent, ok := st.parents[x.ReplyTo]
	if !ok {
		return row("▏↩ (not synced)"), true
	}
	head := "▏" + displaySender(parent, st.self, st.suffix[parent.SenderID]) + ": "
	return row(head + truncate(replyGist(parent), st.inner()-lipgloss.Width(head))), true
}

// senderLabel names a message's sender: the display name Feishu sent, the
// open id when it sent none, and the account suffix that tells same-named
// colleagues apart.
func senderLabel(x store.Message, suffix string) string {
	name := flatten(x.SenderName)
	if name == "" {
		name = x.SenderID
	}
	return personName(name, suffix)
}

// displaySender names a sender on a list: the reader reads as 你, the way the
// chat list already names them.
func displaySender(x store.Message, self, suffix string) string {
	if x.SenderID == self {
		return "你"
	}
	return senderLabel(x, suffix)
}

// headLine opens a block: who spoke, and the badges belonging to the message
// that starts it. It carries no clock — a merged message has no line of its
// own to spell one out on, so the status bar answers for every message alike.
func headLine(x store.Message, st msgStyle) string {
	head := displaySender(x, st.self, st.suffix[x.SenderID])
	if x.SenderID == st.self {
		head = stOK.Render(head)
	} else {
		head = stBold.Render(head)
	}
	if st.names != nil {
		name := st.names[x.ChatID]
		if name == "" {
			name = x.ChatID
		}
		head = stAccent.Render(truncate(flatten(name), 18)) + " " + head
	}
	if x.ThreadID != "" && x.MessagePosition >= 0 {
		head += stAccent.Render(" ⤷thread")
	}
	if x.EditedAt > 0 {
		head += stDim.Render(" (Edited)")
	}
	// Absent from the map is the ordinary case — a message the store
	// returned — which no zero value may stand in for.
	if state, ok := st.outbox[x.MessageID]; ok {
		if state == outFailed {
			head += stErr.Render(" (failed)")
		} else {
			head += stDim.Render(" (sending)")
		}
	}
	return head
}

// systemRows draw a system message the way the client does: centred and
// muted, with no sender of its own.
func systemRows(x store.Message, idx int, st msgStyle) []msgRow {
	var rows []msgRow
	for _, line := range wrap(stDim.Render(flatten(x.Content)), st.width-4) {
		rows = append(rows, msgRow{text: centre(line, st.width), idx: idx})
	}
	return rows
}

// bodyRows render one message's content below its sender line: a card as a
// framed block, a picture as the picture itself, everything else as text.
func bodyRows(x store.Message, idx int, st msgStyle, g *gutters) []msgRow {
	inner := st.inner()
	text := func(lines []string) []msgRow {
		out := make([]msgRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, msgRow{text: g.take() + l, idx: idx})
		}
		return out
	}
	switch {
	case x.Deleted:
		return text(wrap(stDim.Render("(Recalled) "+flatten(x.Content)), inner))
	case x.RenderedAt == 0:
		return text(wrap(stDim.Render(expandEmoji(pendingText(x.MsgType, x.ContentRaw))), inner))
	}
	ms := mentionsIn(x.MentionsJSON, st.self)
	if c, ok := parseCard(x.Content); ok && x.MsgType == "interactive" {
		return cardRows(c, x, idx, st, g, ms)
	}

	var rows []msgRow
	content := strings.ReplaceAll(strings.ReplaceAll(x.Content, "\r", ""), "\t", "    ")
	for _, line := range strings.Split(content, "\n") {
		keys, rest := splitImages(line)
		if len(keys) == 0 || strings.TrimSpace(rest) != "" {
			rows = append(rows, text(wrap(renderInline(rest, ms), inner))...)
		}
		for _, key := range keys {
			rows = append(rows, pictureRows(key, "", x, idx, st, g)...)
		}
	}
	return rows
}

// cardRows lay a card out below its sender line: the text lines carry the
// frame themselves, and the pictures the body names are placed inside it.
func cardRows(c card, x store.Message, idx int, st msgStyle, g *gutters, ms mentions) []msgRow {
	edge := stAccent.Render(cardRule) + " "
	var rows []msgRow
	for _, cr := range renderCard(c, st.inner(), ms) {
		if cr.imgKey == "" {
			rows = append(rows, msgRow{text: g.take() + cr.text, idx: idx})
			continue
		}
		rows = append(rows, pictureRows(cr.imgKey, edge, x, idx, st, g)...)
	}
	return rows
}

// pictureRows reserve the cells a downloaded image will occupy, behind the
// gutter and, inside a card, the card's own edge. A picture this terminal
// cannot draw — no graphics protocol, not downloaded yet, a format the
// decoder will not read — falls back to a one-line stand-in.
func pictureRows(key, edge string, x store.Message, idx int, st msgStyle, g *gutters) []msgRow {
	stand := func() []msgRow {
		return []msgRow{{text: g.take() + edge + stDim.Render("[图片]"), idx: idx}}
	}
	if st.place == nil {
		return stand()
	}
	path := ""
	for _, r := range st.res[x.MessageID] {
		if r.FileKey == key && r.Status == "done" {
			path = r.LocalPath
		}
	}
	pic := st.place(path, st.inner()-lipgloss.Width(edge), st.height)
	if pic.cols == 0 {
		return stand()
	}
	rows := make([]msgRow, 0, pic.rows)
	for row := range pic.rows {
		rows = append(rows, msgRow{idx: idx, pic: pic, picRow: row, prefix: g.take() + edge})
	}
	return rows
}

// splitImages pulls the image references out of one line and returns them
// with what is left of the text.
func splitImages(line string) (keys []string, rest string) {
	rest = imgRef.ReplaceAllStringFunc(line, func(m string) string {
		g := imgRef.FindStringSubmatch(m)
		keys = append(keys, g[1]+g[2])
		return ""
	})
	return keys, rest
}

// daySeparator splits the list where the calendar day changes.
func daySeparator(label string, width int) string {
	rule := max(0, (width-lipgloss.Width(label)-2)/2)
	return stDim.Render(strings.Repeat("─", rule) + " " + label + " " + strings.Repeat("─", rule))
}

// centre pads a line so it sits in the middle of w columns. Wrapping pads to
// its own width first, and that padding would push the text left of centre.
func centre(s string, w int) string {
	s = strings.TrimRight(s, " ")
	if pad := (w - lipgloss.Width(s)) / 2; pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

var weekdayNames = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// daysApart counts calendar days between a message and now, so a timestamp
// four minutes before midnight still reads as yesterday.
func daysApart(t, now time.Time) int {
	day := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location())
	}
	return int(day(now).Sub(day(t)).Hours() / 24)
}

// msgDay labels the day a message falls on, in the buckets the chat list uses.
func msgDay(ms int64, now time.Time) string {
	t := time.UnixMilli(ms).Local()
	switch days := daysApart(t, now); {
	case days <= 0:
		return "今天"
	case days == 1:
		return "昨天"
	case days < 7:
		return weekdayNames[t.Weekday()]
	case t.Year() == now.Year():
		return t.Format("01-02")
	default:
		return t.Format("2006-01-02")
	}
}

// msgTime is a message's own timestamp: the clock time today, the day label
// before it.
func msgTime(ms int64, now time.Time) string {
	t := time.UnixMilli(ms).Local()
	if daysApart(t, now) <= 0 {
		return t.Format("15:04")
	}
	return msgDay(ms, now) + " " + t.Format("15:04")
}
