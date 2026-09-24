package tui

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/emoji"
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
	// plain marks a row that belongs to no message — a day separator, or the
	// blank line parting one message from the next — so the selection never
	// paints it.
	plain bool
	// pic is set on the rows a picture occupies, picRow being which of its
	// rows this one is. Those rows carry an image rather than text, so they
	// are neither fitted nor highlighted, and their text is only known once
	// the terminal holds the picture. prefix is the gutter drawn to their
	// left, which a picture inside a card has to extend with the card's edge.
	pic    picture
	picRow int
	prefix string
	// zone, when set, is the click target this row carries: the button a
	// call's card ends with.
	zone clickZone
	// segs, when set, is the row's text in pieces so that pictures can sit
	// inside a line rather than take one of their own: a reaction carries an
	// emoji this terminal may have no character for, next to a count that is
	// ordinary text.
	segs []rowSeg
	// tinted marks a row of pieces a selection still colours. Body text earns
	// it — an emoji drawn over the tint is what the client shows, and a row
	// left plain inside a selected block reads as a hole. A chip does not: it
	// carries a tint of its own that the selection's would fight.
	tinted bool
}

// clickZone is a target inside a row: the half-open column range [x0, x1)
// in the pane's own content coordinates, and where clicking it leads. An
// empty url is no target at all.
type clickZone struct {
	x0, x1 int
	url    string
}

func (z clickZone) hit(x int) bool { return z.url != "" && x >= z.x0 && x < z.x1 }

// rowSeg is one piece of a row: text, or a picture the terminal fills in.
type rowSeg struct {
	text string
	pic  picture
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
	dots    map[string]bool             // the messages this visit draws the unread marker on
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
	// emojiDir holds the pictures cut out of the sprite sheet, which is what
	// an emoji with no Unicode character is drawn as. Empty means they were
	// never cut out, and the name stands in.
	emojiDir string
	// dark says which way the terminal's background leans, which is what picks
	// the palette a code block is coloured from.
	dark bool
	// people names a reaction's operators, by open id. A sender's name
	// travels on the message itself; a reactor's does not — the block holds
	// an id alone, and the contacts table is where a name for it lives.
	people map[string]string
}

// emojiPics sizes the pictures cut out of the sprite sheet. The zero value
// draws none, which is what a terminal without graphics, or a data dir they
// were never cut into, gets.
type emojiPics struct {
	place func(path string, maxCols, maxRows int) picture
	dir   string
}

// pic sizes one emoji's picture to sit on a line of text: one row tall, and
// up to cols wide. A zero size means there is nothing to draw, and the caller
// falls back to the emoji's name or leaves it out.
func (p emojiPics) pic(key string, cols int) picture {
	if p.place == nil || p.dir == "" {
		return picture{}
	}
	return p.place(emoji.Path(p.dir, key), cols, 1)
}

// chip is the same picture with the reaction's tint behind its cells, which is
// how a reaction is told apart from a picture inside the message.
func (p emojiPics) chip(key string, cols int) picture {
	pic := p.pic(key, cols)
	pic.chip = pic.cols > 0
	return pic
}

// chatPics sizes the emoji pictures drawn inside a line of text: a chat
// row's reactions, and the emoji the picker offers.
func (m Model) chatPics() emojiPics {
	if m.pics == nil {
		return emojiPics{}
	}
	return emojiPics{place: m.pics.place, dir: m.deps.DataDir}
}

// emojiChip is one reaction's picture beside a message, as wide as its own
// shape asks for.
func (st msgStyle) emojiChip(key string) picture {
	return emojiPics{place: st.place, dir: st.emojiDir}.chip(key, emojiCols)
}

// emojiInline is one emoji's picture standing in a line of body text. It
// carries no chip tint: inside a message an emoji is part of what was said,
// not a reaction put on it afterwards.
func (st msgStyle) emojiInline(key string) picture {
	return emojiPics{place: st.place, dir: st.emojiDir}.pic(key, emojiCols)
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
	case opensBlock && unread(x, st):
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
		return stSelf
	case state == outFailed:
		return stErr
	default:
		return stDim
	}
}

// unread reports whether a message still carries this visit's marker. The
// answer is the set the panes gathered as pages arrived, not the store's own
// flags, which opening the chat has already cleared.
func unread(x store.Message, st msgStyle) bool { return st.dots[x.MessageID] }

// renderRows lays messages out as blocks — a sender line followed by every
// body that sender wrote next — split into days.
func renderRows(msgs []store.Message, st msgStyle) []msgRow {
	var rows []msgRow
	day, prev, run := "", "", -1
	// open starts a section — a day rule, a system notice, a sender's block —
	// holding it off whatever came before with a blank line. A day rule is a
	// divider in its own right and heads the day below it, so the first
	// section under one takes no blank of its own. The line belongs to the
	// message below it, so scrolling to that message brings its own air along.
	open := func(i int, underRule bool) {
		if len(rows) > 0 && !underRule {
			rows = append(rows, msgRow{idx: i, plain: true})
		}
	}
	for i, x := range msgs {
		d := msgDay(x.CreateMs, st.now)
		rule := d != day
		if rule {
			open(i, false)
			day, run = d, -1
			rows = append(rows, msgRow{text: daySeparator(d, st.width), idx: i, plain: true})
		}
		if x.MsgType == "system" {
			open(i, rule)
			rows = append(rows, systemRows(x, i, st)...)
			prev, run = x.MessageID, -1
			continue
		}
		opensBlock := run < 0 || !mergeable(msgs[run], x, st)
		g := gutterFor(x, st, opensBlock)
		if opensBlock {
			open(i, rule)
			rows = append(rows, msgRow{text: g.take() + headLine(x, st), idx: i})
			// The blank under the sender line keeps the gutter, so the rail
			// runs unbroken and a selection tints the block as one shape.
			rows = append(rows, msgRow{text: g.take(), idx: i})
			run = i
		}
		if q, ok := quoteRow(x, prev, i, st, &g); ok {
			rows = append(rows, q)
		}
		rows = append(rows, bodyRows(x, i, st, &g)...)
		rows = append(rows, reactionRows(x, i, st, &g)...)
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
		head.ChatID == x.ChatID && unread(head, st) == unread(x, st) &&
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
// chat list already names them, and an app carries the badge that says the
// turn is a machine's.
func displaySender(x store.Message, self, suffix string) string {
	if x.SenderID == self {
		return "你"
	}
	return senderLabel(x, suffix) + botMark(x.SenderType)
}

// headLine opens a block: who spoke, and the badges belonging to the message
// that starts it. It carries no clock — a merged message has no line of its
// own to spell one out on, so the status bar answers for every message alike.
func headLine(x store.Message, st msgStyle) string {
	head := displaySender(x, st.self, st.suffix[x.SenderID])
	if x.SenderID == st.self {
		head = stSelf.Render(head)
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
	if x.Deleted {
		return text(wrap(stDim.Render("(Recalled) "+flatten(x.Content)), inner))
	}
	// A call's body is the whole invite, so its card is drawn without
	// waiting for a rendering: the button matters most in the first seconds.
	if v, ok := videoChatOf(x); ok {
		return videoChatRows(v, idx, st, g)
	}
	if x.RenderedAt == 0 {
		return text(wrap(stDim.Render(expandEmoji(pendingText(x.MsgType, x.ContentRaw))), inner))
	}
	ms := mentionsIn(x.MentionsJSON, st.self)
	if c, ok := parseCard(x.Content); ok && x.MsgType == "interactive" {
		return cardRows(c, x, idx, st, g, ms)
	}
	// A sticker renders as the text "[Sticker]", which names the picture
	// nowhere: the key is in the body.
	if key := stickerKey(x); key != "" {
		return pictureRows(key, "", x, idx, st, g)
	}

	var rows []msgRow
	content := strings.ReplaceAll(strings.ReplaceAll(x.Content, "\r", ""), "\t", "    ")
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fence := codeFence.FindStringSubmatch(line); fence != nil {
			code, next := takeCode(lines, i)
			rows = append(rows, text(codeRows(code, fence[1], inner, st.dark))...)
			i = next
			continue
		}
		keys, rest := splitImages(line)
		if len(keys) == 0 || strings.TrimSpace(rest) != "" {
			if segs := inlineSegs(rest, ms, st.emojiInline); segs != nil {
				rows = append(rows, segRows(segs, idx, st, g)...)
			} else {
				rows = append(rows, text(wrap(renderInline(rest, ms), inner))...)
			}
		}
		for _, key := range keys {
			rows = append(rows, pictureRows(key, "", x, idx, st, g)...)
		}
	}
	return rows
}

// segRows draw one body line as the pieces the client's own emoji pictures
// sit between, where a plain string cannot hold them.
func segRows(segs []rowSeg, idx int, st msgStyle, g *gutters) []msgRow {
	packed := wrapSegs(segs, st.inner())
	out := make([]msgRow, 0, len(packed))
	for _, row := range packed {
		out = append(out, msgRow{idx: idx, tinted: true,
			segs: append([]rowSeg{{text: g.take()}}, row...)})
	}
	return out
}

// emojiCols is the widest a reaction picture is drawn. Most of Feishu's emoji
// are square, which is two cells beside a line of text; the few that are a
// word rather than a face are wider, and cutting them to a square would make
// them unreadable.
const emojiCols = 4

// stickerCols is the widest a sticker is drawn. A sticker is a gesture rather
// than a picture to study, and the client draws it about this size; given the
// pane it would take a screenful for one shrug.
const stickerCols = 12

// stickerKey is the picture a sticker message carries, and "" for any other
// message.
func stickerKey(x store.Message) string {
	if x.MsgType != "sticker" {
		return ""
	}
	var body struct {
		FileKey string `json:"file_key"`
	}
	if json.Unmarshal([]byte(x.ContentRaw), &body) != nil {
		return ""
	}
	return body.FileKey
}

// reactorLimit is how many of an emoji's reactors are named. It is what the
// client shows, and past it a name costs more width than it tells.
const reactorLimit = 3

// reactors is who put one emoji on the message: the first few by name, then
// how many more there are. The reader is 你, which is the only mark a chip of
// theirs carries — nothing else on the strip needs a colour to say it.
//
// Somebody the contacts table has never seen is counted rather than named: a
// raw open id on screen says nothing.
func reactors(c emoji.Chip, st msgStyle) string {
	names := make([]string, 0, reactorLimit)
	for _, id := range c.Operators {
		if len(names) == reactorLimit {
			break
		}
		switch {
		case id == st.self:
			names = append(names, "你")
		case st.people[id] != "":
			names = append(names, st.people[id])
		}
	}
	rest := max(0, c.Count-len(names))
	who := strings.Join(names, "、")
	if rest > 0 {
		who = strings.TrimPrefix(who+" +"+strconv.Itoa(rest), " ")
	}
	return who
}

// reactionChip is one emoji's standing on a message, drawn as the chip the
// client puts it on: the emoji and who put it there share one tint, closed by
// a round cap either side. The emoji is a Unicode character where one carries
// the same feeling, the client's own picture where none does, and the client's
// name for it where this terminal draws no pictures at all.
//
// The strip wraps between chips but never cuts inside one, so a chip crowded
// with names is fitted here rather than left to run past the pane.
func reactionChip(c emoji.Chip, st msgStyle) []rowSeg {
	e, known := emoji.ByKey(c.Key)
	label := "[" + c.Key + "]"
	var pic picture
	switch {
	case !known:
	case e.Glyph != "":
		label = e.Glyph
	default:
		if pic = st.emojiChip(e.Key); pic.cols > 0 {
			label = ""
		} else {
			label = "[" + e.ZH + "]"
		}
	}
	// Nothing is spaced off the caps: a cap's flat side is the cell edge it
	// hands over on, and a picture is drawn at its own shape inside cells
	// rounded up to the grid, so the slack at its right edge is the only gap
	// the chip needs. A character label does pay for the space parting it from
	// the names, so it carries that space into the budget the names get.
	if label != "" {
		label += " "
	}
	who := truncate(reactors(c, st), st.inner()-chipPad-pic.cols-lipgloss.Width(label))
	if pic.cols > 0 {
		return []rowSeg{{text: stChipEdge.Render(chipLeft)}, {pic: pic},
			{text: stChip.Render(who) + stChipEdge.Render(chipRight)}}
	}
	// A chip of characters alone stays one piece, so a strip made of them is
	// ordinary text that a selected row can still tint.
	return []rowSeg{{text: stChipEdge.Render(chipLeft) +
		stChip.Render(strings.TrimSpace(label+who)) + stChipEdge.Render(chipRight)}}
}

func segsWidth(segs []rowSeg) int {
	w := 0
	for _, s := range segs {
		if s.pic.cols > 0 {
			w += s.pic.cols
			continue
		}
		w += lipgloss.Width(s.text)
	}
	return w
}

// reactionRows draw the emoji a message collected, below its body the way the
// Feishu client puts them. A recalled message keeps no reactions: the client
// drops them with the body.
//
// The chips are packed by hand rather than wrapped: a picture stands in the
// text as placeholder cells the terminal fills, and a wrap that measured them
// as the characters they are would break one apart.
func reactionRows(x store.Message, idx int, st msgStyle, g *gutters) []msgRow {
	if x.Deleted {
		return nil
	}
	chips := emoji.Summary(x.ReactionsJSON, st.self)
	if len(chips) == 0 {
		return nil
	}
	// Two spaces between chips: one reads as part of the count before it.
	const gap = "  "
	var rows []msgRow
	var line []rowSeg
	width := 0
	flush := func() {
		if len(line) == 0 {
			return
		}
		// A row of nothing but characters is ordinary text, and stays so: the
		// pieces exist for pictures, and a row made of them takes no selection
		// tint. Only a strip that actually carries one gives that up.
		row := msgRow{idx: idx}
		if slices.ContainsFunc(line, func(s rowSeg) bool { return s.pic.cols > 0 }) {
			row.segs = append([]rowSeg{{text: g.take()}}, line...)
		} else {
			row.text = g.take()
			for _, s := range line {
				row.text += s.text
			}
		}
		rows = append(rows, row)
		line, width = nil, 0
	}
	for _, c := range chips {
		segs := reactionChip(c, st)
		w := segsWidth(segs)
		if len(line) > 0 {
			if width+len(gap)+w > st.inner() {
				flush()
			} else {
				line = append(line, rowSeg{text: gap})
				width += len(gap)
			}
		}
		line = append(line, segs...)
		width += w
	}
	flush()
	return rows
}

// cardRows lay a card out below its sender line: the text lines carry the
// frame themselves, and the pictures the body names are placed inside it.
func cardRows(c card, x store.Message, idx int, st msgStyle, g *gutters, ms mentions) []msgRow {
	edge := cardEdge()
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
	cols, label := st.inner()-lipgloss.Width(edge), "[图片]"
	if x.MsgType == "sticker" {
		cols, label = min(cols, stickerCols), "[表情]"
	}
	stand := func() []msgRow {
		return []msgRow{{text: g.take() + edge + stDim.Render(label), idx: idx}}
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
	pic := st.place(path, cols, st.height)
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

// daySeparator splits the list where the calendar day changes. An odd number
// of columns to share goes to the right arm rather than being dropped: fit
// would pad the shortfall with a space, and the rule would stop one column
// short of the pane edge.
func daySeparator(label string, width int) string {
	room := max(0, width-lipgloss.Width(label)-2)
	left := room / 2
	return stDim.Render(strings.Repeat("─", left) + " " + label + " " + strings.Repeat("─", room-left))
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
