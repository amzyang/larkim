package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

const (
	chatsWidth   = 30
	threadWidth  = 44
	inputHeight  = 3
	statusHeight = 1
	headerHeight = 1
)

var (
	colAccent   = lipgloss.Color("4")
	colDim      = lipgloss.Color("8")
	colSelBg    = lipgloss.Color("237")
	colSelBgAlt = lipgloss.Color("236")
	colErr      = lipgloss.Color("1")
	colOK       = lipgloss.Color("2")

	stDim      = lipgloss.NewStyle().Foreground(colDim)
	stAccent   = lipgloss.NewStyle().Foreground(colAccent)
	stBold     = lipgloss.NewStyle().Bold(true)
	stErr      = lipgloss.NewStyle().Foreground(colErr)
	stOK       = lipgloss.NewStyle().Foreground(colOK)
	stStatus   = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))
	stSel      = lipgloss.NewStyle().Background(colSelBg)
	stSelInact = lipgloss.NewStyle().Background(colSelBgAlt)
)

// msgRow is one rendered line of a message list and the message it belongs to.
type msgRow struct {
	text string
	idx  int  // index into the backing message slice
	head bool // first line of the message
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
}

// bodyHeight is the inner height of the list panes.
func (m Model) bodyHeight() int {
	return max(1, m.height-inputHeight-2-statusHeight-2) // input border + pane border
}

func (m Model) chatListHeight() int { return m.bodyHeight() }

func (m Model) messagesWidth() int {
	w := m.width - chatsWidth
	if m.threadOpen {
		w -= threadWidth
	}
	return max(20, w)
}

func (m *Model) rebuildMessages() {
	m.msgRows = renderRows(m.msgs, m.messagesWidth()-2, m.deps.Self)
}

func (m *Model) rebuildThread() {
	if !m.threadOpen {
		m.threadRows = nil
		return
	}
	m.threadRows = renderRows(m.thread, threadWidth-2, m.deps.Self)
}

// renderRows lays messages out as "HH:MM sender  message_id" head lines
// followed by wrapped content lines.
func renderRows(msgs []store.Message, width int, self string) []msgRow {
	var rows []msgRow
	body := lipgloss.NewStyle().Width(max(10, width-2))
	for i, x := range msgs {
		sender := flatten(x.SenderName)
		if sender == "" {
			sender = x.SenderID
		}
		if x.SenderID == self {
			sender = stOK.Render(sender)
		} else {
			sender = stBold.Render(sender)
		}
		head := fmt.Sprintf("%s %s  %s", stDim.Render(time.UnixMilli(x.CreateMs).Local().Format("01-02 15:04")), sender, stDim.Render(x.MessageID))
		if x.ThreadID != "" && x.MessagePosition >= 0 {
			head += stAccent.Render(" ⤷thread")
		}
		if x.Updated {
			head += stDim.Render(" (edited)")
		}
		rows = append(rows, msgRow{text: head, idx: i, head: true})
		content := x.Content
		if x.RenderedAt == 0 {
			content = stDim.Render("(rendering…) ") + x.ContentRaw
		}
		if x.Deleted {
			content = stDim.Render("(recalled) " + content)
		}
		content = strings.ReplaceAll(strings.ReplaceAll(content, "\r", ""), "\t", "    ")
		for _, line := range strings.Split(body.Render(content), "\n") {
			rows = append(rows, msgRow{text: "  " + line, idx: i})
		}
	}
	return rows
}

func firstRow(rows []msgRow, idx int) int {
	for i, r := range rows {
		if r.idx == idx && r.head {
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

func (m *Model) scrollMessagesToSelection() {
	m.msgTop = scrollTo(m.msgRows, m.msgIdx, m.msgTop, m.bodyHeight())
}

func (m *Model) scrollThreadToSelection() {
	m.threadTop = scrollTo(m.threadRows, m.threadIdx, m.threadTop, m.bodyHeight())
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
	if y >= 1+body+1 && y < 1+body+1+inputHeight+2 {
		return paneInput, 0
	}
	if y < 1 || y > body {
		return -1, 0
	}
	row := y - 1
	switch {
	case x < chatsWidth:
		return paneChats, row
	case m.threadOpen && x >= m.width-threadWidth:
		return paneThread, row
	default:
		return paneMessages, row - headerHeight
	}
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.KeyboardEnhancements = tea.KeyboardEnhancements{ReportAlternateKeys: true}
	v.WindowTitle = "larkim"
	if m.width == 0 {
		v.Content = "loading…"
		return v
	}
	body := m.bodyHeight()
	panes := []string{m.renderChats(body)}
	panes = append(panes, m.renderMessages(body))
	if m.threadOpen {
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
	lines := make([]string, 0, h)
	w := chatsWidth - 2
	for i := m.chatTop; i < len(vis) && len(lines) < h; i++ {
		c := vis[i]
		name := flatten(c.Name)
		if name == "" {
			name = c.ChatID
		}
		mark := " "
		switch c.ChatMode {
		case "p2p":
			mark = "·"
		case "topic":
			mark = "#"
		default:
			mark = "⌂"
		}
		line := fit(fmt.Sprintf("%s %s", stDim.Render(mark), truncate(name, w-3)), w)
		if i == m.chatIdx {
			if m.focus == paneChats {
				line = stSel.Render(line)
			} else {
				line = stSelInact.Render(line)
			}
		}
		lines = append(lines, line)
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	title := "Chats"
	if m.chatFilter != "" {
		title = "Chats /" + m.chatFilter
	}
	content := fit(stBold.Render(truncate(title, w)), w) + "\n" + strings.Join(lines[:max(0, h-1)], "\n")
	return paneStyle(m.focus == paneChats, w).Height(h).Render(content)
}

func (m Model) renderMessages(h int) string {
	w := m.messagesWidth() - 2
	header := m.renderHeader(w)
	lines := make([]string, 0, h)
	for i := m.msgTop; i < len(m.msgRows) && len(lines) < h-headerHeight; i++ {
		r := m.msgRows[i]
		line := fit(r.text, w)
		if r.idx == m.msgIdx {
			if m.focus == paneMessages {
				line = stSel.Render(line)
			} else {
				line = stSelInact.Render(line)
			}
		}
		lines = append(lines, line)
	}
	if len(m.msgs) == 0 {
		lines = append(lines, fit(stDim.Render("no messages synced for this chat yet"), w))
	}
	for len(lines) < h-headerHeight {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneMessages, w).Height(h).Render(header + "\n" + strings.Join(lines, "\n"))
}

func (m Model) renderHeader(w int) string {
	c, ok := m.currentChat()
	if !ok {
		return stDim.Render("select a chat")
	}
	name := flatten(c.Name)
	if name == "" {
		name = "(unnamed)"
	}
	parts := []string{stBold.Render(name), c.ChatMode, stAccent.Render(c.ChatID), fmt.Sprintf("%d msgs", c.MessageCount)}
	if c.SyncError != "" {
		parts = append(parts, stErr.Render("history unavailable"))
	}
	return fit(strings.Join(parts, stDim.Render(" · ")), w)
}

func (m Model) renderThread(h int) string {
	w := threadWidth - 2
	lines := make([]string, 0, h)
	lines = append(lines, fit(stBold.Render("Thread ")+stDim.Render(truncate(m.threadID, w-7)), w))
	for i := m.threadTop; i < len(m.threadRows) && len(lines) < h; i++ {
		r := m.threadRows[i]
		line := fit(r.text, w)
		if r.idx == m.threadIdx {
			if m.focus == paneThread {
				line = stSel.Render(line)
			} else {
				line = stSelInact.Render(line)
			}
		}
		lines = append(lines, line)
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneThread, w).Height(h).Render(strings.Join(lines, "\n"))
}

func (m Model) renderInput() string {
	w := m.width - 2
	label := ""
	if m.replyTo != nil {
		kind := "reply"
		if m.inThrd {
			kind = "reply in thread"
		}
		label = stAccent.Render(fmt.Sprintf("%s → %s ", kind, m.replyTo.MessageID))
	}
	if m.mode == modeCommand || m.mode == modeFilter {
		return paneStyle(true, w).Height(inputHeight).Render(fitBlock(m.cmdline.View(), w, inputHeight))
	}
	content := m.input.View()
	if label != "" {
		content = label + "\n" + content
	}
	return paneStyle(m.focus == paneInput, w).Height(inputHeight).Render(fitBlock(content, w, inputHeight))
}

func (m Model) renderStatus() string {
	left := fmtStatus(m)
	right := m.notice
	if right == "" {
		right = "? help"
	}
	if m.noticeErr {
		right = stErr.Render(right)
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return stStatus.Width(m.width).Render(" " + left + strings.Repeat(" ", gap) + right)
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
