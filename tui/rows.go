package tui

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
)

const (
	// leadWidth is the columns every row of a message opens with: the sender's
	// disc, drawn at the size the chat list draws one, then the marker column.
	leadWidth = avatarWidth + 1
	// runSpan is how long a sender's block stays open, measured from the
	// message that opened it, so a name line still marks a sender coming back
	// rather than a whole day collapsing into one block.
	runSpan = 5 * time.Minute
)

// msgRow is one rendered line of a message list and the message it belongs to.
type msgRow struct {
	// lead is the columns the row opens with: the sender's disc and the
	// marker beside it. A day rule and a system notice carry none and take
	// the whole width.
	lead lead
	text string
	idx  int // index into the backing message slice
	// plain marks a row that belongs to no message — a day separator, or the
	// blank line parting one message from the next — so the selection never
	// paints it.
	plain bool
	// pic is set on the rows a picture occupies, picRow being which of its
	// rows this one is. Those rows carry an image rather than text, so they
	// are neither fitted nor highlighted, and their text is only known once
	// the terminal holds the picture.
	pic    picture
	picRow int
	// zones are the click targets this row carries, in the order they are
	// drawn: a call's join button, the card an attachment is drawn as, or one
	// per pill of a card's action row.
	zones []clickZone
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
// in the pane's own content coordinates, and where clicking it leads. No urls
// is no target at all.
type clickZone struct {
	x0, x1 int
	// urls are opened together in one invocation. A message's pictures share
	// a zone each so that pressing any of them opens the lot in one viewer,
	// the one pressed first; everything else leads to a single place.
	urls []string
	// react is the emoji a reaction chip toggles when it is pressed. A chip is
	// not a place to open: pressing one puts the reader's own reaction on the
	// message or takes it back, which is what the client does.
	react string
	// label names the target the way the chooser lists it, and note is what
	// the status bar says once it has been handed over. They differ because a
	// list wants the thing and a status line wants the act.
	label string
	note  string
}

// live reports whether the zone leads anywhere at all.
func (z clickZone) live() bool { return len(z.urls) > 0 || z.react != "" }

func (z clickZone) hit(x int) bool { return z.live() && x >= z.x0 && x < z.x1 }

// placeZones recomputes a row's click targets from the widths of its pieces,
// so a caller that has just changed a piece — a list marker taking the place
// of an indent — does not have to know what the targets were.
func (r *msgRow) placeZones() {
	r.zones = nil
	x := r.lead.cols()
	for _, s := range r.segs {
		w := s.cols()
		if len(s.urls) > 0 {
			r.zones = append(r.zones, clickZone{x0: x, x1: x + w, urls: s.urls, label: s.label, note: s.note})
		}
		x += w
	}
}

// reindent swaps the indent a row opens with for s, whichever way the row
// holds its text, and puts the targets back where the new width leaves them.
func (r *msgRow) reindent(indent, s string) {
	if len(r.segs) == 0 {
		r.text = s + strings.TrimPrefix(r.text, indent)
		return
	}
	r.segs[0].text = s + strings.TrimPrefix(r.segs[0].text, indent)
	r.placeZones()
}

// rowSeg is one piece of a row: text, or a picture the terminal fills in. A
// piece that leads somewhere carries the target, which placeZones turns into
// the row's click ranges once the pieces are laid out.
type rowSeg struct {
	text string
	pic  picture
	urls []string
	// label and note travel with the target, meaning what they mean on
	// clickZone.
	label string
	note  string
}

// cols is how much of a row the piece takes.
func (s rowSeg) cols() int {
	if s.pic.cols > 0 {
		return s.pic.cols
	}
	return ansi.StringWidth(s.text)
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
	docs    map[string]store.DocLabel   // Feishu documents linked to, by store.DocRef.Key
	outbox  map[string]outboxState      // the sends still on their way, by the id their rows carry
	dots    map[string]bool             // the messages this visit draws the unread marker on
	// reacts are the reaction presses Feishu has not answered yet, by message
	// id then folded emoji key, laid over the stored summary so a press draws
	// before it is sent.
	reacts map[string]map[string]bool
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
	// disc places a sender's picture as a circle: their avatar file, or one
	// drawn from their name when they have none. Nil draws the colour block.
	disc func(file, id, name string, cols, rows int) picture
	// dataDir is where downloads live: the pictures cut out of the emoji
	// sprite sheet, and the attachments a card opens. Empty is a pane with no
	// store behind it, where an emoji falls back to its name and nothing
	// opens.
	dataDir string
	// dark says which way the terminal's background leans, which is what picks
	// the palette a code block is coloured from.
	dark bool
	// people names a reaction's operators, by open id. A sender's name
	// travels on the message itself; a reactor's does not — the block holds
	// an id alone, and the contacts table is where a name for it lives.
	people map[string]string
	// avatars are the senders' downloaded pictures, by open id, relative to
	// the data dir. A sender absent from it falls back to the colour block.
	avatars map[string]string
	// p2p drops the sender's name from the line that opens a block: in a chat
	// of two, the disc beside the block already says which of them spoke.
	p2p bool
	// peer is who the reader is talking to in such a chat, which is how far an
	// @ in it carries: a name that is neither of theirs reaches nobody here.
	peer string
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
	return emojiPics{place: st.place, dir: st.dataDir}.chip(key, emojiCols)
}

// emojiInline is one emoji's picture standing in a line of body text. It
// carries no chip tint: inside a message an emoji is part of what was said,
// not a reaction put on it afterwards.
func (st msgStyle) emojiInline(key string) picture {
	return emojiPics{place: st.place, dir: st.dataDir}.pic(key, emojiCols)
}

// docLabel names the Feishu document a URL addresses, when one has been read
// for it. A URL that names no document, or one whose title has not been read
// yet, is left as the address it is.
func (st msgStyle) docLabel(url string) (store.DocLabel, bool) {
	ref, ok := store.ParseDocURL(url)
	if !ok {
		return store.DocLabel{}, false
	}
	l, ok := st.docs[ref.Key()]
	return l, ok
}

// inner is the width a message body has, once the lead is taken off.
func (st msgStyle) inner() int { return st.width - leadWidth }

// lead is the columns a row opens with: the sender's disc, then the one
// column carrying the marks belonging to the message. Only the
// row that opens a block draws a disc; the rest hold its cells blank so every
// body line keeps the same column.
type lead struct {
	// pic is the sender's disc, picRow being which of its rows this one is.
	// It is unset where there is no picture to place.
	pic    picture
	picRow int
	box    string // what stands in the disc's cells: a colour block, or blanks
	mark   string // one column: the unread dot, the reply mark
}

// cols is how much of a row the lead takes. A zero lead belongs to a row that
// opens with nothing — a day rule, a system notice — which spans the pane.
func (l lead) cols() int {
	if l.box == "" && l.pic.cols == 0 {
		return 0
	}
	return leadWidth
}

// block is what a sender's run of messages shares: the disc drawn beside it,
// handed out one row at a time.
//
// The disc belongs to the block rather than to the message that opened it,
// because it is taller than one line: a run of two short messages draws the
// top of the circle beside the first and the bottom beside the second.
type block struct {
	disc []lead
	n    int
	idx  int // the message the disc's leftover rows are charged to
}

// take is the next row of the disc, or the blank standing in once it is spent.
func (b *block) take() lead {
	if b.n < len(b.disc) {
		l := b.disc[b.n]
		b.n++
		l.mark = " "
		return l
	}
	return lead{box: strings.Repeat(" ", avatarWidth), mark: " "}
}

// openBlock starts a sender's run with the disc drawn beside it.
func openBlock(x store.Message, st msgStyle) block {
	return block{disc: senderDisc(x, st)}
}

// discTail is the rows of the disc the block never reached: a run of one short
// line would otherwise draw the top of the circle and cut the rest off.
func discTail(b *block) []msgRow {
	var rows []msgRow
	for b.n < len(b.disc) {
		rows = append(rows, msgRow{lead: b.take(), idx: b.idx})
	}
	return rows
}

// leads are the leads one message's rows take: the block's disc while it
// lasts, and the marker the message's own first row carries — the draft's
// reply target, or the dot on an unread one. The dot is the block's, not the
// message's: a block is all read or all unread, so repeating it under the
// sender line would say nothing new.
type leads struct {
	b     *block
	first string
	used  bool
}

func (g *leads) take() lead {
	l := g.b.take()
	if !g.used {
		g.used = true
		if g.first != "" {
			l.mark = g.first
		}
	}
	return l
}

func leadFor(x store.Message, st msgStyle, b *block, opensBlock bool) leads {
	g := leads{b: b}
	switch {
	case x.MessageID == st.quoted:
		g.first = stAccent.Render("↩")
	case opensBlock && unread(x, st):
		g.first = stAccent.Render("●")
	}
	return g
}

// senderDisc is the picture beside the block a sender opens, cut into the rows
// it spans: the round avatar the client draws, at the size the chat list draws
// one, or the colour block standing in for it where there is no picture to
// place — no file downloaded yet, or a terminal that draws none.
func senderDisc(x store.Message, st msgStyle) []lead {
	name := senderLabel(x, "")
	if st.disc != nil {
		if pic := st.disc(st.avatars[x.SenderID], x.SenderID, name, avatarWidth, avatarHeight); pic.cols > 0 {
			rows := make([]lead, 0, pic.rows)
			for row := range pic.rows {
				rows = append(rows, lead{pic: pic, picRow: row})
			}
			return rows
		}
	}
	// The stand-in carries the name on its first line and nothing on the rest,
	// the way the chat list draws the same block.
	rows := []lead{{box: avatarBlock(x.SenderID, name, avatarWidth)}}
	blank := avatarStyle(x.SenderID).Render(strings.Repeat(" ", avatarWidth))
	for range avatarHeight - 1 {
		rows = append(rows, lead{box: blank})
	}
	return rows
}

// unread reports whether a message still carries this visit's marker. The
// answer is the set the panes gathered as pages arrived, not the store's own
// flags, which opening the chat has already cleared.
func unread(x store.Message, st msgStyle) bool { return st.dots[x.MessageID] }

// blockHeads names, for each message, the message whose sender line it sits
// under — itself when it opens a block. It is the one definition of where a
// block starts: the renderer draws the sender line against it, and so does
// the unread dot, which is why clearing a dot has to take the whole block.
func blockHeads(msgs []store.Message, st msgStyle) []int {
	heads := make([]int, len(msgs))
	day, run := "", -1
	for i, x := range msgs {
		d := msgDay(x.CreateMs, st.now)
		if d != day || x.MsgType == "system" || run < 0 || !mergeable(msgs[run], x, st) {
			run = i
		}
		day, heads[i] = d, run
		// A system notice stands alone, so a sender coming back under it
		// opens a block rather than reaching over it.
		if x.MsgType == "system" {
			run = -1
		}
	}
	return heads
}

// renderRows lays messages out as blocks — a sender line followed by every
// body that sender wrote next — split into days.
func renderRows(msgs []store.Message, st msgStyle) []msgRow {
	var rows []msgRow
	var b block
	heads := blockHeads(msgs, st)
	day, prev := "", ""
	// open starts a section — a system notice, a sender's block — holding it
	// off whatever came before with a blank line. A day rule needs none on
	// either side: it is a divider in its own right, and it heads the day
	// below it, so the first section under one takes no blank of its own. The
	// line belongs to the message below it, so scrolling to that message
	// brings its own air along.
	open := func(i int, underRule bool) {
		if len(rows) > 0 && !underRule {
			rows = append(rows, msgRow{idx: i, plain: true})
		}
	}
	for i, x := range msgs {
		d := msgDay(x.CreateMs, st.now)
		rule := d != day
		// A day rule, a system notice and a new sender all end the run above
		// them, so the disc beside it finishes before anything else is drawn.
		opensBlock := heads[i] == i
		if opensBlock {
			rows = append(rows, discTail(&b)...)
		}
		if rule {
			day = d
			rows = append(rows, msgRow{text: daySeparator(d, st.width), idx: i, plain: true})
		}
		if x.MsgType == "system" {
			open(i, rule)
			rows = append(rows, systemRows(x, i, st)...)
			prev = x.MessageID
			continue
		}
		if opensBlock {
			open(i, rule)
			b = openBlock(x, st)
		}
		b.idx = i
		g := leadFor(x, st, &b, opensBlock)
		if opensBlock {
			// A chat of two writes no head line unless the message carries a
			// badge, so the disc lands on the first line of the body instead.
			if head := headLine(x, st); head != "" {
				rows = append(rows, msgRow{lead: g.take(), text: head, idx: i})
			}
		}
		if q, ok := quoteRow(x, prev, i, st, &g); ok {
			rows = append(rows, q)
		}
		rows = append(rows, bodyRows(x, i, st, &g)...)
		rows = append(rows, reactionRows(x, i, st, &g)...)
		prev = x.MessageID
	}
	return append(rows, discTail(&b)...)
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
// own can show. A send still on its way is one: how far it has got is spelled
// out on that line and nowhere else.
func solo(x store.Message, st msgStyle) bool {
	if (x.ThreadID != "" && x.MessagePosition >= 0) || x.EditedAt > 0 {
		return true
	}
	_, ok := st.outbox[x.MessageID]
	return ok
}

// quoteRow names the message a reply answers, above its body, the way the
// Feishu client quotes it. A reply to the message right above says nothing
// the list does not already show, so that one is left out.
func quoteRow(x store.Message, prev string, idx int, st msgStyle, g *leads) (msgRow, bool) {
	if x.ReplyTo == "" || x.ReplyTo == prev {
		return msgRow{}, false
	}
	row := func(s string) msgRow { return msgRow{lead: g.take(), text: stDim.Render(s), idx: idx} }
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
//
// A chat of two names nobody: the disc says which of the two spoke, and a name
// repeated down the pane says nothing else. The line is then drawn only for
// the badges, and "" leaves the block to open with its own first body line.
func headLine(x store.Message, st msgStyle) string {
	var parts []string
	if !st.p2p {
		// The name is dim: the disc beside it is what picks a sender out of
		// the list, and a bold name on every block would shout over the words.
		name := stDim.Render(displaySender(x, st.self, st.suffix[x.SenderID]))
		if st.names != nil {
			chat := st.names[x.ChatID]
			if chat == "" {
				chat = x.ChatID
			}
			name = stAccent.Render(truncate(flatten(chat), 18)) + " " + name
		}
		parts = append(parts, name)
	}
	if x.ThreadID != "" && x.MessagePosition >= 0 {
		parts = append(parts, stAccent.Render("⤷thread"))
	}
	if x.EditedAt > 0 {
		parts = append(parts, stDim.Render("(Edited)"))
	}
	// Absent from the map is the ordinary case — a message the store
	// returned — which no zero value may stand in for.
	if state, ok := st.outbox[x.MessageID]; ok {
		if state == outFailed {
			parts = append(parts, stErr.Render("(failed)"))
		} else {
			parts = append(parts, stDim.Render("(sending)"))
		}
	}
	return strings.Join(parts, " ")
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
func bodyRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	inner := st.inner()
	text := func(lines []string) []msgRow {
		out := make([]msgRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, msgRow{lead: g.take(), text: l, idx: idx})
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
	// An attachment's body likewise names the whole card, and the text a
	// rendering would bring is the markup the card replaces.
	if a, ok := attachmentOf(x.MsgType, x.ContentRaw); ok {
		return attachRows(a, x, idx, st, g)
	}
	ms := mentionsIn(x.MentionsJSON, st.self).facing(st.peer)
	// A card describes itself in full, so it is drawn as soon as it lands:
	// waiting on a rendering would hold back the whole of what it says.
	if x.MsgType == "interactive" {
		if c, ok := card.Parse(x.ContentRaw); ok {
			return cardRows(c, x, idx, st, g, ms)
		}
	}
	if x.RenderedAt == 0 {
		return text(wrap(stDim.Render(expandEmoji(pendingText(x.MsgType, x.ContentRaw))), inner))
	}
	// A sticker renders as the text "[Sticker]", which names the picture
	// nowhere: the key is in the body.
	if key := stickerKey(x); key != "" {
		return pictureRows(key, x, idx, st, g)
	}
	// A post is markdown by construction, so it is drawn as the document it
	// is. A text message is not: someone typing "3 * 4 * 5" means the
	// asterisks, so that one keeps the literal path below.
	if x.MsgType == "post" {
		return mdRows(x.Content, x, idx, st, g, ms)
	}

	var rows []msgRow
	content := strings.ReplaceAll(strings.ReplaceAll(x.Content, "\r", ""), "\t", "    ")
	for _, line := range strings.Split(content, "\n") {
		keys, rest := splitImages(line)
		if len(keys) == 0 || strings.TrimSpace(rest) != "" {
			if segs := inlineSegs(rest, ms, st.emojiInline, st.docLabel); segs != nil {
				rows = append(rows, segRows(segs, "", idx, st, g)...)
			} else {
				rows = append(rows, text(wrap(renderInline(rest, ms), inner))...)
			}
		}
		for _, key := range keys {
			rows = append(rows, pictureRows(key, x, idx, st, g)...)
		}
	}
	return rows
}

// segRows draw one body line as the pieces the client's own emoji pictures
// and its links sit between, where a plain string cannot hold them. pad is
// the indent nesting has already charged the line, which every wrapped row
// carries: the string path pads after wrapping too, and a piece that leads
// somewhere would otherwise be clicked one indent left of where it is drawn.
func segRows(segs []rowSeg, pad string, idx int, st msgStyle, g *leads) []msgRow {
	packed := wrapSegs(segs, st.inner()-lipgloss.Width(pad))
	out := make([]msgRow, 0, len(packed))
	for _, row := range packed {
		if pad != "" {
			row = append([]rowSeg{{text: pad}}, row...)
		}
		r := msgRow{lead: g.take(), idx: idx, tinted: true, segs: row}
		r.placeZones()
		out = append(out, r)
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
func reactionRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	if x.Deleted {
		return nil
	}
	chips := pendingChips(emoji.Summary(x.ReactionsJSON, st.self), st.reacts[x.MessageID], st.self)
	if len(chips) == 0 {
		return nil
	}
	// Two spaces between chips: one reads as part of the count before it.
	const gap = "  "
	var rows []msgRow
	var line []rowSeg
	// zones are the chips packed onto the line so far, in the strip's own
	// columns; flush moves them behind the lead once the row exists. They are
	// kept beside the pieces rather than derived from them because a chip is
	// one piece or three depending on whether its emoji is a picture.
	var zones []clickZone
	width := 0
	flush := func() {
		if len(line) == 0 {
			return
		}
		// A row of nothing but characters is ordinary text, and stays so: the
		// pieces exist for pictures, and a row made of them takes no selection
		// tint. Only a strip that actually carries one gives that up.
		row := msgRow{lead: g.take(), idx: idx}
		if slices.ContainsFunc(line, func(s rowSeg) bool { return s.pic.cols > 0 }) {
			row.segs = line
		} else {
			for _, s := range line {
				row.text += s.text
			}
		}
		for _, z := range zones {
			z.x0, z.x1 = z.x0+row.lead.cols(), z.x1+row.lead.cols()
			row.zones = append(row.zones, z)
		}
		rows = append(rows, row)
		line, zones, width = nil, nil, 0
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
		zones = append(zones, clickZone{x0: width, x1: width + w, react: c.Key})
		line = append(line, segs...)
		width += w
	}
	flush()
	return rows
}

// cardRows lay a card out below its sender line: the header band, the body as
// the document the card describes, and the pictures and buttons placed among
// it.
func cardRows(c card.Card, x store.Message, idx int, st msgStyle, g *leads, ms mentions) []msgRow {
	var rows []msgRow
	text := func(s string) {
		for _, line := range wrap(s, st.inner()) {
			rows = append(rows, msgRow{lead: g.take(), text: line, idx: idx})
		}
	}
	if head := cardHead(c); head != "" {
		text(head)
	}
	for _, b := range c.Blocks {
		switch {
		case b.ImageKey != "":
			rows = append(rows, pictureRows(b.ImageKey, x, idx, st, g)...)
		case len(b.Buttons) > 0:
			for _, l := range cardButtons(b.Buttons, st.inner(), feishuChatLink(x.ChatID, x.MessagePosition)) {
				row := msgRow{lead: g.take(), text: l.text, idx: idx}
				for _, z := range l.zones {
					z.x0, z.x1 = z.x0+row.lead.cols(), z.x1+row.lead.cols()
					row.zones = append(row.zones, z)
				}
				rows = append(rows, row)
			}
		default:
			rows = append(rows, mdRows(b.Markdown, x, idx, st, g, ms)...)
		}
	}
	return rows
}

// pictureRows reserve the cells a downloaded image will occupy, behind the
// lead. A picture this terminal cannot draw — no graphics protocol, not
// downloaded yet, a format the decoder will not read — falls back to a
// one-line stand-in.
func pictureRows(key string, x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	cols, label := st.inner(), "[图片]"
	if x.MsgType == "sticker" {
		cols, label = min(cols, stickerCols), "[表情]"
	}
	pic := placePicture(key, x, st, cols)
	if pic.cols == 0 {
		return []msgRow{{lead: g.take(), text: stDim.Render(label), idx: idx}}
	}
	rows := picRows(pic, idx, g)
	// A sticker is a gesture, not a picture to study, and pressing one in the
	// client opens nothing either.
	if x.MsgType == "sticker" {
		return rows
	}
	zone, ok := pictureZone(key, x, st)
	if !ok {
		return rows
	}
	for i := range rows {
		z := zone
		z.x0, z.x1 = rows[i].lead.cols(), rows[i].lead.cols()+pic.cols
		rows[i].zones = []clickZone{z}
	}
	return rows
}

// pictureZone is what pressing one of a message's pictures opens: that
// picture, and behind it every other one the message carries.
//
// They go over as one target rather than one each because that is what the
// client does — pressing any picture opens the viewer on it with the rest of
// the message beside it — and because macOS puts files opened together in one
// window, the first of them showing.
func pictureZone(key string, x store.Message, st msgStyle) (clickZone, bool) {
	var pressed string
	var rest []string
	for _, r := range st.res[x.MessageID] {
		if r.Type != "image" || r.Status != "done" {
			continue
		}
		path := dataPath(st.dataDir, r.LocalPath)
		if path == "" {
			continue
		}
		if r.FileKey == key {
			pressed = path
		} else {
			rest = append(rest, path)
		}
	}
	if pressed == "" {
		return clickZone{}, false
	}
	label := "图片"
	if n := len(rest) + 1; n > 1 {
		label = strconv.Itoa(n) + " 张图片"
	}
	return clickZone{urls: append([]string{pressed}, rest...), label: label, note: "opening " + label}, true
}

// placePicture sizes one of a message's downloaded pictures for the pane. A
// zero size means there is nothing to draw: no graphics protocol, a key the
// message does not carry, a download still on its way, or a format the
// decoder will not read.
func placePicture(key string, x store.Message, st msgStyle, cols int) picture {
	if st.place == nil || key == "" {
		return picture{}
	}
	r := attachRes(key, x, st)
	if r.Status != "done" {
		return picture{}
	}
	return st.place(r.LocalPath, cols, st.height)
}

// picRows hold a picture's cells, one row of the pane per cell row.
func picRows(pic picture, idx int, g *leads) []msgRow {
	rows := make([]msgRow, 0, pic.rows)
	for row := range pic.rows {
		rows = append(rows, msgRow{idx: idx, pic: pic, picRow: row, lead: g.take()})
	}
	return rows
}

// splitImages pulls the image references out of one line and returns them
// with what is left of the text.
func splitImages(line string) (keys []string, rest string) {
	rest = sync.ImageRef.ReplaceAllStringFunc(line, func(m string) string {
		g := sync.ImageRef.FindStringSubmatch(m)
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
