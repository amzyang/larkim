package tui

import (
	"fmt"
	"image/color"
	"regexp"
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
	minHeight        = 12
	inputHeight      = 3
	// restingComposer is the composer's inner height with nothing claimed
	// beyond the writing area and the badge row under it. Every mode draws the
	// box at least this tall, so switching mode never moves the panes.
	restingComposer = inputHeight + 1
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
	// colChatSel and colChatSelText are the client's own tint for the chat it
	// is on, kept off the shade ladder so the list reads the same wherever it
	// is opened. The text colour rides along because the tint is light on
	// every terminal: it is re-asserted per run, so a run that names its own
	// colour keeps it.
	colChatSel     = lipgloss.Color("#e7eefc")
	colChatSelText = lipgloss.Color("#1f2329")

	stDim    = lipgloss.NewStyle().Foreground(colDim)
	stAccent = lipgloss.NewStyle().Foreground(colAccent)
	stBold   = lipgloss.NewStyle().Bold(true)
	stErr    = lipgloss.NewStyle().Foreground(colErr)
	// The reader's own mention wears the filled badge the client paints it as:
	// the brand blue it owns, closed by the same caps a chip is, and white on
	// it. The fill is fixed rather than the terminal's blue because a badge
	// carries its own background, and one shaded from the theme would land as a
	// different signal on every palette.
	colMentionMe    = lipgloss.Color("#3370ff")
	stMentionMe     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(colMentionMe)
	stMentionMeEdge = lipgloss.NewStyle().Foreground(colMentionMe)
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

	// A card's button is a filled rectangle darker than the chip: a block of
	// colour with its label on it, not bracketed text, and the padding sits
	// inside the fill so the label needs none of its own.
	colBtn = lipgloss.Color("#9db4e0")
	stBtn  = lipgloss.NewStyle().Foreground(colChatSelText).Background(colBtn).Padding(0, 1)

	// sgrReset is the sequence that ends a styled run; lipgloss writes the
	// short spelling, a hand-written line may carry the long one.
	sgrReset = regexp.MustCompile("\x1b\\[0?m")
)

// theme holds the styles shaded from the terminal background, so the
// selected row (sel also paints the status bar) stays readable on light and
// dark palettes alike.
type theme struct {
	sel, selInact lipgloss.Style
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

func (m *Model) layout() {
	m.input.SetWidth(max(10, m.width-2))
	m.input.SetHeight(m.composerRows().input)
	m.cmdline.SetWidth(max(10, m.width-4))
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

// bodyHeight is the inner height of the list panes.
func (m Model) bodyHeight() int {
	return max(1, m.height-m.composerHeight()-2-statusHeight-2) // input border + pane border
}

// listHeight is the number of rows a list pane shows below its title.
func (m Model) listHeight() int { return max(1, m.bodyHeight()-headerHeight) }

// msgListHeight is listHeight for the messages pane, which spends a second row
// on the rule under its title.
func (m Model) msgListHeight() int { return max(1, m.bodyHeight()-msgHeaderHeight) }

// picHeight is the tallest a picture may be. It is the list height with the
// composer at its shortest rather than the current one: sizing to the live
// pane would re-encode and re-send every picture on screen each time the reply
// bar opens, for one row of difference.
func (m Model) picHeight() int {
	return max(1, m.height-restingComposer-statusHeight-msgHeaderHeight-4) // input border + pane border
}

// chatListHeight is how many whole chats the chat pane shows; a chat is never
// drawn with only one of its two lines.
func (m Model) chatListHeight() int { return max(1, chatsThatFit(m.listHeight())) }

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
	st := msgStyle{width: width, height: m.picHeight(), self: m.deps.Self, now: time.Now(),
		suffix: meta.suffix, people: meta.people, avatars: meta.avatars,
		res: meta.res, parents: meta.parents, dataDir: m.deps.DataDir,
		outbox: m.outboxStates(), dots: m.dots, dark: m.dark}
	if c, ok := m.currentChat(); ok {
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
	return st
}

// rebuildPreview draws the draft as the message list will draw it, through
// the same rows a sent message takes, so what is previewed and what is sent
// cannot describe different messages.
func (m *Model) rebuildPreview() {
	m.previewRows = nil
	if !m.previewOpen || m.mode != modeInsert || m.draft.kind == kindText {
		return
	}
	it := outboxItem{localID: "preview", chatID: m.chatID, msgType: m.draft.kind.msgType(),
		body: m.draft.body, images: m.draft.uploads()}
	meta := msgMeta{suffix: m.meta.suffix, people: m.meta.people, avatars: m.meta.avatars}
	resPending(&meta, []outboxItem{it})
	st := m.msgStyleFor(m.width-2, meta)
	// The body alone, not renderRows: a sender line, a day rule and a time
	// belong to a message that exists, and none of them would tell the
	// reader anything the composer above does not already say.
	var b block
	g := leads{b: &b}
	m.previewRows = bodyRows(it.message(m.deps.Self, m.selfName), 0, st, &g)
}

func (m *Model) rebuildMessages() {
	w := m.messagesWidth() - 2
	if m.searching {
		m.msgRows = renderSearchRows(m.searchHits, m.chats, m.msgStyleFor(w, m.searchMeta))
		return
	}
	m.msgRows = renderRows(m.msgs, m.msgStyleFor(w, m.meta))
}

// renderSearchRows lays the panel out: the message hits as message blocks,
// then the chats and the people, each group under a rule of its own. Row
// indices point at hits rather than messages, so one cursor walks all three.
func renderSearchRows(hits []searchHit, chats []store.Chat, st msgStyle) []msgRow {
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
		rows = append(rows, searchRule("Messages", st.width))
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
func searchRule(label string, w int) msgRow {
	return msgRow{text: fit(stDim.Render("── "+label+" "+strings.Repeat("─", max(0, w-len(label)-4))), w), plain: true}
}

// searchRowText is the one line a chat or a person takes: the name with the
// runes the query landed on underlined, and what tells two of them apart.
func searchRowText(h searchHit) string {
	if h.kind == hitChat {
		return markName(flatten(h.chat.Name), h.mark, stBold)
	}
	tail := h.user.Department
	if tail == "" {
		tail = h.user.Email
	}
	line := markName(h.user.Name, h.mark, stBold)
	if tail != "" {
		line += stDim.Render(" · " + tail)
	}
	return line
}

// aiLines wraps the assistant's answer to the right pane.
func (m Model) aiLines() []string {
	text := m.aiText
	if text == "" && m.aiBusy {
		text = "…"
	}
	return strings.Split(lipgloss.NewStyle().Width(m.rightWidth()-2).Render(text), "\n")
}

func (m *Model) rebuildThread() {
	if !m.threadOpen {
		m.threadRows = nil
		return
	}
	m.threadRows = renderRows(m.thread, m.msgStyleFor(m.rightWidth()-2, m.threadMeta))
}

// rowLine is the drawn form of one row, and whether a selection may tint it.
// A row that is one whole picture may not: it holds cells the terminal fills
// with an image, so fitting it would cut the glyph cluster apart and the tint
// would have nothing to colour. A row of pieces says for itself — body text
// takes the tint under its emoji, a chip brings its own.
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
		return lead + fit("", w), false // still on its way to the terminal
	}
	return lead + cells + strings.Repeat(" ", max(0, w-r.pic.cols)), false
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

// joinSegs draws a line whose pictures sit inside its text. It pads rather
// than fits: MaxWidth measures a picture's placeholder cells as the characters
// they are and would cut one out of its cluster, and the pieces were packed to
// the pane's width when they were built, so there is nothing to cut.
func (m Model) joinSegs(segs []rowSeg, w int) string {
	var b strings.Builder
	for _, s := range segs {
		if s.pic.cols == 0 {
			b.WriteString(s.text)
			continue
		}
		b.WriteString(m.segCells(s.pic))
	}
	line := b.String()
	return line + strings.Repeat(" ", max(0, w-lipgloss.Width(line)))
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
// -1 is the pane's own head — its title, and the rule the messages pane draws
// under one — which indexes nothing: added to a scrolled offset it would land
// on a real row.
func (m Model) hit(x, y int) (pane, int) {
	body := m.bodyHeight()
	if y >= 1+body+1 && y < 1+body+1+m.composerHeight()+2 {
		return paneInput, 0
	}
	if y < 1 || y > body {
		return -1, 0
	}
	listRow := func(head int) int {
		if row := y - 1 - head; row >= 0 {
			return row
		}
		return -1
	}
	switch {
	case x < chatsWidth:
		if row := listRow(headerHeight); row >= 0 {
			return paneChats, row / chatRowStride
		}
		return paneChats, -1
	case m.rightOpen() && x >= m.width-m.rightWidth():
		return paneThread, listRow(headerHeight)
	default:
		return paneMessages, listRow(msgHeaderHeight)
	}
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.KeyboardEnhancements = tea.KeyboardEnhancements{ReportAlternateKeys: true}
	v.WindowTitle = windowTitle(m.chats, m.unread)
	if m.width == 0 {
		v.Content = "loading…"
		return v
	}
	if m.width < minWidth || m.height < minHeight {
		v.Content = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			stDim.Render(fmt.Sprintf("terminal too small · need %d×%d", minWidth, minHeight)))
		return v
	}
	body := m.bodyHeight()
	panes := []string{m.renderChats(body)}
	if !m.foldRight() {
		panes = append(panes, m.renderMessages(body))
	}
	if m.aiOpen {
		panes = append(panes, m.renderAI(body))
	} else if m.threadOpen {
		panes = append(panes, m.renderThread(body))
	}
	top := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	var out strings.Builder
	out.WriteString(top)
	out.WriteString("\n")
	switch m.mode {
	case modeEmoji:
		out.WriteString(m.renderPicker())
	case modeTarget:
		out.WriteString(m.renderTargets())
	default:
		out.WriteString(m.renderInput())
	}
	out.WriteString("\n")
	out.WriteString(m.renderStatus())
	if m.showHelp {
		v.Content = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			paneStyle(true, m.width-4).Padding(0, 1).Render(helpText))
		return v
	}
	v.Content = out.String()
	v.Cursor = m.cursorAt()
	return v
}

// cursorShape is what the terminal cursor says about the mode: vim's bar
// wherever keys are text, vim's block wherever they are commands. The block is
// steady because it is parked rather than written at.
func cursorShape(md mode) (tea.CursorShape, bool) {
	if md == modeNormal || md == modeVisual || md == modeTarget {
		return tea.CursorBlock, false
	}
	return tea.CursorBar, true
}

// cursorAt places the real terminal cursor, whose shape is what names the mode
// without the reader going back to the status bar. The composer holds the
// cursor even in the modes that do not write into it, so the block sits on the
// spot i would resume at, the way vim's does.
func (m Model) cursorAt() *tea.Cursor {
	w := m.width - 2
	// The panes with their border, then the composer box's own top border.
	top := m.bodyHeight() + 3
	shape, blink := cursorShape(m.mode)
	place := func(c *tea.Cursor, x, y int) *tea.Cursor {
		if c == nil {
			return nil
		}
		// A line wider than its box scrolls under it and bubbles keeps the
		// offset to itself, so the caret is pinned to the last column it can
		// be in — which is where it is whenever it is the thing pushing the
		// text along.
		c.X, c.Y = min(c.X+x, m.width-2), c.Y+y
		c.Shape, c.Blink = shape, blink
		c.Color = nil // the terminal's own cursor colour wins
		return c
	}
	switch m.mode {
	case modeCommand, modeFilter, modeSearch:
		return place(textinputCursor(m.cmdline), 1, top)
	case modeEmoji:
		return place(textinputCursor(m.picker.input), 1+lipgloss.Width(pickerPrompt()), top)
	}
	// bubbles reports no cursor for a blurred widget, and every mode but
	// insert blurs the composer, so the caret is asked of a focused copy.
	ta := m.input
	ta.Focus()
	above := m.composerAbove(w)
	r := m.composerRows()
	// fitBlock drops rows off the top of an over-filled box, lifting the
	// writing area above the rows that claim to sit over it.
	clip := max(0, len(above)+r.input+r.badge-r.total())
	return place(ta.Cursor(), 1, top+len(above)-clip)
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
func paneStyle(focused bool, _ int) lipgloss.Style {
	c := colDim
	if focused {
		c = colAccent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c)
}

func (m Model) renderChats(h int) string {
	vis := m.visibleChats()
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
	// A summary carrying a reaction picture is padded rather than fitted:
	// MaxWidth measures a placeholder as the characters it is and would cut
	// one out of its cluster. The pieces were packed to the row's width when
	// it was built, so there is nothing to cut.
	segLine := func(avatar string, segs []rowSeg, selected bool) string {
		text := gap
		for _, s := range segs {
			if s.pic.cols == 0 {
				text += s.text
				continue
			}
			text += m.segCells(s.pic)
		}
		text += strings.Repeat(" ", max(0, w-avatarWidth-lipgloss.Width(text)))
		if selected {
			avatar = m.highlightAvatar(avatar, m.focus == paneChats)
			text = m.highlightChat(text, m.focus == paneChats)
		}
		return avatar + text
	}
	lines := make([]string, 0, h)
	// A trailing row that cannot show both its lines is left out entirely.
	last := m.chatTop + min(len(vis)-m.chatTop, chatsThatFit(h-headerHeight)) - 1
	for i := m.chatTop; i <= last; i++ {
		mark, _ := m.chatIx.match(vis[i], m.chatFilter)
		r := renderChatRow(m.avatars, vis[i], m.unread[vis[i].ChatID], m.deps.Self, now, w, m.chatPics(), mark)
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
	content := chatsHeader(m.chats, m.unread, m.chatFilter, w) + "\n" + strings.Join(lines, "\n")
	return paneStyle(m.focus == paneChats, w).Height(h).Render(content)
}

func (m Model) renderMessages(h int) string {
	w := m.messagesWidth() - 2
	header := m.renderHeader(w)
	lines := make([]string, 0, h)
	for i := m.msgTop; i < len(m.msgRows) && len(lines) < h-msgHeaderHeight; i++ {
		r := m.msgRows[i]
		line, tint := m.rowLine(r, w)
		if tint && !r.plain && m.inSelection(paneMessages, r.idx) {
			line = m.highlight(line, m.focus == paneMessages)
		}
		lines = append(lines, line)
	}
	if len(m.msgRows) == 0 {
		lines = append(lines, fit(stDim.Render("no messages synced for this chat yet"), w))
	}
	for len(lines) < h-msgHeaderHeight {
		lines = append(lines, fit("", w))
	}
	content := header + "\n" + paneRule(w) + "\n" + strings.Join(lines, "\n")
	return paneStyle(m.focus == paneMessages, w).Height(h).Render(content)
}

func (m Model) renderAI(h int) string {
	w := m.rightWidth() - 2
	lines := make([]string, 0, h)
	state := ""
	if m.aiBusy {
		state = stDim.Render(" (streaming)")
	}
	lines = append(lines, fit(stBold.Render("AI · ")+truncate(m.aiTitle, w-14)+state, w))
	body := m.aiLines()
	for i := m.aiTop; i < len(body) && len(lines) < h; i++ {
		lines = append(lines, fit(body[i], w))
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneThread, w).Height(h).Render(strings.Join(lines, "\n"))
}

func (m Model) renderHeader(w int) string {
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
	name := flatten(c.Name)
	if name == "" {
		name = "(unnamed)"
	}
	title := stBold.Render(name)
	if g := chatModeGlyph(c.ChatMode); g != "" {
		title = stDim.Render(g) + " " + title
	}
	parts := []string{title}
	if c.SyncError != "" {
		parts = append(parts, stErr.Render("history unavailable"))
	}
	return fit(strings.Join(parts, stDim.Render(" · ")), w)
}

// chatModeGlyph says what kind of chat the header names. The glyphs come from
// the Nerd Font the terminal maps the private use area to, so each holds to a
// single column and takes the colour it is given — the same arrangement
// botBadge and muteGlyph rely on.
func chatModeGlyph(mode string) string {
	switch mode {
	case "p2p":
		return "\uf007"
	case "group":
		return "\uf0c0"
	case "topic":
		return "\uf075"
	}
	return ""
}

// paneRule parts a pane's title from the list under it. It spans the whole
// content width so its ends meet the border the pane is drawn with.
func paneRule(w int) string { return stDim.Render(strings.Repeat("─", max(0, w))) }

func (m Model) renderThread(h int) string {
	w := m.rightWidth() - 2
	lines := make([]string, 0, h)
	lines = append(lines, fit(stBold.Render("Thread ")+stDim.Render(truncate(m.threadID, w-7)), w))
	for i := m.threadTop; i < len(m.threadRows) && len(lines) < h; i++ {
		r := m.threadRows[i]
		line, tint := m.rowLine(r, w)
		if tint && !r.plain && m.inSelection(paneThread, r.idx) {
			line = m.highlight(line, m.focus == paneThread)
		}
		lines = append(lines, line)
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneThread, w).Height(h).Render(strings.Join(lines, "\n"))
}

func (m Model) renderInput() string {
	w, h := m.width-2, m.composerHeight()
	if m.mode == modeCommand || m.mode == modeFilter || m.mode == modeSearch {
		return paneStyle(true, w).Height(h).Render(fitBlock(m.cmdline.View(), w, h))
	}
	content := strings.Join(append(m.composerAbove(w), m.input.View()), "\n")
	if m.composerRows().badge > 0 {
		content += "\n" + m.renderBadge(w)
	}
	return paneStyle(m.focus == paneInput, w).Height(h).Render(fitBlock(content, w, h))
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
// send.
func (m Model) renderBadge(w int) string {
	left := stChipEdge.Render(chipLeft) + stChip.Render(m.draft.kind.msgType()) + stChipEdge.Render(chipRight)
	hint := stDim.Render(composerHint)
	if m.mode != modeInsert {
		hint = stDim.Render(writeHint)
	}
	room := max(0, w-lipgloss.Width(left)-lipgloss.Width(hint)-2)
	if m.draftErr != nil {
		return padBetween(left+" "+stErr.Render(truncate(m.draftErr.Error(), room)), hint, w)
	}
	if d := m.draft.detail(); d != "" {
		left += " " + stDim.Render(truncate(d, room))
	}
	return padBetween(left, hint, w)
}

func (m Model) renderStatus() string {
	left := fmtStatus(m)
	right := m.notice
	if right == "" {
		right = "? help"
	}
	right = truncate(right, max(0, m.width-lipgloss.Width(left)-3))
	if m.noticeErr {
		right = stErr.Render(right)
	}
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	return m.th.sel.Render(fit(" "+left+strings.Repeat(" ", gap)+right, m.width))
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

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > n-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// flatten collapses newlines and tabs so one-line fields stay one line.
func flatten(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u200b", "")), " ")
}

// fit cuts a styled line to w columns without wrapping and pads it to w so
// row highlights span the pane.
func fit(s string, w int) string {
	s = lipgloss.NewStyle().MaxWidth(w).Inline(true).Render(s)
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
