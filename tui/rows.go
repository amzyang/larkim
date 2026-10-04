package tui

import (
	"cmp"
	"encoding/json"
	"iter"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
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
	// rule marks the line parting one group of rows from the next: a search
	// group, or one chat's stretch of the Unread panel. The panel pins the
	// section's rule over the pane, so it has to know when the row under the
	// pin is that rule already.
	rule bool
	// pic is set on the rows a picture occupies, picRow being which of its
	// rows this one is. Those rows carry an image rather than text, so they
	// are padded rather than fitted, and their cells are only known once the
	// terminal holds the picture.
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
	// panel marks a row a card lays its panel under: the shade the client
	// puts behind a column_set given a background.
	panel bool
	// tinted marks a row of pieces a selection still colours. Body text earns
	// it — an emoji drawn over the tint is what the client shows, and a row
	// left plain inside a selected block reads as a hole. A chip does not: it
	// carries a tint of its own that the selection's would fight.
	tinted bool
}

// pictures is every picture the row draws, in the order it draws them: the
// disc it opens with, the image it may be whole, and the ones standing inside
// its text. It is the one answer to what a row has to have transmitted, which
// both the panes and the printed page ask.
func (r msgRow) pictures() iter.Seq[picture] {
	return func(yield func(picture) bool) {
		if !yield(r.lead.pic) || !yield(r.pic) {
			return
		}
		for _, s := range r.segs {
			if !yield(s.pic) {
				return
			}
		}
	}
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
	// jump is the message a quote line leads back to. It is not a place to
	// open but a place to stand: pressing it puts the cursor on the message
	// the reply answers, fetching the page that holds it when it is off this
	// one.
	jump string
	// open is the container a summary line leads into, and openKind which
	// sort. It is not a place to open but a pane to show. The kind is carried
	// rather than read off the id's prefix: nothing in this repository
	// decides behaviour that way, and Feishu promises nothing about it.
	// openRoot is the bundle a forward's rows are stored under, which is the
	// outermost one when the line sits inside a frame.
	// openName is the title the summary drew, carried so the frame opens
	// under the same name the card that led to it showed.
	open     string
	openRoot string
	openName string
	openKind rightKind
	// act is one of the assistant panel's own acts — a card's actions, the
	// head's Regenerate or Stop — which are not places to open but things to
	// do, naming the answer and the card within it.
	act aiAct
	// cand is a lark-watch reply draft offered under the message.
	cand candZone
	// task names the task a todo's checkbox toggles when pressed, and
	// taskDone the state the box is drawn in — a press flips it. A checkbox
	// is not a place to open but a thing to do, which is what the client
	// does with one.
	task     string
	taskDone bool
	// label names the target the way the chooser lists it, and note is what
	// the status bar says once it has been handed over. They differ because a
	// list wants the thing and a status line wants the act.
	label string
	note  string
}

// live reports whether the zone leads anywhere at all.
func (z clickZone) live() bool {
	return len(z.urls) > 0 || z.react != "" || z.jump != "" || z.open != "" || z.task != "" || z.act.kind != actNone || z.cand.c.Mid != ""
}

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

// addZones hangs targets measured from where the row's content starts, which
// puts them behind its lead.
func (r *msgRow) addZones(zs []clickZone) {
	dx := r.lead.cols()
	for _, z := range zs {
		z.x0, z.x1 = z.x0+dx, z.x1+dx
		r.zones = append(r.zones, z)
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

// shift charges a picture row the indent its block owes. The blanks ride in
// text, which a picture row otherwise leaves unset, because the cells are the
// terminal's to fill and nothing can be written in front of them but the
// columns they start at; the target over them moves the same distance.
func (r *msgRow) shift(pad string) {
	if pad == "" {
		return
	}
	r.text = pad + r.text
	w := lipgloss.Width(pad)
	for i := range r.zones {
		r.zones[i].x0 += w
		r.zones[i].x1 += w
	}
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
	width    int
	self     string
	selfName string
	now      time.Time
	quoted   string                      // the message an open draft replies to
	parents  map[string]store.Message    // the messages these replies answer, by parent id
	suffix   map[string]string           // sender open id → account suffix
	res      map[string][]store.Resource // attachments, by message id
	docs     map[string]store.DocLabel   // Feishu documents linked to, by store.DocRef.Key
	outbox   map[string]outboxState      // the sends still on their way, by the id their rows carry
	dots     map[string]bool             // the messages this visit draws the unread marker on
	// reacts are the reaction presses Feishu has not answered yet, by message
	// id then folded emoji key, laid over the stored summary so a press draws
	// before it is sent.
	reacts map[string]map[string]bool
	// forwards is the collapsed line of each merged forward on the page, by
	// the bundle's message id, and forwardRoot the bundle a frame's own rows
	// belong to — empty in a chat, where every bundle is its own root.
	forwards    map[string]store.ForwardGist
	forwardRoot string
	// threads is the collapsed line of each thread rooted on the page, by
	// thread id, and replies the tree each message belongs to, by message id.
	// inFrame says these rows are a container's own, where a summary would
	// count the replies standing right below it.
	threads map[string]store.ThreadGist
	replies map[string]store.ReplyGist
	inFrame bool
	// names labels each message's chat on its sender line. It is set for
	// search results, which run across chats; inside one chat, naming it on
	// every block says nothing.
	names map[string]string
	// hits are the words the search panel is looking for. They are marked
	// where they stand in a body or a sender name, which is what says why a
	// block is in the list at all. Empty on every other page.
	hits []string
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
	// candidates are pending lark-watch drafts keyed by source message id.
	candidates map[string][]store.Candidate
}

// emojiPics sizes the pictures cut out of the sprite sheet. The zero value
// draws none, which is what a terminal without graphics, or a data dir they
// were never cut into, gets.
type emojiPics struct {
	place func(path string, maxCols, maxRows int) picture
	dir   string
}

// pic sizes one emoji's picture to sit on a line of text: one row tall, and
// up to cols wide. A zero size means there is no renderer to draw with, and
// the caller falls back to the emoji's name or leaves it out. A renderer that
// cannot produce the picture is a broken start rather than a degraded one —
// startup cut every picture before any of this runs — so it panics.
func (p emojiPics) pic(key string, cols int) picture {
	if p.place == nil || p.dir == "" {
		return picture{}
	}
	path := emoji.Picture(p.dir, key)
	pic := p.place(path, cols, 1)
	if pic.cols == 0 {
		panic("emoji picture unreadable: " + path)
	}
	return pic
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

// picBox bounds one picture in the message flow. Drawn at its own pixels a
// screenshot is a whole screenful, and the conversation it was sent into
// scrolls away behind it; the client hangs an inline picture in a box of its
// own and opens the full one on a click, which is the same trade — enough of
// the picture to know what it is, the rest a keypress away. A sticker sets its
// own, tighter size and does not come through here.
func (st msgStyle) picBox() (cols, rows int) {
	return st.inner() / 2, max(1, st.height/3)
}

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
		if d != day || standsAlone(x) || run < 0 || !mergeable(msgs[run], x, st) {
			run = i
		}
		day, heads[i] = d, run
		// A notice stands alone, so a sender coming back under it opens a
		// block rather than reaching over it.
		if standsAlone(x) {
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
	day := ""
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
		// A notice carries no sender line, no quote, no card, no reactions
		// and no thread summary: continuing here is what drops them all.
		if standsAlone(x) {
			open(i, rule)
			if x.Deleted {
				rows = append(rows, recallRows(x, i, st)...)
			} else {
				rows = append(rows, systemRows(x, i, st)...)
			}
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
		if q, ok := quoteRow(x, i, st, &g); ok {
			rows = append(rows, q)
		}
		// A message can be both, and the thread wins: a forward somebody
		// started a topic on is read in the topic, where the forward is one
		// row that opens in turn.
		if !threadRoot(x, st) {
			if rs, ok := forwardSummary(x, st.forwardRoot, i, st, &g); ok {
				rows = append(rows, rs...)
			}
		}
		rows = append(rows, bodyRows(x, i, st, &g)...)
		rows = append(rows, reactionRows(x, i, st, &g)...)
		rows = append(rows, candidateRows(x, i, st, &g)...)
		// Outermost, past the reactions: those decorate the message itself,
		// where these lines point away from it at the answers it drew.
		if rs, ok := threadRows(x, i, st, &g); ok {
			rows = append(rows, rs...)
		}
		if r, ok := replySummary(x, i, st, &g); ok {
			rows = append(rows, r)
		}
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
// own can show. A failed send is one: the failure is spelled out on that line
// and nowhere else.
func solo(x store.Message, st msgStyle) bool {
	// A container brings a summary line of its own, which belongs under a
	// sender line rather than inside somebody else's block.
	if (x.ThreadID != "" && x.MessagePosition >= 0) || x.EditedAt > 0 || x.MsgType == "merge_forward" {
		return true
	}
	return st.outbox[x.MessageID] == outFailed
}

// quoteRow names the message a reply answers, above its body, the way the
// Feishu client quotes it. It is drawn for every reply, including one
// answering the message directly above: the client quotes that one too, and a
// reply that reads as an ordinary next line is a reply the reader cannot see.
//
// The line says "Reply to" outright because the bar it opens with is the same
// one a merged forward's card draws, and a card's preview lines read
// "Name: text" too — without the words the two containers look alike.
func quoteRow(x store.Message, idx int, st msgStyle, g *leads) (msgRow, bool) {
	if x.ReplyTo == "" {
		return msgRow{}, false
	}
	// The whole line is the target, the way the client makes the quote block
	// one: it names a single message, and nothing else is drawn beside it.
	row := func(text string, segs []rowSeg) msgRow {
		l := g.take()
		x0 := l.cols()
		return msgRow{lead: l, text: text, segs: segs, idx: idx,
			zones: []clickZone{{x0: x0, x1: x0 + lipgloss.Width(text) + segsWidth(segs), jump: x.ReplyTo}}}
	}
	parent, ok := st.parents[x.ReplyTo]
	if !ok {
		return row(stDim.Render("▏Reply to (not synced)"), nil), true
	}
	head := "▏Reply to " + displaySender(parent, st.self, st.suffix[parent.SenderID]) + ": "
	gist := replyGist(parent)
	if segs := gistSegs(stDim.Render(head), gist, st.inner(), spellOf(parent.MsgType), stDim, st.emojiGist); segs != nil {
		return row("", segs), true
	}
	return row(stDim.Render(head+truncate(gist, st.inner()-lipgloss.Width(head))), nil), true
}

// senderLabel names a message's sender: the display name Feishu sent, the
// open id when it sent none, and the account suffix that tells same-named
// colleagues apart.
func senderLabel(x store.Message, suffix string) string {
	return personName(cmp.Or(flatten(x.SenderName), x.SenderID), suffix)
}

// displaySender names a sender on a list: the reader reads as 你, the way the
// chat list already names them, and an app carries the badge that says the
// turn is a machine's.
func displaySender(x store.Message, self, suffix string) string {
	if x.SenderID == self {
		return "You"
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
		// A search matches sender names as well as bodies, so the mark goes
		// on here too — a hit whose only match is the name would otherwise
		// look like a block the panel included for no reason.
		who := displaySender(x, st.self, st.suffix[x.SenderID])
		name := markName(who, hitPositions(who, st.hits), stDim)
		if st.names != nil {
			chat := cmp.Or(st.names[x.ChatID], x.ChatID)
			name = stAccent.Render(truncate(flatten(chat), 18)) + " " + name
		}
		parts = append(parts, name)
	}
	// The badges are fainter than the name they follow, which stDim already
	// draws: in one grey the two would read as equals, and the name is what
	// the reader is scanning for.
	if x.EditedAt > 0 {
		parts = append(parts, stFaint.Render("(Edited)"))
	}
	// A send on its way draws as the message it will be: failing is rare
	// enough that only a failure is worth a word.
	if st.outbox[x.MessageID] == outFailed {
		parts = append(parts, stErr.Render("(failed)"))
	}
	return strings.Join(parts, " ")
}

// standsAlone reports whether a message is a notice rather than something
// somebody said: it takes no sender line and merges into no block.
func standsAlone(x store.Message) bool { return x.MsgType == "system" || x.Deleted }

// systemRows draw a system message the way the client does: centred and
// muted, with no sender of its own.
func systemRows(x store.Message, idx int, st msgStyle) []msgRow {
	return noticeRows(flatten(x.Content), idx, st)
}

// recallRows name who took a message back. The body is not drawn: a recall
// removes it for everybody, and the client says only that it happened.
func recallRows(x store.Message, idx int, st msgStyle) []msgRow {
	who := displaySender(x, st.self, st.suffix[x.SenderID])
	return noticeRows(who+" recalled a message.", idx, st)
}

func noticeRows(text string, idx int, st msgStyle) []msgRow {
	var rows []msgRow
	for _, line := range wrap(stDim.Render(text), st.width-4) {
		rows = append(rows, msgRow{text: centre(line, st.width), idx: idx})
	}
	return rows
}

// bodyRows render one message's content below its sender line: a card as a
// framed block, a picture as the picture itself, everything else as text.
func bodyRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	// A merged forward's summary line is its body. What lark-cli renders it
	// into is the whole tree, tags and ISO timestamps included, which is
	// exactly what the list must not print.
	if x.MsgType == "merge_forward" {
		return nil
	}
	// A call's body is the whole invite, so its card is drawn without
	// waiting for a rendering: the button matters most in the first seconds.
	if v, ok := videoChatOf(x); ok {
		return videoChatRows(v, idx, st, g)
	}
	// An event's body likewise names the whole card, and the rendering it
	// would get wraps the same two lines in XML.
	if c, ok := calendarOf(x); ok {
		return calendarRows(c, x, idx, st, g)
	}
	// A task's card needs both halves of it: the body names the guid its
	// detail page opens on, while the rendering carries the checkbox the
	// task list last reported.
	if x.MsgType == "todo" {
		if rows := todoRows(x, idx, st, g); rows != nil {
			return rows
		}
	}
	// An attachment's body likewise names the whole card, and the text a
	// rendering would bring is the markup the card replaces.
	if a, ok := attachmentOf(x.MsgType, x.ContentRaw); ok {
		return attachRows(a, x, idx, st, g)
	}
	ms := mentionsIn(x.MentionsJSON, st.self).facing(st.peer).marking(st.hits).spelling(spellOf(x.MsgType))
	// A card describes itself in full, so it is drawn as soon as it lands:
	// waiting on a rendering would hold back the whole of what it says.
	if x.MsgType == "interactive" {
		if c, ok := card.Parse(x.ContentRaw); ok {
			return cardRows(c, x, idx, st, g, ms)
		}
	}
	// A post likewise says everything about itself, and says it better than
	// its rendering does: that rendering flattens the paragraphs it was
	// written in to markdown, which cannot tell an asterisk somebody typed
	// from one it added, and drops the empty paragraphs that are blank lines.
	if x.MsgType == "post" {
		if b, ok := postBodyOf(x.ContentRaw); ok {
			return postRows(b, x, idx, st, g, ms)
		}
	}
	// A sticker's rendering is the words "[Sticker]", which name the picture
	// nowhere, and an image's only spells its key back out: both are in the
	// body, so the body is what draws them.
	if key := pictureKey(x); key != "" {
		return pictureRows(key, x, idx, st, g)
	}
	if x.RenderedAt == 0 {
		return dimRows(pendingText(x.MsgType, x.ContentRaw, x.MentionsJSON), idx, st, g, spellOf(x.MsgType))
	}
	// A post is markdown by construction, so it is drawn as the document it
	// is. A text message is not: someone typing "3 * 4 * 5" means the
	// asterisks, so that one keeps the literal path below.
	if x.MsgType == "post" {
		return mdRows(x.Content, x, idx, st, g, ms)
	}

	var rows []msgRow
	content := strings.ReplaceAll(strings.ReplaceAll(x.Content, "\r", ""), "\t", "    ")
	for line := range strings.SplitSeq(content, "\n") {
		keys, rest := splitImages(line)
		if len(keys) == 0 || strings.TrimSpace(rest) != "" {
			if segs := inlineSegs(rest, ms, st.emojiInline, st.docLabel); segs != nil {
				rows = append(rows, segRows(segs, "", idx, st, g)...)
			} else {
				rows = append(rows, textRows(wrap(renderInline(rest, ms), st.inner()), idx, g)...)
			}
		}
		for _, key := range keys {
			rows = append(rows, pictureRows(key, x, idx, st, g)...)
		}
	}
	return rows
}

// dimRows draw a body larkim holds no rendering for, its words dim. An
// official emoji among them is drawn as the picture the client draws, the way
// a rendered body draws one: these rows are what a merged forward's children
// keep for good, since lark-cli's expansion answers with raw bodies and
// renders none of them.
func dimRows(body string, idx int, st msgStyle, g *leads, sp emojiSpell) []msgRow {
	var rows []msgRow
	for line := range strings.SplitSeq(body, "\n") {
		if segs := emojiSegs(line, sp, st.emojiInline, func(t string) string { return stDim.Render(t) }); segs != nil {
			rows = append(rows, segRows(segs, "", idx, st, g)...)
			continue
		}
		rows = append(rows, textRows(wrap(stDim.Render(line), st.inner()), idx, g)...)
	}
	return rows
}

// textRows lays lines already fitted to the body out one row each, taking the
// block's leads as they go.
func textRows(lines []string, idx int, g *leads) []msgRow {
	out := make([]msgRow, 0, len(lines))
	for _, l := range lines {
		out = append(out, msgRow{lead: g.take(), text: l, idx: idx})
	}
	return out
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

// pictureKey is the picture a message is rather than one it carries: an
// image's key, or a sticker's own file, which the two bodies spell with
// different field names. Empty for every other message.
func pictureKey(x store.Message) string {
	if x.MsgType != "image" && x.MsgType != "sticker" {
		return ""
	}
	var body struct {
		ImageKey string `json:"image_key"`
		FileKey  string `json:"file_key"`
	}
	if json.Unmarshal([]byte(x.ContentRaw), &body) != nil {
		return ""
	}
	if x.MsgType == "sticker" {
		return body.FileKey
	}
	return body.ImageKey
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
			names = append(names, "You")
		case st.people[id] != "":
			names = append(names, personName(st.people[id], st.suffix[id]))
		}
	}
	rest := max(0, c.Count-len(names))
	who := strings.Join(names, ", ")
	if rest > 0 {
		who = strings.TrimPrefix(who+" +"+strconv.Itoa(rest), " ")
	}
	return who
}

// reactionChip is one emoji's standing on a message, drawn as the chip the
// client puts it on: the emoji and who put it there share one tint, closed by
// a round cap either side. The emoji is the client's own picture — the client
// draws its emoji as pictures and never as characters — and the client's name
// for it only where this terminal draws no pictures at all.
//
// The strip wraps between chips but never cuts inside one, so a chip crowded
// with names is fitted here rather than left to run past the pane.
func reactionChip(c emoji.Chip, st msgStyle) []rowSeg {
	e, known := emoji.ByKey(c.Key)
	label := "[" + c.Key + "]"
	var pic picture
	if known {
		if pic = st.emojiChip(e.Key); pic.cols > 0 {
			label = ""
		} else {
			label = "[" + e.Name() + "]"
		}
	}
	// Nothing inside a chip is spaced off anything: a cap's flat side is the
	// cell edge it hands over on, an emoji carries its own side bearing, and a
	// picture is drawn at its own shape inside the cells it rounded to.
	// The rule is what parts the emoji from the names, and it only has to be
	// read as a break, not as a gap.
	label = truncate(label, st.inner()-chipPad)
	who := truncate(reactors(c, st), st.inner()-chipPad-pic.cols-lipgloss.Width(label)-lipgloss.Width(chipRule))
	// The rule exists only to part the emoji from the names, so a chip too
	// narrow to name anybody draws neither.
	tail := ""
	if who != "" {
		tail = stChipRule.Render(chipRule) + stChipDim.Render(who)
	}
	if pic.cols > 0 {
		return []rowSeg{{text: stChipEdge.Render(chipLeft)}, {pic: pic},
			{text: tail + stChipEdge.Render(chipRight)}}
	}
	// A chip of characters alone stays one piece, so a strip made of them is
	// ordinary text that a selected row can still tint.
	return []rowSeg{{text: stChipEdge.Render(chipLeft) +
		stChip.Render(label) + tail + stChipEdge.Render(chipRight)}}
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
// Feishu client puts them.
//
// The chips are packed by hand rather than wrapped: a picture stands in the
// text as placeholder cells the terminal fills, and a wrap that measured them
// as the characters they are would break one apart.
func reactionRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
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
		row.addZones(zones)
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
	rows = textRows(cardHead(c, st.inner()), idx, g)
	client := applink.ChatLink(x.ChatID, x.MessageID, x.MessagePosition)
	prevText, prevPanel := false, false
	for _, b := range c.Blocks {
		parts := []card.Block{b}
		var line []rowSeg
		var pills []clickZone
		abreast := false
		if len(b.Row) > 0 {
			if line, pills, abreast = layCardRow(b.Row, x, idx, st, ms, client); !abreast {
				parts = b.Stacked()
			}
		}
		if len(parts) == 0 {
			continue
		}
		top, bottom := parts[0].Markdown != "", parts[len(parts)-1].Markdown != ""
		if abreast {
			top = slices.ContainsFunc(b.Row, func(c card.Cell) bool { return c.Markdown != "" })
			bottom = top
		}
		panel := b.Background != ""
		// Text beside text is two paragraphs, which a blank line parts, the
		// way it parts them in the document the card copies out as. Inside
		// one panel the line between them is the panel's too.
		if prevText && top {
			rows = append(rows, msgRow{lead: g.take(), idx: idx, panel: panel && prevPanel})
		}
		prevText, prevPanel = bottom, panel
		from := len(rows)
		if abreast {
			row := msgRow{lead: g.take(), idx: idx, tinted: true, segs: line}
			row.placeZones()
			row.addZones(pills)
			rows = append(rows, row)
		} else {
			bold := ms
			if b.Bold {
				bold = ms.styled(ms.base.Bold(true))
			}
			for _, part := range parts {
				rows = append(rows, cardBlockRows(part, x, idx, st, g, bold, client)...)
			}
		}
		for j := from; j < len(rows); j++ {
			rows[j].align(b.Align, st.inner())
			rows[j].panel = panel
		}
	}
	return rows
}

// align moves a row's content to where the card sets it in w columns:
// centred, or against the right edge. Left is where it already stands.
func (r *msgRow) align(how string, w int) {
	if how != "center" && how != "right" || r.pic.cols > 0 {
		return
	}
	used := segsWidth(r.segs)
	if len(r.segs) == 0 {
		r.text, used = trimPad(r.text)
	}
	pad := w - used
	if how == "center" {
		pad /= 2
	}
	if pad <= 0 {
		return
	}
	if len(r.segs) == 0 {
		r.text = strings.Repeat(" ", pad) + r.text
		return
	}
	r.segs = append([]rowSeg{{text: strings.Repeat(" ", pad)}}, r.segs...)
	r.placeZones()
}

// cardBlockRows draw one block of a card body.
func cardBlockRows(b card.Block, x store.Message, idx int, st msgStyle, g *leads, ms mentions, client string) []msgRow {
	switch {
	case b.ImageKey != "":
		return pictureRows(b.ImageKey, x, idx, st, g)
	case len(b.Buttons) > 0:
		var rows []msgRow
		for _, l := range cardButtons(b.Buttons, st.inner(), client) {
			row := msgRow{lead: g.take(), text: l.text, idx: idx}
			row.addZones(l.zones)
			rows = append(rows, row)
		}
		return rows
	}
	return mdRows(b.Markdown, x, idx, st, g, ms)
}

// cardCellGap is the columns between two cells of a row: the gap the client
// leaves between columns, at the width a terminal can draw it.
const cardCellGap = 1

// layCardRow lays a column_set out on the one line the client draws it on:
// an auto column as wide as it needs, the columns weighted between them
// sharing what is left by weight, the way the client's flex layout shares
// it. It reports false when that line cannot hold them — a cell that runs to
// a second line, or a share too narrow for what it holds — and the row is
// then drawn stacked. The pills' targets come back measured from where the
// row's content starts.
func layCardRow(row []card.Cell, x store.Message, idx int, st msgStyle, ms mentions, client string) ([]rowSeg, []clickZone, bool) {
	type laid struct {
		segs  []rowSeg
		zones []clickZone
		cols  int
	}
	cells := make([]laid, len(row))
	room := st.inner() - cardCellGap*(len(row)-1)
	autos, weights := 0, 0
	for i, c := range row {
		var l laid
		switch {
		case c.Markdown != "":
			// Drawn against leads of its own: what this measures is not yet
			// on screen, and the message's leads go to the rows that are.
			rs := mdRows(c.Markdown, x, idx, st, &leads{b: &block{}}, ms)
			if len(rs) != 1 || rs[0].pic.cols > 0 {
				return nil, nil, false
			}
			if l.segs = rs[0].segs; len(l.segs) == 0 {
				text, _ := trimPad(rs[0].text)
				l.segs = []rowSeg{{text: text}}
			}
			l.cols = segsWidth(l.segs)
		case len(c.Buttons) > 0:
			// A fill button is as wide as its column, which is not known until
			// the shares are handed out: its label is what the column needs.
			bs := slices.Clone(c.Buttons)
			for j := range bs {
				bs[j].Fill = false
			}
			lines := cardButtons(bs, st.inner(), client)
			if len(lines) != 1 {
				return nil, nil, false
			}
			l.segs, l.zones, l.cols = []rowSeg{{text: lines[0].text}}, lines[0].zones, lipgloss.Width(lines[0].text)
		}
		if c.Weight > 0 {
			weights += c.Weight
		} else {
			autos += l.cols
		}
		cells[i] = l
	}
	share := room - autos
	if share < 0 {
		return nil, nil, false
	}
	var line []rowSeg
	var zones []clickZone
	at, handed, given := 0, 0, 0
	for i, c := range row {
		l := cells[i]
		width := l.cols
		if c.Weight > 0 {
			// Each takes its share of the whole, and the last what rounding
			// left, so the row ends at the edge of the pane.
			if handed += c.Weight; handed == weights {
				width = share - given
			} else {
				width = share * c.Weight / weights
			}
			given += width
			if l.cols > width {
				return nil, nil, false
			}
		}
		if len(c.Buttons) > 0 && c.Buttons[0].Fill {
			pill := cardButtons(c.Buttons, width, client)[0]
			l.segs, l.zones, l.cols = []rowSeg{{text: pill.text}}, pill.zones, width
		}
		if i > 0 {
			line = append(line, rowSeg{text: strings.Repeat(" ", cardCellGap)})
			at += cardCellGap
		}
		line = append(line, l.segs...)
		if pad := width - l.cols; pad > 0 {
			line = append(line, rowSeg{text: strings.Repeat(" ", pad)})
		}
		for _, z := range l.zones {
			z.x0, z.x1 = z.x0+at, z.x1+at
			zones = append(zones, z)
		}
		at += width
	}
	return line, zones, true
}

// pictureRows reserve the cells a downloaded image will occupy, behind the
// lead. A picture this terminal cannot draw — no graphics protocol, not
// downloaded yet, a format the decoder will not read — falls back to a
// one-line stand-in.
func pictureRows(key string, x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	boxCols, boxRows := st.picBox()
	label := "[Image]"
	if x.MsgType == "sticker" {
		boxCols, boxRows, label = min(st.inner(), stickerCols), st.height, "[Sticker]"
	}
	pic := placePicture(key, x, st, boxCols, boxRows)
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
	label := "image"
	if n := len(rest) + 1; n > 1 {
		label = strconv.Itoa(n) + " images"
	}
	return clickZone{urls: append([]string{pressed}, rest...), label: label, note: "opening " + label}, true
}

// placePicture sizes one of a message's downloaded pictures for the pane. A
// zero size means there is nothing to draw: no graphics protocol, a key the
// message does not carry, a download still on its way, or a format the
// decoder will not read.
func placePicture(key string, x store.Message, st msgStyle, cols, rows int) picture {
	if st.place == nil || key == "" {
		return picture{}
	}
	r := attachRes(key, x, st)
	if r.Status != "done" {
		return picture{}
	}
	return st.place(r.LocalPath, cols, rows)
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

var weekdayNames = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

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
		return "Today"
	case days == 1:
		return "Yesterday"
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
