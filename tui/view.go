package tui

import (
	"cmp"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
)

const (
	chatsWidth       = 38 // avatar + two lines of text, per docs/chats-list/PRD.md
	threadWidth      = 44
	minMessagesWidth = 40 // narrower than this, the right pane takes the messages pane's place
	minWidth         = chatsWidth + minMessagesWidth
	// minHeight is the shortest terminal the layout still fits in: the status
	// bar, the composer box at rest, and a messages pane holding its head and
	// one row of message. Derived rather than written down, because the panes
	// have a floor of their own and a box that claims one more row than the
	// screen can spare overflows past the bottom edge.
	minHeight   = statusHeight + restingComposer + 2 + msgHeaderHeight + 1 + 2 // box border + pane border
	inputHeight = 3
	// restingComposer is the composer's inner height with nothing claimed
	// beyond the writing area, the quote row and the badge row around it. Every
	// mode draws the box at least this tall, and so does either box of a split
	// band, so neither switching mode nor handing the keys across moves the
	// panes.
	restingComposer = inputHeight + 2
	statusHeight    = 1
	headerHeight    = 1 // title row of every list pane
	// msgHeaderHeight is what the messages pane spends on its own head: the
	// title row plus the rule under it. The chat's name is bold and so is a
	// sender line, so without the rule the first message reads as part of the
	// header.
	msgHeaderHeight = headerHeight + 1

	// chipLeft and chipRight are the powerline half-circles that round a chip
	// off, drawn from the Nerd Font this terminal maps U+E0B0-U+E0C8 to.
	chipLeft  = "\ue0b6"
	chipRight = "\ue0b4"
	// chipRule parts a reaction's emoji from the names of who put it there.
	// Dots rather than a box-drawing rule: kitty draws the box-drawing glyphs
	// itself, edge to edge of the cell, and a stroke that tall beside one line
	// of text reads as a border around the names instead of a break before
	// them. This one is a font glyph, so it keeps to the text's own height.
	chipRule = "\u22ee"
	// chipPad is what a chip costs beside what it holds: a cap either side.
	chipPad = 2
)

// Foreground colours are ANSI palette indices, so they follow the terminal
// theme; shades that need a background come from theme. The fixed hex tints
// below are the exceptions: each one is a colour the client owns, so it reads
// the same whatever a terminal theme happens to paint.
var (
	colAccent = lipgloss.Color("4")
	colDim    = lipgloss.Color("8")
	colErr    = lipgloss.Color("1")
	// colBullet is the client's own tint for the marker of an unordered list
	// item, the one part of a list it colours.
	colBullet = lipgloss.Color("#1c70f0")
	// colWarn is what the badge paints a lint finding: the body still sends,
	// so it cannot wear the colour a refused path does.
	colWarn = lipgloss.Color("3")
	// colChatSel and colChatSelText are the client's own tint for the chat it
	// is on, kept off the shade ladder so the list reads the same wherever it
	// is opened. The text colour rides along because the tint is light on
	// every terminal: it is re-asserted per run, so a run that names its own
	// colour keeps it.
	colChatSel     = lipgloss.Color("#e7eefc")
	colChatSelText = lipgloss.Color("#1f2329")

	stDim    = lipgloss.NewStyle().Foreground(colDim)
	stBullet = lipgloss.NewStyle().Foreground(colBullet)
	stAccent = lipgloss.NewStyle().Foreground(colAccent)
	stBold   = lipgloss.NewStyle().Bold(true)
	stErr    = lipgloss.NewStyle().Foreground(colErr)
	stWarn   = lipgloss.NewStyle().Foreground(colWarn)
	// stConfirm is a decision waiting on y/n: not an error, not plain info.
	stConfirm = lipgloss.NewStyle().Bold(true).Foreground(colWarn)
	// The reader's own mention wears the filled badge the client paints it as:
	// the brand blue it owns, closed by the same caps a chip is, and white on
	// it. The fill is fixed rather than the terminal's blue because a badge
	// carries its own background, and one shaded from the theme would land as a
	// different signal on every palette.
	colMentionMe    = lipgloss.Color("#3370ff")
	stMentionMe     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(colMentionMe)
	stMentionMeEdge = lipgloss.NewStyle().Foreground(colMentionMe)
	// The draft wears what the client paints a conversation preview's draft
	// with: red on a dotted underline, so the pencil reads as the reader's own
	// words still to send rather than another mark the chat collected. Dots,
	// not a straight line, keep it from reading as a filter hit on a name.
	colDraft = lipgloss.Color("#f54a45")
	stDraft  = lipgloss.NewStyle().Foreground(colDraft).Underline(true).
			UnderlineStyle(lipgloss.UnderlineDotted).UnderlineColor(colDraft)
	// stUnread is the terminal-palette stand-in for badgeRed, the disc stamped
	// onto a picture: the header's count and the digits a row falls back to when
	// no disc could be drawn both take it, so the three read as one signal.
	stUnread = lipgloss.NewStyle().Foreground(colErr).Bold(true)
	// stChatSel paints the chat row under the cursor while the chats pane has
	// focus; without focus the row falls back to the shaded selection.
	stChatSel = lipgloss.NewStyle().Foreground(colChatSelText).Background(colChatSel)
	// stChatSelBG is that tint without the text colour, for the avatar column:
	// a picture's cells carry their image id in the foreground.
	stChatSelBG = lipgloss.NewStyle().Background(colChatSel)

	// A reaction wears the chip the client draws it on: a tint closed by a
	// round cap either side. The tint is fixed, like the selected chat's, both
	// because the emoji art is drawn for a light backing and because the caps
	// have to be painted in the very colour they close.
	colChip     = lipgloss.Color("#bbcef6")
	stChip      = lipgloss.NewStyle().Foreground(colChatSelText).Background(colChip)
	stChipCells = lipgloss.NewStyle().Background(colChip)
	stChipEdge  = lipgloss.NewStyle().Foreground(colChip)
	// Who reacted is secondary to what they reacted with, and the rule parting
	// them from the emoji carries less than either, so each step away from the
	// label is a step lighter over the chip's backing. Neither uses SGR faint:
	// the backing is a fixed light tint and a terminal dims by darkening its
	// foreground, which over this blue reads as more weight rather than less.
	colChipDim  = lipgloss.Color("#8f959e")
	colChipRule = lipgloss.Color("#a5b1ca")
	stChipDim   = lipgloss.NewStyle().Foreground(colChipDim).Background(colChip)
	stChipRule  = lipgloss.NewStyle().Foreground(colChipRule).Background(colChip)

	// A card's button is a filled rectangle darker than the chip: a block of
	// colour with its label on it, not bracketed text, and the padding sits
	// inside the fill so the label needs none of its own.
	colBtn = lipgloss.Color("#9db4e0")
	stBtn  = lipgloss.NewStyle().Foreground(colChatSelText).Background(colBtn).Padding(0, 1)

	// stFaint is the tier under stDim, for the badges beside a sender name
	// that stDim already draws: two things in the same grey read as equals.
	// It names no colour of its own — faint darkens whatever foreground is
	// current, and over colDim's grey there is nothing left to darken.
	stFaint = lipgloss.NewStyle().Faint(true)

	// A chat outside this tenant wears the colour the client marks one with.
	// It is not stErr: being external is a fact about the chat, not a fault,
	// and the red was only ever the nearest thing the palette had.
	colExternal = lipgloss.Color("#ff8800")
	stExternal  = lipgloss.NewStyle().Foreground(colExternal)

	// sgrReset is the sequence that ends a styled run; lipgloss writes the
	// short spelling, a hand-written line may carry the long one.
	sgrReset = regexp.MustCompile("\x1b\\[0?m")
)

// theme holds the styles shaded from the terminal background, so the
// selected row (sel also paints the status bar) stays readable on light and
// dark palettes alike.
type theme struct {
	sel, selInact lipgloss.Style
	// panel is the shade a card lays under a column_set given a background:
	// fainter than a selection, so a panel never reads as one.
	panel lipgloss.Style
}

func themeFor(bg color.Color, dark bool) theme {
	shade := func(onLight, onDark float64) color.Color {
		if dark {
			return lipgloss.Lighten(bg, onDark)
		}
		return lipgloss.Darken(bg, onLight)
	}
	return theme{
		sel:      lipgloss.NewStyle().Background(shade(0.12, 0.18)),
		selInact: lipgloss.NewStyle().Background(shade(0.06, 0.09)),
		panel:    lipgloss.NewStyle().Background(shade(0.035, 0.05)),
	}
}

// composerStyles keeps bubbles' defaults but drops the cursor-line shade (the
// focused border already marks the composer) and lets the placeholder use the
// palette's muted colour.
func composerStyles(dark bool) textarea.Styles {
	st := textarea.DefaultStyles(dark)
	st.Focused.CursorLine = lipgloss.NewStyle()
	st.Focused.Placeholder = stDim
	st.Blurred.Placeholder = stDim
	return st
}

// newQueryInput is the one-line field every chooser and filter is built on —
// the emoji picker, the forward chooser, the help panel, the config panel's
// query and the value it edits — so they all take the same readline keys and
// wear the same colours.
func (m Model) newQueryInput() textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.SetStyles(textinput.DefaultStyles(m.dark))
	// The terminal's own cursor carries the mode, so bubbles must stop drawing
	// its reverse-video stand-in: a virtual cursor has no shape to change.
	in.SetVirtualCursor(false)
	return in
}

func (m *Model) layout() {
	// A box that is not on screen cannot be the one being written in, so the
	// keys are sent back before anything is measured against the side.
	m.holdSide()
	m.input.SetWidth(max(10, m.bandWidth(sideMain)-2))
	m.rightInput.SetWidth(max(10, m.bandWidth(sideRight)-2))
	if m.aiP != nil {
		m.aiP.input.SetWidth(max(10, m.bandWidth(sideAI)-2))
	}
	// A prompt stands where a quote would, so it is there only while the box
	// is answering its frame rather than a message in it. A Thread is answered
	// without naming anything; a reply tree's answer lands in the chat's flow,
	// so the prompt says whose message it will land beside.
	m.rightInput.Placeholder = ""
	if m.rightReply == nil {
		switch m.rightKind {
		case rightThread:
			m.rightInput.Placeholder = threadPrompt
		case rightReply:
			if x, ok := m.frameRoot(); ok {
				m.rightInput.Placeholder = replyPrompt + displaySender(x, m.deps.Self, m.suffixOf(x.SenderID))
			}
		}
	}
	rows := m.composerRows()
	m.sized(sideMain, rows)
	m.sized(sideRight, rows)
	m.sized(sideAI, rows)
	m.cmdline.SetWidth(max(10, m.bandWidth(m.cmdSide())-4))
	m.rebuildPreview()
	// A resize rewraps every row, so each pane is held by the message on its
	// top row rather than scrolled back to its cursor: a resize is not a
	// cursor move, and layout also runs on a paste and on the editor's return.
	msgA := topAnchor(m.msgRows, m.msgs, m.msgTop)
	msgTail := atTail(m.msgRows, m.msgTop, m.msgListHeight())
	thrA := topAnchor(m.threadRows, m.thread, m.threadTop)
	thrTail := atTail(m.threadRows, m.threadTop, m.listHeight())
	m.rebuildMessages()
	m.rebuildThread()
	m.clampChat()
	m.msgTop = holdTop(m.msgRows, m.msgs, msgA, msgTail, m.msgTop, m.msgListHeight())
	m.threadTop = holdTop(m.threadRows, m.thread, thrA, thrTail, m.threadTop, m.listHeight())
	if m.foldRight() && m.focus == paneMessages {
		m.focus = paneThread
	}
}

// sized gives a box's writing area its share of the band.
func (m *Model) sized(s composerSide, rows composerRows) {
	var ta *textarea.Model
	switch s {
	case sideRight:
		ta = &m.rightInput
	case sideAI:
		if m.aiP == nil {
			return
		}
		ta = &m.aiP.input
	default:
		ta = &m.input
	}
	ta.SetHeight(m.textHeight(s, rows))
}

// textHeight is the writing area's height inside the box for s. The box with
// the keys keeps the rows claimed for it; the other one spends the preview's,
// the popup's and the badge's rows on text as well, since all three belong to
// the draft being typed. Either way the quote row the band claims in every
// state goes to the text when there is no quote to draw in it, rather than
// being left blank under the badge, which is where fitBlock would pad it.
func (m Model) textHeight(s composerSide, rows composerRows) int {
	h := rows.input + rows.quote
	if s != m.side {
		h = rows.total()
	}
	if _, ok := m.quotedOn(s); ok {
		h -= rows.quote
	}
	return max(1, h)
}

// bodyHeight is the inner height of the message panes, the ones the composer
// box stands under.
func (m Model) bodyHeight() int {
	return max(1, m.height-m.composerHeight()-2-statusHeight-2) // input border + pane border
}

// chatsBodyHeight is the inner height of the chats pane, which runs past the
// composer to the status bar the way the client's chat list does. It is the
// body plus the box below it, so it holds still while a growing draft shortens
// the panes beside it.
func (m Model) chatsBodyHeight() int {
	return m.bodyHeight() + m.composerHeight() + 2 // input border
}

// listHeight is the number of rows a list pane shows below its title.
func (m Model) listHeight() int { return max(1, m.bodyHeight()-headerHeight) }

// msgListHeight is listHeight for the messages pane, which spends a second row
// on the rule under its title.
func (m Model) msgListHeight() int { return max(1, m.bodyHeight()-msgHeaderHeight) }

// aiListHeight is the assistant panel's viewport: the pane's title, the
// context strip and the rule under it are three rows the turns never get.
func (m Model) aiListHeight() int { return max(1, m.bodyHeight()-3) }

// picHeight is the tallest a picture may be. It is the list height with the
// composer at its shortest rather than the current one: sizing to the live
// pane would re-encode and re-send every picture on screen each time the reply
// bar opens, for one row of difference.
func (m Model) picHeight() int {
	return max(1, m.height-restingComposer-statusHeight-msgHeaderHeight-4) // input border + pane border
}

// chatRowsHeight is listHeight for the chats pane, which has a column of its
// own and so goes on past the composer box beside it.
func (m Model) chatRowsHeight() int { return max(1, m.chatsBodyHeight()-headerHeight) }

// chatListHeight is how many whole chats the chat pane shows; a chat is never
// drawn with only one of its two lines.
func (m Model) chatListHeight() int { return max(1, chatsThatFit(m.chatRowsHeight())) }

// foldRight reports whether the terminal is too narrow for three columns, in
// which case the thread or assistant pane replaces the messages pane.
func (m Model) foldRight() bool {
	return m.rightOpen() && m.width < chatsWidth+minMessagesWidth+threadWidth
}

func (m Model) rightWidth() int {
	if m.foldRight() {
		return m.width - chatsWidth
	}
	return threadWidth
}

func (m Model) messagesWidth() int {
	w := m.width - chatsWidth
	if m.rightOpen() && !m.foldRight() {
		w -= threadWidth
	}
	return max(20, w)
}

// msgStyleFor is the render context of one message pane: its width and the
// details the store holds.
func (m Model) msgStyleFor(width int, meta msgMeta) msgStyle {
	st := meta.style()
	st.width, st.height, st.self, st.selfName, st.now = width, m.picHeight(), m.deps.Self, m.selfName, time.Now()
	st.dataDir, st.dots, st.dark = m.deps.DataDir, m.dots, m.dark
	st.outbox, st.reacts = m.outboxStates(), m.reactStates()
	// The Unread panel runs across chats, so the chat the cursor happens to
	// sit in says nothing about the messages above and below it.
	if c, ok := m.currentChat(); ok && m.feed == nil {
		st.p2p = c.ChatMode == "p2p"
		if st.p2p {
			st.peer = c.P2PTargetID
		}
	}
	if m.replyTo != nil {
		st.quoted = m.replyTo.MessageID
	}
	if m.pics != nil {
		st.place, st.disc = m.pics.place, m.pics.disc
	}
	st.candidates = m.candidatesForStyle()
	return st
}

// rebuildPreview draws the draft as the message list will draw it, through
// the same rows a sent message takes, so what is previewed and what is sent
// cannot describe different messages.
func (m *Model) rebuildPreview() {
	m.previewRows = nil
	if m.previewOpen && m.mode == modeInsert && m.draft.kind != kindText {
		it := outboxItem{localID: "preview", chatID: m.chatID, msgType: m.draft.kind.msgType(),
			body: m.draft.body, images: m.draft.uploads()}
		meta := msgMeta{suffix: m.meta.suffix, people: m.meta.people, avatars: m.meta.avatars, docs: m.meta.docs}
		resPending(&meta, []outboxItem{it})
		st := m.msgStyleFor(m.width-2, meta)
		// The body alone, not renderRows: a sender line, a day rule and a time
		// belong to a message that exists, and none of them would tell the
		// reader anything the composer above does not already say.
		var b block
		g := leads{b: &b}
		m.previewRows = bodyRows(it.message(m.deps.Self, m.selfName), 0, st, &g)
	}
	// Clamped rather than reset: typing rebuilds the preview on every
	// keystroke, and a reader who scrolled to the tail of a long post is still
	// writing into it. A preview that closed has no rows, so this zeroes it.
	m.previewTop = clamp(m.previewTop, 0, m.previewBottom())
}

func (m *Model) rebuildMessages() {
	w := m.messagesWidth() - 2
	// The search panel draws over whatever the pane held, the Unread panel
	// included, so it answers before the panel does.
	if m.searching {
		lead := "Messages"
		if m.mentions {
			lead = mentionsLabel
		}
		st := m.msgStyleFor(w, m.searchMeta)
		// The panel's own query, split the way the store split it to match:
		// what is marked is then what was searched for.
		st.hits = strings.Fields(m.searchQuery)
		m.msgRows = renderSearchRows(m.searchHits, m.chats, lead, st)
		return
	}
	if m.feed != nil {
		m.msgRows = renderFeedRows(m.msgs, m.feed, m.chats, m.msgStyleFor(w, m.meta))
		return
	}
	m.msgRows = renderRows(m.msgs, m.msgStyleFor(w, m.meta))
	// The head of the page says why scrolling stops here, which no amount of
	// blank space above the first message would.
	if len(m.msgRows) > 0 && m.atLocalFloor() {
		m.msgRows = append([]msgRow{m.floorRow(w)}, m.msgRows...)
	}
}

// renderSearchRows lays the panel out: the message hits as message blocks,
// then the chats and the people, each group under a rule of its own. Row
// indices point at hits rather than messages, so one cursor walks all three.
func renderSearchRows(hits []searchHit, chats []store.Chat, lead string, st msgStyle) []msgRow {
	// Hits run across chats, so every block names its sender, and no @ in one
	// is measured against the chat the cursor happens to sit on.
	st.p2p, st.peer = false, ""
	st.names = make(map[string]string, len(chats))
	for _, c := range chats {
		st.names[c.ChatID] = c.Name
	}
	// Message hits lead, the store's before Feishu's, so a block's index into
	// either run is its index into the hits once the run's own start is added.
	var local, remote []store.Message
	for _, h := range hits {
		if h.kind != hitMessage {
			break
		}
		if h.remote {
			remote = append(remote, h.msg)
			continue
		}
		local = append(local, h.msg)
	}
	var rows []msgRow
	if len(local) > 0 {
		rows = append(rows, searchRule(lead, st.width))
	}
	rows = append(rows, renderRows(local, st)...)
	if len(remote) > 0 {
		rows = append(rows, searchRule("Feishu", st.width))
		for _, r := range renderRows(remote, st) {
			r.idx += len(local)
			rows = append(rows, r)
		}
	}
	msgs := len(local) + len(remote)
	// Nothing past the message hits is a message, so the first row here
	// always opens a group of its own.
	kind := hitMessage
	for i := msgs; i < len(hits); i++ {
		h := hits[i]
		if h.kind != kind {
			kind = h.kind
			rows = append(rows, searchRule(groupLabel(kind), st.width))
		}
		rows = append(rows, msgRow{text: fit(searchRowText(h), st.width), idx: i})
	}
	return rows
}

func groupLabel(k searchKind) string {
	if k == hitPerson {
		return "People"
	}
	return "Chats"
}

// searchRule parts one group from the next. It belongs to no hit, so the
// selection never paints it.
//
// The label is measured in columns, not bytes: a chat name is as often CJK as
// ASCII, and counting its bytes would leave the rule short of the edge by two
// columns for every character in it.
func searchRule(label string, w int) msgRow {
	dashes := strings.Repeat("─", max(0, w-lipgloss.Width(label)-4))
	return msgRow{text: fit(stDim.Render("── "+label+" "+dashes), w), plain: true, rule: true}
}

// searchRowText is the one line a chat or a person takes: the name with the
// runes the query landed on underlined, and what tells two of them apart.
func searchRowText(h searchHit) string {
	if h.kind == hitChat {
		return markName(flatten(h.chat.Name), h.mark, stBold)
	}
	tail := cmp.Or(h.user.Department, h.user.Email)
	line := markName(h.user.Name, h.mark, stBold)
	if tail != "" {
		line += stDim.Render(" · " + tail)
	}
	return line
}

func (m *Model) rebuildThread() {
	if !m.threadOpen() {
		m.threadRows = nil
		return
	}
	st := m.msgStyleFor(m.rightWidth()-2, m.threadMeta)
	// Inside a frame every nested bundle belongs to the tree the frame came
	// from, not to itself, which is where its rows and its pictures are kept.
	st.forwardRoot, st.inFrame = m.rightRoot, true
	// The box under the column marks what it answers. A quote taken into the
	// chat's box keeps its own mark, since that message is in this list too.
	if x, ok := m.quotedOn(sideRight); ok {
		st.quoted = x.MessageID
	}
	m.threadRows = renderRows(m.thread, st)
}

// paneLine is one row as a pane draws it: under the selection when it is in
// one, else over the panel its card lays under it. The selection wins because
// it re-asserts its shade only after each reset, and a panel's own shade
// standing inside the line would hold until the next one.
func (m Model) paneLine(r msgRow, w int, selected, focused bool) string {
	line, tint := m.rowLine(r, w)
	switch {
	case selected && tint && !r.plain:
		return m.highlight(line, focused)
	case r.panel:
		// The lead stays clear: the panel is the card's, and the disc and
		// the marker beside it belong to the message.
		lead := m.leadCells(r.lead)
		return lead + paint(m.th.panel, strings.TrimPrefix(line, lead))
	}
	return line
}

// rowLine is the drawn form of one row, and whether a selection may tint it.
// A row that is one whole picture takes the tint like any other: a terminal
// paints the cell background under a placement, so it reaches the lead column
// and whatever of the pane the picture leaves clear. A row of pieces says for
// itself — body text takes the tint under its emoji, a chip brings its own.
func (m Model) rowLine(r msgRow, w int) (string, bool) {
	lead := m.leadCells(r.lead)
	w -= r.lead.cols()
	if len(r.segs) > 0 {
		return lead + m.joinSegs(r.segs, w), r.tinted
	}
	if r.pic.cols == 0 {
		return lead + fit(r.text, w), true
	}
	cells := m.pics.cells(r.pic, r.picRow)
	if cells == "" {
		return lead + fit(r.text, w), true // still on its way to the terminal
	}
	// Padded rather than fitted: fit measures a placeholder as the characters
	// it is and would cut one out of its cluster. What text a picture row
	// carries is the indent its block charged it, which opens the row.
	indent := lipgloss.Width(r.text)
	return lead + r.text + cells + strings.Repeat(" ", max(0, w-r.pic.cols-indent)), true
}

// leadCells draws the columns a row opens with. A disc is a picture the
// terminal fills in, so it is placed the way any other one is; the block
// standing in for it is ordinary text. A disc narrower than the column — a
// file too small to fill it — is padded rather than stretched, so every body
// line still starts at the same column.
func (m Model) leadCells(l lead) string {
	if l.pic.cols == 0 {
		return l.box + l.mark
	}
	cells := m.pics.cells(l.pic, l.picRow)
	if cells == "" {
		cells = l.pic.gap() // still on its way to the terminal
	}
	return cells + strings.Repeat(" ", avatarWidth-l.pic.cols) + l.mark
}

// segCells draws one segment's picture, holding the cells it will fill while
// the image is still on its way to the terminal.
func (m Model) segCells(pic picture) string {
	if cells := m.pics.cells(pic, 0); cells != "" {
		return cells
	}
	return pic.gap()
}

// joinSegs draws a line whose pictures sit inside its text, piece by piece
// against what the row has left. The pieces were packed to that width when
// they were built, so this normally has nothing to cut; it cuts anyway because
// a pane is drawn as wide as its widest row, and one row past its share pushes
// the whole frame off the terminal, where every line wraps. Whole rows are
// what the reader loses then, against the one column lost here.
func (m Model) joinSegs(segs []rowSeg, w int) string {
	var b strings.Builder
	used := 0
	for _, s := range segs {
		if s.pic.cols > 0 {
			// A picture goes in whole: its cells name one image to the
			// terminal, and half of them name nothing.
			if used+s.pic.cols > w {
				break
			}
			b.WriteString(m.segCells(s.pic))
			used += s.pic.cols
			continue
		}
		text := cut(s.text, w-used)
		used += lipgloss.Width(text)
		// The link goes on after the cut, so the closing sequence is there
		// whatever the cut took.
		if len(s.urls) > 0 {
			text = hyperlink(s.urls[0], text)
		}
		b.WriteString(text)
	}
	return b.String() + strings.Repeat(" ", max(0, w-used))
}

// firstRow is where a message's block starts — its day separator when it
// opens a day, so scrolling to it brings that label along.
func firstRow(rows []msgRow, idx int) int {
	for i, r := range rows {
		if r.idx == idx {
			return i
		}
	}
	return 0
}

func lastRow(rows []msgRow, idx int) int {
	last := 0
	for i, r := range rows {
		if r.idx == idx {
			last = i
		}
	}
	return last
}

func rowAt(rows []msgRow, line int) int {
	if line < 0 || line >= len(rows) {
		return -1
	}
	return rows[line].idx
}

// zoneAt is the click target at column x of a row, if the row carries one
// there. x is in the pane's own content coordinates.
func zoneAt(rows []msgRow, line, x int) (clickZone, bool) {
	if line < 0 || line >= len(rows) {
		return clickZone{}, false
	}
	for _, z := range rows[line].zones {
		if z.hit(x) {
			return z, true
		}
	}
	return clickZone{}, false
}

// holdTop is where a rebuilt pane's viewport lands: at the new bottom when it
// was already showing the last line, and on the message its top row held
// otherwise.
func holdTop(rows []msgRow, msgs []store.Message, a lineAnchor, tail bool, fall, h int) int {
	if tail {
		return max(0, len(rows)-h)
	}
	return a.line(rows, msgs, h, fall)
}

func (m *Model) scrollMessagesToSelection() {
	m.msgTop = scrollTo(m.msgRows, m.msgIdx, m.msgTop, m.msgListHeight())
}

func (m *Model) scrollThreadToSelection() {
	m.threadTop = scrollTo(m.threadRows, m.threadIdx, m.threadTop, m.listHeight())
}

func (m *Model) centerThreadOnSelection() {
	m.threadTop = centerTo(m.threadRows, m.threadIdx, m.listHeight())
}

// lineAnchor is the line a pane's top row sits on, held as the message that
// owns it plus the offset inside that message's rows. A reload renumbers every
// line and a resize rewraps them, so a raw line index would slide the view.
type lineAnchor struct {
	id  string
	off int
}

func topAnchor(rows []msgRow, msgs []store.Message, top int) lineAnchor {
	if top <= 0 || top >= len(rows) {
		return lineAnchor{}
	}
	idx := rows[top].idx
	return lineAnchor{idAt(msgs, idx), top - firstRow(rows, idx)}
}

// line is where the anchor's message starts now, or fall when the page no
// longer carries it.
func (a lineAnchor) line(rows []msgRow, msgs []store.Message, h, fall int) int {
	i := indexOfID(msgs, a.id)
	if i < 0 {
		return clamp(fall, 0, max(0, len(rows)-h))
	}
	return clamp(firstRow(rows, i)+a.off, 0, max(0, len(rows)-h))
}

// atTail reports whether the last line is on screen, which is what makes an
// arriving message scroll the view. It is the viewport's own question: a
// cursor the wheel left parked on the newest message must not answer it.
func atTail(rows []msgRow, top, h int) bool { return top >= max(0, len(rows)-h) }

// centerTo puts a message's block in the middle of the viewport, which is
// where a pane opened on a message the reader named starts. Such a pane has
// nothing on screen yet, so revealing the message would put it against an edge
// — at the bottom with its answers below the fold, which is the half the
// reader opened the pane for.
func centerTo(rows []msgRow, idx, h int) int {
	return clamp(firstRow(rows, idx)-h/2, 0, max(0, len(rows)-h))
}

func scrollTo(rows []msgRow, idx, top, h int) int {
	if len(rows) == 0 {
		return 0
	}
	first, last := firstRow(rows, idx), lastRow(rows, idx)
	if first < top {
		top = first
	}
	if last >= top+h {
		top = last - h + 1
	}
	return clamp(top, 0, max(0, len(rows)-h))
}

// hit maps screen coordinates to a pane and the row inside its list. A row of
// -1 is the pane's own head — its title, the rule the messages pane draws
// under one, the three lines the assistant column draws — which indexes
// nothing: added to a scrolled offset it would land on a real row.
func (m Model) hit(x, y int) (pane, int) {
	body := m.bodyHeight()
	listRow := func(head int) int {
		if row := y - 1 - head; row >= 0 {
			return row
		}
		return -1
	}
	// The chats pane has a column to itself, so its rows go on past the row
	// the composer box starts at beside it.
	if x < chatsWidth {
		if y < 1 || y > m.chatsBodyHeight() {
			return -1, 0
		}
		if row := listRow(headerHeight); row >= 0 {
			return paneChats, row / chatRowStride
		}
		return paneChats, -1
	}
	inRight := m.rightOpen() && x >= m.width-m.rightWidth()
	_, boxed := m.bandAt(x)
	if boxed && y >= 1+body+1 && y < 1+body+1+m.composerHeight()+2 {
		// The row inside the box, counted past its top border. Borders land
		// outside the box's own height, which composerBand reads as neither
		// the preview nor the writing area.
		return paneInput, y - (body + 3)
	}
	// A column with no box under it runs its pane to the status bar, the way
	// the chats pane does.
	bottom := body
	if !boxed {
		bottom = m.chatsBodyHeight()
	}
	if y < 1 || y > bottom {
		return -1, 0
	}
	if inRight {
		// The assistant column draws a taller head over its rows than the
		// thread pane's title; hit must charge the head on screen or every
		// row of the pane answers one line low.
		if m.aiOpen() {
			return paneThread, listRow(aiHeadLines)
		}
		return paneThread, listRow(headerHeight)
	}
	return paneMessages, listRow(msgHeaderHeight)
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.KeyboardEnhancements = tea.KeyboardEnhancements{ReportAlternateKeys: true}
	v.WindowTitle = windowTitle(m.rows.all(m.chats, m.threads), m.unread)
	if m.width == 0 {
		v.Content = "loading…"
		return v
	}
	if m.width < minWidth || m.height < minHeight {
		v.Content = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			stDim.Render(fmt.Sprintf("terminal too small · need %d×%d", minWidth, minHeight)))
		return v
	}
	// Every column owns the box under it: the box belongs to the conversation
	// it writes into, not to the whole width of the screen, and the chats pane
	// writes into none and so runs to the status bar.
	cols := []string{m.renderChats(m.chatsBodyHeight())}
	if !m.foldRight() {
		cols = append(cols, m.renderMessages(m.bodyHeight())+"\n"+m.renderBand(sideMain))
	}
	switch {
	case m.aiOpen():
		cols = append(cols, m.rightColumn(m.renderAI))
	case m.infoOpen:
		cols = append(cols, m.rightColumn(m.renderInfo))
	case m.threadOpen():
		cols = append(cols, m.rightColumn(m.renderThread))
	}
	var out strings.Builder
	out.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cols...))
	out.WriteString("\n")
	out.WriteString(m.renderStatus())
	if m.config.open {
		v.Content = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderConfig())
		v.Cursor = m.configCursor()
		return v
	}
	if m.help.open {
		v.Content = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderHelp())
		v.Cursor = m.helpCursor()
		return v
	}
	v.Content = m.floatOver(out.String())
	v.Cursor = m.cursorAt()
	return v
}

// cursorShape is what the terminal cursor says about the mode: vim's bar
// wherever keys are text, vim's block wherever they are commands. The block is
// steady because it is parked rather than written at.
func cursorShape(md mode) (tea.CursorShape, bool) {
	if md == modeNormal || md == modeVisual || md == modeTarget || md == modeCandidates {
		return tea.CursorBlock, false
	}
	return tea.CursorBar, true
}

// cursorAt places the real terminal cursor, whose shape is what names the mode
// without the reader going back to the status bar. The composer holds the
// cursor even in the modes that do not write into it, so the block sits on the
// spot i would resume at, the way vim's does.
func (m Model) cursorAt() *tea.Cursor {
	// The panes with their border, then the composer box's own top border.
	top := m.bodyHeight() + 3
	shape, blink := cursorShape(m.mode)
	// A box starts where the column it belongs to does, so every caret inside
	// it is offset by that column as well as by its own border.
	place := func(s composerSide, c *tea.Cursor, x, y int) *tea.Cursor {
		if c == nil {
			return nil
		}
		left, w := m.bandLeft(s), m.bandWidth(s)-2
		// A line wider than its box scrolls under it and bubbles keeps the
		// offset to itself, so the caret is pinned to the last column it can
		// be in — which is where it is whenever it is the thing pushing the
		// text along.
		c.X, c.Y = min(c.X+left+x, left+w), c.Y+y
		c.Shape, c.Blink = shape, blink
		c.Color = nil // the terminal's own cursor colour wins
		return c
	}
	switch m.mode {
	case modeCommand, modeFilter, modeSearch:
		return place(m.cmdSide(), textinputCursor(m.cmdline), 1, top)
	case modeEmoji:
		return place(m.side, textinputCursor(m.picker.input), 1+lipgloss.Width(pickerPrompt()), top)
	case modeForward:
		return place(m.side, textinputCursor(m.fwd.input), 1+lipgloss.Width(fwdPrompt()), top)
	}
	// bubbles reports no cursor for a blurred widget, and every mode but
	// insert blurs the composer, so the caret is asked of a focused copy.
	ta := m.area()
	ta.Focus()
	return place(m.side, ta.Cursor(), 1, top+len(m.composerAbove(m.side, m.bandWidth(m.side)-2)))
}

// textinputCursor is where a text input's caret sits, in cells. bubbles counts
// it in runes — textinput.Model.Cursor adds Position straight to the prompt
// width — which leaves the cursor a cell short of itself for every wide
// character before it, and a chat filter is typed in Chinese.
func textinputCursor(in textinput.Model) *tea.Cursor {
	if !in.Focused() {
		return nil
	}
	typed := string([]rune(in.Value())[:in.Position()])
	return tea.NewCursor(lipgloss.Width(in.Prompt)+lipgloss.Width(typed), 0)
}

// paneStyle draws the border only; every content line is already fitted to
// width, because lipgloss' Width() would word-wrap and break the row grid.
func paneStyle(focused bool) lipgloss.Style {
	c := colDim
	if focused {
		c = colAccent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c)
}

func (m Model) renderChats(h int) string {
	vis := m.visibleRows()
	w := chatsWidth - 2
	now := time.Now()
	gap := strings.Repeat(" ", avatarGap)
	line := func(avatar, text string, selected bool) string {
		text = fit(gap+text, w-avatarWidth)
		if selected {
			avatar = m.highlightAvatar(avatar, m.focus == paneChats)
			text = m.highlightChat(text, m.focus == paneChats)
		}
		return avatar + text
	}
	// A summary carrying a reaction picture goes through joinSegs rather than
	// fit: MaxWidth measures a placeholder as the characters it is and would
	// cut one out of its cluster.
	segLine := func(avatar string, segs []rowSeg, selected bool) string {
		text := gap + m.joinSegs(segs, w-avatarWidth-avatarGap)
		if selected {
			avatar = m.highlightAvatar(avatar, m.focus == paneChats)
			text = m.highlightChat(text, m.focus == paneChats)
		}
		return avatar + text
	}
	// The whole interleave, which the header and the Unread row count over:
	// what a filter hides is still waiting.
	all := m.rows.all(m.chats, m.threads)
	lines := make([]string, 0, h)
	// A trailing row that cannot show both its lines is left out entirely.
	last := m.chatTop + min(len(vis)-m.chatTop, chatsThatFit(h-headerHeight)) - 1
	for i := m.chatTop; i <= last; i++ {
		row := vis[i]
		var r chatRow
		switch {
		case row.isFeed():
			// No summary to take: the row stands for the panel, not for a
			// conversation, so the gist cache is never asked for one.
			r = renderUnreadRow(m.avatars, row, all, m.unread, w)
		case row.isThread():
			// A thread's title is the words its root opened with, not a name,
			// so the filter has no rune positions there to underline.
			r = renderThreadRow(m.avatars, row, m.draftForThreadRow(row.thread.ThreadID), m.deps.Self,
				m.gistFor(row), now, w)
		default:
			mark, _ := m.chatIx.match(row.chat, m.chatFilter)
			r = renderChatRow(m.avatars, row, m.draftForRow(row.chatID()), m.unread[row.chatID()],
				m.cands[row.chatID()], m.gistFor(row), now, w, mark)
		}
		sel := i == m.chatIdx
		bottom := line(r.avatarBottom, r.bottom, sel)
		if len(r.segs) > 0 {
			bottom = segLine(r.avatarBottom, r.segs, sel)
		}
		lines = append(lines, line(r.avatarTop, r.top, sel), bottom)
		if i < last {
			lines = append(lines, fit("", w))
		}
	}
	for len(lines) < h-headerHeight {
		lines = append(lines, fit("", w))
	}
	content := chatsHeader(all, m.unread, m.chatFilter, w) + "\n" + strings.Join(lines, "\n")
	return paneStyle(m.focus == paneChats).Height(h).Render(content)
}

func (m Model) renderMessages(h int) string {
	w := m.messagesWidth() - 2
	header := m.renderHeader(w)
	// In the panel the line under the title is the section the top row is in.
	// A top row that is itself that rule is held open rather than drawn twice,
	// so nothing under it moves as the section slides up into the pin.
	rule, pinned := paneRule(w), false
	if m.inFeed() {
		rule, pinned = m.feedRuleLine(w)
	}
	lines := make([]string, 0, h)
	for i := m.msgTop; i < len(m.msgRows) && len(lines) < h-msgHeaderHeight; i++ {
		r := m.msgRows[i]
		if pinned && i == m.msgTop {
			lines = append(lines, fit("", w))
			continue
		}
		lines = append(lines, m.paneLine(r, w, m.inSelection(paneMessages, r.idx), m.focus == paneMessages))
	}
	// The panel answers for an empty page by the messages it holds, not by the
	// rows: with every section emptied out it still draws the line counting
	// the chats it left out, and that line is not a page.
	switch {
	case m.inFeed() && len(m.msgs) == 0:
		lines = append(lines, fit(stDim.Render("All caught up · nothing waiting"), w))
	case len(m.msgRows) == 0:
		lines = append(lines, fit(stDim.Render("no messages synced for this chat yet"), w))
	}
	for len(lines) < h-msgHeaderHeight {
		lines = append(lines, fit("", w))
	}
	content := header + "\n" + rule + "\n" + strings.Join(lines, "\n")
	return paneStyle(m.focus == paneMessages).Height(h).Render(content)
}

func (m Model) renderAI(h int) string {
	return m.aiP.renderAI(m, h)
}

func (m Model) renderHeader(w int) string {
	if m.inFeed() {
		return m.feedTitle(w)
	}
	if m.mentions {
		tail := fmt.Sprintf(" · %d · Esc to leave", len(m.searchHits))
		return fit(stBold.Render(mentionsLabel)+stDim.Render(tail), w)
	}
	if m.searching {
		// The store answers inside a keystroke and Feishu takes a round trip,
		// so the line says which half is still out rather than leaving the
		// reader to wonder whether the count is the whole answer.
		tail := fmt.Sprintf(" · %d hits · Esc to leave", len(m.searchHits))
		if m.searchBusy {
			tail = fmt.Sprintf(" · %d hits · asking Feishu…", len(m.searchHits))
		}
		return fit(stBold.Render("Search ")+stAccent.Render(m.searchQuery)+stDim.Render(tail), w)
	}
	c, ok := m.currentChat()
	if !ok {
		return fit(stDim.Render("select a chat"), w)
	}
	dot := stDim.Render(" · ")
	title := stBold.Render(cmp.Or(flatten(c.Name), "(unnamed)"))
	if g := chatModeGlyph(c.ChatMode); g != "" {
		title = stDim.Render(g) + title
	}
	// Error and tags stay readable before the title tail: the client marks
	// external rooms and sync faults on the header for a reason.
	var tailParts []string
	if c.SyncError != "" {
		tailParts = append(tailParts, stErr.Render("history unavailable"))
	}
	tailParts = append(tailParts, chatTags(c)...)
	kept := tailParts[:0]
	for _, p := range tailParts {
		if p == "" {
			continue
		}
		trial := append(kept, p)
		if lipgloss.Width(joinHeaderTail(trial, dot)) <= w {
			kept = trial
		}
	}
	tailSuffix := joinHeaderTail(kept, dot)
	tailW := lipgloss.Width(tailSuffix)
	sep := 0
	if tailSuffix != "" {
		sep = lipgloss.Width(dot)
	}
	title = truncate(title, max(0, w-tailW-sep))
	if title == "" {
		return fit(truncate(tailSuffix, w), w)
	}
	if tailSuffix == "" {
		return fit(title, w)
	}
	return fit(title+dot+tailSuffix, w)
}

func joinHeaderTail(parts []string, dot string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += dot
		}
		out += p
	}
	return out
}

// chatModeGlyph says what kind of chat the header names. The glyphs come from
// the Nerd Font the terminal maps the private use area to, so each takes the
// colour it is given and, carrying its own enSpace, measures two columns.
func chatModeGlyph(mode string) string {
	switch mode {
	case "p2p":
		return "\uf007" + enSpace
	case "group":
		return "\uf0c0" + enSpace
	case "topic":
		return "\uf075" + enSpace
	}
	return ""
}

// paneRule parts a pane's title from the list under it. It spans the whole
// content width so its ends meet the border the pane is drawn with.
func paneRule(w int) string { return stDim.Render(strings.Repeat("─", max(0, w))) }

func (m Model) renderThread(h int) string {
	w := m.rightWidth() - 2
	lines := make([]string, 0, h)
	lines = append(lines, fit(m.rightTitle(w), w))
	if m.rightNote != "" {
		lines = append(lines, fit(stDim.Render(m.rightNote), w))
	}
	for i := m.threadTop; i < len(m.threadRows) && len(lines) < h; i++ {
		r := m.threadRows[i]
		lines = append(lines, m.paneLine(r, w, m.inSelection(paneThread, r.idx), m.focus == paneThread))
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneThread).Height(h).Render(strings.Join(lines, "\n"))
}

// rightTitle names the frame and how deep it sits. The depth is what says
// Esc steps back rather than closes, which is the one thing about the column
// a reader cannot see from its contents.
func (m Model) rightTitle(w int) string {
	name, tail := "Thread", m.threadID
	switch m.rightKind {
	case rightForward:
		// The card's own title, so a frame is named the way the summary that
		// led into it was. A bundle's id says nothing a reader recognises.
		name, tail = "Forwarded", m.rightName
	case rightReply:
		// The client's own word for this pane, and beside it the message the
		// conversation started from, which is what the reader opened.
		name, tail = "Details", m.rightName
	}
	if d := len(m.rightStack) + 1; d > 1 {
		name += " " + strconv.Itoa(d)
	}
	name += " "
	return stBold.Render(name) + stDim.Render(truncate(tail, w-lipgloss.Width(name)))
}

// renderInput draws one box. The box without the keys shows its quote and its
// text and nothing else — the preview and the badge both describe the draft
// being typed, and the command line is the whole program's, so it stands in the
// box the reader is in.
func (m Model) renderInput(s composerSide) string {
	w, h := m.bandWidth(s)-2, m.composerHeight()
	if s == m.cmdSide() && (m.mode == modeCommand || m.mode == modeFilter || m.mode == modeSearch) {
		rows := []string{m.cmdline.View()}
		// The row the badge has in every other mode carries the list's hint
		// here, so the box pads between the line and the hint rather than
		// below it and the hint keeps the bottom edge.
		for len(rows) < h-1 {
			rows = append(rows, "")
		}
		rows = append(rows, m.renderCmdCompHint(w))
		return paneStyle(true).Height(h).Render(fitBlock(strings.Join(rows, "\n"), w, h))
	}
	ta := m.input
	switch s {
	case sideRight:
		ta = m.rightInput
	case sideAI:
		if m.aiP != nil {
			ta = m.aiP.input
		}
	}
	content := strings.Join(append(m.composerAbove(s, w), ta.View()), "\n")
	if s == m.side && m.composerRows().badge > 0 {
		if s == sideAI {
			lines := strings.Split(content, "\n")
			for len(lines) < h-1 {
				lines = append(lines, "")
			}
			lines = append(lines, m.renderBadge(w))
			content = strings.Join(lines, "\n")
		} else {
			content += "\n" + m.renderBadge(w)
		}
	}
	return paneStyle(m.focus == paneInput && s == m.side).Height(h).Render(fitBlock(content, w, h))
}

// renderBand is the box under one column, with whichever chooser has taken the
// reader's own box standing in for it.
func (m Model) renderBand(s composerSide) string {
	if s == m.side {
		switch m.mode {
		case modeEmoji:
			return m.renderPicker()
		case modeForward:
			return m.renderForward()
		case modeTarget:
			return m.renderTargets()
		}
	}
	return m.renderInput(s)
}

// rightColumn draws the right pane with whatever stands under it: the
// assistant panel's own box, the frame's box when the column carries one, the
// chat's box when the column is standing in for the messages pane, and nothing
// at all otherwise — in which case the pane runs to the status bar the way the
// chats pane does.
func (m Model) rightColumn(render func(int) string) string {
	switch {
	case m.aiOpen():
		return render(m.bodyHeight()) + "\n" + m.renderBand(sideAI)
	case m.rightHasComposer():
		return render(m.bodyHeight()) + "\n" + m.renderBand(sideRight)
	case m.foldRight():
		return render(m.bodyHeight()) + "\n" + m.renderBand(sideMain)
	}
	return render(m.chatsBodyHeight())
}

// composerHint names the two keys the composer's own mode owns, and writeHint
// the one key that reaches that mode. They sit on the badge row rather than in
// the placeholder, which a draft covers up and which would go on telling a
// reader already in insert mode to start writing.
const (
	composerHint = "Enter send · Shift+Enter newline"
	writeHint    = "i to write"
)

// renderBadge names the message type the draft will be sent as, so the
// composer's choice is never a surprise Enter springs on the reader. Outside
// insert mode the row names the key that opens it instead of the keys that
// send. The assistant's box asks rather than sends, so its row names / snippets.
func (m Model) renderBadge(w int) string {
	if m.side == sideAI {
		return m.renderSnippetRow(w)
	}
	left := stChipEdge.Render(chipLeft) + stChip.Render(m.draft.badge()) + stChipEdge.Render(chipRight)
	hint := stDim.Render(composerHint)
	switch {
	case m.mode == modeCandidates && m.candRows() > 0:
		hint = stDim.Render(strconv.Itoa(m.cand.idx+1) + "/" + strconv.Itoa(len(m.cand.items)) + " · " + candHint)
	case m.mode != modeInsert:
		hint = stDim.Render(writeHint)
	case m.pumShowing():
		// The popup has taken Enter, so the row says so rather than going on
		// promising a send. Its count rides here too, which is what keeps the
		// popup itself to offers alone.
		hint = stDim.Render(strconv.Itoa(m.pum.menu.idx+1) + "/" + strconv.Itoa(len(m.pum.menu.items)) + " · " + pumHint)
	}
	room := max(0, w-lipgloss.Width(left)-lipgloss.Width(hint)-2)
	if m.draftErr != nil {
		return padBetween(left+" "+stErr.Render(truncate(m.draftErr.Error(), room)), hint, w)
	}
	// A path the draft cannot send beats what it merely loses, so the lint
	// takes the row only once there is no error on it.
	if n := len(m.draftLint); n > 0 {
		warn := m.draftLint[0].Message
		if n > 1 {
			warn = strconv.Itoa(n) + " · " + warn
		}
		return padBetween(left+" "+stWarn.Render(truncate(warn, room)), hint, w)
	}
	if d := m.draft.detail(); d != "" {
		left += " " + stDim.Render(truncate(d, room))
	}
	return padBetween(left, hint, w)
}

const statusHelpHint = "? help"

func (m Model) renderStatus() string {
	left := fmtStatus(m)
	leftPart := " " + left
	room := max(0, m.width-lipgloss.Width(leftPart)-lipgloss.Width(statusHelpHint)-2)
	var mid string
	switch {
	case m.notice != "" && m.noticeErr:
		mid = stErr.Render(truncate(m.notice, room))
	case m.confirm.kind != confirmNone && m.notice != "" && !m.noticeErr:
		mid = formatConfirmNotice(m.notice, room)
	case m.notice != "" && !m.noticeErr:
		mid = truncateStatusNotice(m.notice, room)
	case m.statusWarn != "":
		mid = stWarn.Render(truncate(m.statusWarn, room))
	}
	right := padBetween(mid, statusHelpHint, m.width-lipgloss.Width(leftPart))
	return m.th.sel.Render(leftPart + right)
}

func truncateStatusNotice(notice string, room int) string {
	if room <= 0 {
		return ""
	}
	return truncate(notice, room)
}

// highlight paints the selected row, brighter when its pane has focus.
func (m Model) highlight(line string, focused bool) string {
	if focused {
		return paint(m.th.sel, line)
	}
	return paint(m.th.selInact, line)
}

// highlightChat paints the chat row under the cursor. A focused chats pane
// takes the client's fixed tint rather than a shade of the terminal
// background, so the chat being read looks the same on every palette.
func (m Model) highlightChat(line string, focused bool) string {
	if !focused {
		return m.highlight(line, false)
	}
	return paint(stChatSel, line)
}

// highlightAvatar tints the avatar column of the row under the cursor. Only
// the background travels: a picture's cells name their image in the
// foreground, and a terminal paints the cell background under a placement, so
// the tint reaches what the avatar's disc leaves clear.
func (m Model) highlightAvatar(cells string, focused bool) string {
	if !focused {
		return paint(m.th.selInact, cells)
	}
	return paint(stChatSelBG, cells)
}

// paint wraps a row in st. The row carries styles of its own whose resets
// would end a plainly wrapped colour, so st is re-asserted after each reset —
// after the resets alone, so that a run naming its own colour keeps it.
func paint(st lipgloss.Style, line string) string {
	a := ansi.Style{}.BackgroundColor(st.GetBackground())
	if fg := st.GetForeground(); fg != (lipgloss.NoColor{}) {
		a = a.ForegroundColor(fg)
	}
	sgr := a.String()
	return st.Render(sgrReset.ReplaceAllStringFunc(line, func(s string) string { return s + sgr }))
}

// truncate cuts s to n columns, the last of them spent on an ellipsis saying
// it was cut. The cut goes through cut rather than trimming runes: trimming
// runes drops the reset that closes a colour as readily as the text it closed,
// and the colour then runs on into whatever is drawn beside it — one picker
// cell tinting its neighbour across the grid. cut also keeps a grapheme
// cluster whole, so a keycap emoji is not left as a bare digit.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return cut(s, n-1) + "…"
}

// flatten collapses newlines and tabs so one-line fields stay one line.
func flatten(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u200b", "")), " ")
}

// fit cuts a styled line to w columns without wrapping and pads it to w so
// row highlights span the pane.
func fit(s string, w int) string {
	s = cut(lipgloss.NewStyle().Inline(true).Render(s), w)
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// fitBlock fits every line of a block to w and clips it to h lines.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i := range lines {
		lines[i] = fit(lines[i], w)
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return strings.Join(lines, "\n")
}
