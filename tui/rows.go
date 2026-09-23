package tui

import (
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

// bodyIndent is the gutter the message body sits in, below its sender line.
const bodyIndent = "  "

// imgRef matches the two ways lark-cli names an image in rendered text: the
// markdown form inside a rich-text post, and the whole body of an image
// message.
var imgRef = regexp.MustCompile(`!\[[^\]\n]*\]\((img_[A-Za-z0-9_-]+)\)|\[Image: (img_[A-Za-z0-9_-]+)\]`)

// msgRow is one rendered line of a message list and the message it belongs to.
type msgRow struct {
	text string
	idx  int  // index into the backing message slice
	head bool // first line of the message
	// plain marks a row that belongs to no message — a day separator — so the
	// selection never paints it.
	plain bool
	// pic is set on the rows a picture occupies, picRow being which of its
	// rows this one is. Those rows carry an image rather than text, so they
	// are neither fitted nor highlighted, and their text is only known once
	// the terminal holds the picture.
	pic    picture
	picRow int
}

// msgStyle is what the message rows need besides the messages themselves.
type msgStyle struct {
	width    int
	self     string
	now      time.Time
	selected string                      // the one message whose time is spelled out
	suffix   map[string]string           // sender open id → account suffix
	res      map[string][]store.Resource // attachments, by message id
	// place sizes a picture for the pane. Nil draws a text stand-in instead,
	// which is what a terminal without graphics gets.
	place func(path string, maxCols int) picture
}

// renderRows lays messages out as a sender line followed by the body, split
// into days.
func renderRows(msgs []store.Message, st msgStyle) []msgRow {
	var rows []msgRow
	day := ""
	for i, x := range msgs {
		if d := msgDay(x.CreateMs, st.now); d != day {
			day = d
			rows = append(rows, msgRow{text: daySeparator(d, st.width), idx: i, plain: true})
		}
		if x.MsgType == "system" {
			rows = append(rows, systemRows(x, i, st)...)
			continue
		}
		rows = append(rows, msgRow{text: headLine(x, st), idx: i, head: true})
		rows = append(rows, bodyRows(x, i, st)...)
	}
	return rows
}

// headLine names who spoke. The time is spelled out for the selected message
// only — on every row it would be noise, and the day separators already carry
// the coarse answer.
func headLine(x store.Message, st msgStyle) string {
	sender := flatten(x.SenderName)
	if sender == "" {
		sender = x.SenderID
	}
	if x.SenderID == st.self {
		sender = stOK.Render(sender)
	} else {
		sender = stBold.Render(sender)
	}
	if s := st.suffix[x.SenderID]; s != "" {
		sender += stDim.Render("(" + s + ")")
	}
	dot := " "
	if x.IsReadRemote != nil && !*x.IsReadRemote {
		dot = stAccent.Render("●")
	}
	head := dot + sender
	if x.MessageID == st.selected {
		head += " " + stDim.Render(msgTime(x.CreateMs, st.now))
	}
	if x.ThreadID != "" && x.MessagePosition >= 0 {
		head += stAccent.Render(" ⤷thread")
	}
	if x.Updated {
		head += stDim.Render(" (edited)")
	}
	return head
}

// systemRows draw a system message the way the client does: centred and
// muted, with no sender of its own.
func systemRows(x store.Message, idx int, st msgStyle) []msgRow {
	var rows []msgRow
	for j, line := range wrap(stDim.Render(flatten(x.Content)), st.width-4) {
		rows = append(rows, msgRow{text: centre(line, st.width), idx: idx, head: j == 0})
	}
	return rows
}

// bodyRows render one message's content below its sender line: a card as a
// framed block, a picture as the picture itself, everything else as text.
func bodyRows(x store.Message, idx int, st msgStyle) []msgRow {
	inner := st.width - lipgloss.Width(bodyIndent)
	text := func(lines []string) []msgRow {
		out := make([]msgRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, msgRow{text: bodyIndent + l, idx: idx})
		}
		return out
	}
	switch {
	case x.Deleted:
		return text(wrap(stDim.Render("(recalled) "+flatten(x.Content)), inner))
	case x.RenderedAt == 0:
		return text(wrap(stDim.Render("(rendering…) ")+flatten(x.ContentRaw), inner))
	}
	if c, ok := parseCard(x.Content); ok && x.MsgType == "interactive" {
		return text(renderCard(c, inner))
	}

	var rows []msgRow
	content := strings.ReplaceAll(strings.ReplaceAll(x.Content, "\r", ""), "\t", "    ")
	for _, line := range strings.Split(content, "\n") {
		keys, rest := splitImages(line)
		if len(keys) == 0 || strings.TrimSpace(rest) != "" {
			rows = append(rows, text(wrap(renderInline(rest), inner))...)
		}
		for _, key := range keys {
			rows = append(rows, pictureRows(key, x, idx, st, inner)...)
		}
	}
	return rows
}

// pictureRows reserve the cells a downloaded image will occupy. A picture
// this terminal cannot draw — no graphics protocol, not downloaded yet, a
// format the decoder will not read — falls back to a one-line stand-in.
func pictureRows(key string, x store.Message, idx int, st msgStyle, inner int) []msgRow {
	stand := []msgRow{{text: bodyIndent + stDim.Render("[图片]"), idx: idx}}
	if st.place == nil {
		return stand
	}
	path := ""
	for _, r := range st.res[x.MessageID] {
		if r.FileKey == key && r.Status == "done" {
			path = r.LocalPath
		}
	}
	pic := st.place(path, inner)
	if pic.cols == 0 {
		return stand
	}
	rows := make([]msgRow, 0, pic.rows)
	for row := range pic.rows {
		rows = append(rows, msgRow{idx: idx, pic: pic, picRow: row})
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
