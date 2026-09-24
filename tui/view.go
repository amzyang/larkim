package tui

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
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
	statusHeight     = 1
	headerHeight     = 1 // title row of every list pane
)

// Foreground colours are ANSI palette indices, so they follow the terminal
// theme; shades that need a background come from theme.
var (
	colAccent = lipgloss.Color("4")
	colDim    = lipgloss.Color("8")
	colErr    = lipgloss.Color("1")
	colOK     = lipgloss.Color("2")

	stDim    = lipgloss.NewStyle().Foreground(colDim)
	stAccent = lipgloss.NewStyle().Foreground(colAccent)
	stBold   = lipgloss.NewStyle().Bold(true)
	stErr    = lipgloss.NewStyle().Foreground(colErr)
	stOK     = lipgloss.NewStyle().Foreground(colOK)
	// stMentionMe is the filled badge the client paints on the reader's own
	// mention. White on an ANSI colour reads on every terminal theme, which is
	// why the avatar block is drawn the same way.
	stMentionMe = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(colAccent)
	// stUnread is the terminal-palette stand-in for badgeRed, the disc stamped
	// onto a picture: the header's count and the digits a row falls back to when
	// no disc could be drawn both take it, so the three read as one signal.
	stUnread = lipgloss.NewStyle().Foreground(colErr).Bold(true)

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
	m.input.SetHeight(inputHeight)
	m.cmdline.SetWidth(max(10, m.width-4))
	m.rebuildMessages()
	m.rebuildThread()
	m.clampChat()
	m.scrollMessagesToSelection()
	m.scrollThreadToSelection()
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

// picHeight is the tallest a picture may be. It is the list height with the
// composer at its shortest rather than the current one: sizing to the live
// pane would re-encode and re-send every picture on screen each time the reply
// bar opens, for one row of difference.
func (m Model) picHeight() int {
	return max(1, m.height-inputHeight-statusHeight-headerHeight-4) // input border + pane border
}

// chatListHeight is how many whole chats the chat pane shows; a chat is never
// drawn with only one of its two lines.
func (m Model) chatListHeight() int { return max(1, m.listHeight()/chatRowHeight) }

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
		suffix: meta.suffix, res: meta.res, parents: meta.parents, outbox: m.outboxStates(), dots: m.dots}
	if m.replyTo != nil {
		st.quoted = m.replyTo.MessageID
	}
	if m.pics != nil {
		st.place = m.pics.place
	}
	return st
}

func (m *Model) rebuildMessages() {
	w := m.messagesWidth() - 2
	if m.searching {
		m.msgRows = renderSearchRows(m.searchResults, m.chats, m.msgStyleFor(w, m.searchMeta))
		return
	}
	m.msgRows = renderRows(m.msgs, m.msgStyleFor(w, m.meta))
}

// renderSearchRows is renderRows with each block's chat named on its sender
// line, because search hits run across chats.
func renderSearchRows(msgs []store.Message, chats []store.Chat, st msgStyle) []msgRow {
	st.names = make(map[string]string, len(chats))
	for _, c := range chats {
		st.names[c.ChatID] = c.Name
	}
	return renderRows(msgs, st)
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

// rowLine is the drawn form of one row, and whether it carries a picture. A
// picture row is already the pane's width and holds cells the terminal fills
// with an image: fitting it would cut the glyph cluster apart and a selection
// tint has nothing to colour.
func (m Model) rowLine(r msgRow, w int) (string, bool) {
	if r.pic.cols == 0 {
		return fit(r.text, w), false
	}
	cells := m.pics.cells(r.pic, r.picRow)
	if cells == "" {
		return fit("", w), true // still on its way to the terminal
	}
	return r.prefix + cells + strings.Repeat(" ", max(0, w-lipgloss.Width(r.prefix)-r.pic.cols)), true
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

// cursorInWindow pulls the cursor into the rows a scroll left on screen. The
// cursor is what a reload scrolls back to, so one left behind off screen drags
// the viewport back down with it the next time the store changes.
func cursorInWindow(rows []msgRow, idx, top, h int) int {
	switch {
	case lastRow(rows, idx) < top:
		return rowAt(rows, top)
	case firstRow(rows, idx) >= top+h:
		return rowAt(rows, min(top+h, len(rows))-1)
	}
	return idx
}

func rowAt(rows []msgRow, line int) int {
	if line < 0 || line >= len(rows) {
		return -1
	}
	return rows[line].idx
}

func (m *Model) scrollMessagesToSelection() {
	m.msgTop = scrollTo(m.msgRows, m.msgIdx, m.msgTop, m.listHeight())
}

func (m *Model) scrollThreadToSelection() {
	m.threadTop = scrollTo(m.threadRows, m.threadIdx, m.threadTop, m.listHeight())
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

// hit maps screen coordinates to a pane and the row inside its body.
func (m Model) hit(x, y int) (pane, int) {
	body := m.bodyHeight()
	if y >= 1+body+1 && y < 1+body+1+m.composerHeight()+2 {
		return paneInput, 0
	}
	if y < 1 || y > body {
		return -1, 0
	}
	row := y - 1 - headerHeight
	switch {
	case x < chatsWidth:
		return paneChats, row / chatRowHeight
	case m.rightOpen() && x >= m.width-m.rightWidth():
		return paneThread, row
	default:
		return paneMessages, row
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
	out.WriteString(m.renderInput())
	out.WriteString("\n")
	out.WriteString(m.renderStatus())
	if m.showHelp {
		v.Content = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			paneStyle(true, m.width-4).Padding(0, 1).Render(helpText))
		return v
	}
	v.Content = out.String()
	return v
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
			text = m.highlight(text, m.focus == paneChats)
		}
		return avatar + text
	}
	lines := make([]string, 0, h)
	for i := m.chatTop; i < len(vis) && len(lines)+chatRowHeight <= h-headerHeight; i++ {
		r := renderChatRow(m.avatars, vis[i], m.unread[vis[i].ChatID], m.deps.Self, now, w)
		sel := i == m.chatIdx
		lines = append(lines, line(r.avatarTop, r.top, sel), line(r.avatarBottom, r.bottom, sel))
	}
	// A trailing row that cannot show both its lines is left out entirely.
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
	for i := m.msgTop; i < len(m.msgRows) && len(lines) < h-headerHeight; i++ {
		r := m.msgRows[i]
		line, pic := m.rowLine(r, w)
		if !pic && !r.plain && m.inSelection(paneMessages, r.idx) {
			line = m.highlight(line, m.focus == paneMessages)
		}
		lines = append(lines, line)
	}
	if len(m.msgRows) == 0 {
		lines = append(lines, fit(stDim.Render("no messages synced for this chat yet"), w))
	}
	for len(lines) < h-headerHeight {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneMessages, w).Height(h).Render(header + "\n" + strings.Join(lines, "\n"))
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
		return fit(stBold.Render("Search ")+stAccent.Render(m.searchQuery)+stDim.Render(fmt.Sprintf(" · %d hits · Esc to leave", len(m.searchResults))), w)
	}
	c, ok := m.currentChat()
	if !ok {
		return fit(stDim.Render("select a chat"), w)
	}
	name := flatten(c.Name)
	if name == "" {
		name = "(unnamed)"
	}
	parts := []string{stBold.Render(name), c.ChatMode, stAccent.Render(c.ChatID)}
	if c.SyncError != "" {
		parts = append(parts, stErr.Render("history unavailable"))
	}
	return fit(strings.Join(parts, stDim.Render(" · ")), w)
}

func (m Model) renderThread(h int) string {
	w := m.rightWidth() - 2
	lines := make([]string, 0, h)
	lines = append(lines, fit(stBold.Render("Thread ")+stDim.Render(truncate(m.threadID, w-7)), w))
	for i := m.threadTop; i < len(m.threadRows) && len(lines) < h; i++ {
		r := m.threadRows[i]
		line, pic := m.rowLine(r, w)
		if !pic && !r.plain && m.inSelection(paneThread, r.idx) {
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
	if m.mode == modeCommand || m.mode == modeFilter {
		return paneStyle(true, w).Height(h).Render(fitBlock(m.cmdline.View(), w, h))
	}
	content := m.input.View()
	if m.replyTo != nil {
		content = m.renderReplyBar(w) + "\n" + content
	}
	return paneStyle(m.focus == paneInput, w).Height(h).Render(fitBlock(content, w, h))
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

// highlight paints the selected row, brighter when its pane has focus. The
// row carries styles of its own whose resets would end a plainly wrapped
// background, so the background is re-asserted after each reset — after the
// resets alone, so that a run painting its own background keeps it.
func (m Model) highlight(line string, focused bool) string {
	st := m.th.sel
	if !focused {
		st = m.th.selInact
	}
	bg := ansi.Style{}.BackgroundColor(st.GetBackground()).String()
	return st.Render(sgrReset.ReplaceAllStringFunc(line, func(s string) string { return s + bg }))
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
