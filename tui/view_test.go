package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func sized(w, h int) Model {
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = w, h
	for i := range 80 {
		name := fmt.Sprintf("群 %d 【语言】灵创问题及需求沟通群很长很长的名字 %d", i, i)
		if i%3 == 0 {
			name = "line1\nline2 " + name
		}
		m.chats = append(m.chats, store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: name, ChatMode: "group"})
	}
	m.chatID = "oc_1"
	for i := range 40 {
		m.msgs = append(m.msgs, store.Message{MessageID: fmt.Sprintf("om_%d", i), ChatID: "oc_1", SenderName: "邹洋", SenderID: "ou_me",
			Content: strings.Repeat("内容很长 content ", 12), RenderedAt: 1, CreateMs: int64(i) * 1000})
	}
	m.layout()
	return m
}

func withThread(m Model) Model {
	m.threadOpen, m.threadID, m.thread = true, "omt_1", m.msgs[:3]
	m.layout()
	return m
}

func TestPanesShareHeight(t *testing.T) {
	m := sized(120, 40)
	h := m.bodyHeight()
	require.Equal(t, h+2, lipgloss.Height(m.renderChats(h)), "chats pane = body + border")
	require.Equal(t, h+2, lipgloss.Height(m.renderMessages(h)), "messages pane = body + border")
	m = withThread(m)
	require.Equal(t, h+2, lipgloss.Height(m.renderThread(h)))
	v := m.View()
	require.Equal(t, m.height, lipgloss.Height(v.Content), "whole view fits the terminal exactly")
	for _, line := range strings.Split(v.Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "no line wider than the terminal: %q", line)
	}
}

func TestChatsPaneKeepsOneRowPerChat(t *testing.T) {
	m := sized(120, 30)
	h := m.bodyHeight()
	lines := strings.Split(m.renderChats(h), "\n")
	require.Len(t, lines, h+2)
	require.Contains(t, lines[2], "line1 line2", "multi-line names are flattened onto one row")
}

func TestScrollToKeepsSelectionVisible(t *testing.T) {
	m := sized(120, 20)
	m.msgs = append(m.msgs, store.Message{MessageID: "om_last", ChatID: "oc_1", SenderName: "邹洋", Content: "LASTLINE", RenderedAt: 1, CreateMs: 99_000})
	m.layout()
	m.msgIdx = len(m.msgs) - 1
	m.scrollMessagesToSelection()
	require.Contains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), "LASTLINE", "the selected message's last row is on screen")
	m.msgIdx = 0
	m.scrollMessagesToSelection()
	require.Zero(t, m.msgTop)
}

func TestLastChatStaysVisibleAfterG(t *testing.T) {
	m := sized(120, 36)
	m.focus = paneChats
	mm, _ := m.move(1 << 30)
	m = mm.(Model)
	require.Equal(t, len(m.chats)-1, m.chatIdx)
	require.Contains(t, ansi.Strip(m.renderChats(m.bodyHeight())), "群 79 ", "the selected last chat is rendered")
}

func TestHitMapsPanes(t *testing.T) {
	m := sized(120, 30)
	p, row := m.hit(2, 3)
	require.Equal(t, paneChats, p)
	require.Equal(t, 1, row, "chat rows start below the title")
	p, row = m.hit(chatsWidth+5, 3)
	require.Equal(t, paneMessages, p)
	require.Equal(t, 1, row, "message body rows start below the header")
	m = withThread(m)
	p, row = m.hit(m.width-5, 3)
	require.Equal(t, paneThread, p)
	require.Equal(t, 1, row, "thread rows start below the title")
	p, _ = m.hit(5, m.bodyHeight()+3)
	require.Equal(t, paneInput, p)
}

func TestHighlightSurvivesInnerResets(t *testing.T) {
	m := sized(120, 30)
	line := stDim.Render("12:00") + " sender " + stBold.Render("om_1")
	out := m.highlight(line, true)
	bg := ansi.Style{}.BackgroundColor(m.th.sel.GetBackground()).String()
	require.GreaterOrEqual(t, strings.Count(out, bg), 3, "background re-applied after every embedded style: %q", out)
}

func TestFilterEnterOpensHighlightedChat(t *testing.T) {
	m := sized(120, 36)
	m.mode, m.chatFilter, m.chatIdx = modeFilter, "群 70 ", 0
	mm, _ := m.onFilterKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)
	require.Equal(t, "oc_70", m.chatID)
	require.Equal(t, modeNormal, m.mode)
}

func TestEscClearsFilterAndKeepsCurrentChat(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatFilter = "oc_5", "群 7"
	m.clampChat()
	mm, _ := m.onNormalKey("esc")
	m = mm.(Model)
	require.Empty(t, m.chatFilter)
	require.Equal(t, "oc_5", m.visibleChats()[m.chatIdx].ChatID)
}

func TestOpeningHiddenChatDropsFilter(t *testing.T) {
	m := sized(120, 36)
	m.chatFilter = "群 7"
	m.openChat("oc_1")
	require.Empty(t, m.chatFilter)
	require.Equal(t, "oc_1", m.visibleChats()[m.chatIdx].ChatID)
}

func TestTabCyclesListPanesOnly(t *testing.T) {
	m := sized(120, 36)
	m.focus = paneMessages
	mm, _ := m.onNormalKey("tab")
	m = mm.(Model)
	require.Equal(t, paneChats, m.focus)
	require.Equal(t, modeNormal, m.mode)
	m = withThread(m)
	m.focus = paneMessages
	mm, _ = m.onNormalKey("tab")
	m = mm.(Model)
	require.Equal(t, paneThread, m.focus)
	mm, _ = m.onNormalKey("l")
	m = mm.(Model)
	require.Equal(t, paneThread, m.focus, "l stops at the right edge")
	mm, _ = m.onNormalKey("h")
	m = mm.(Model)
	require.Equal(t, paneMessages, m.focus)
}

func TestNarrowTerminalFoldsRightPane(t *testing.T) {
	m := sized(82, 35)
	m = withThread(m)
	m.focus = paneThread
	require.True(t, m.foldRight())
	v := m.View()
	for _, line := range strings.Split(v.Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "%q", line)
	}
	require.Contains(t, ansi.Strip(v.Content), "Thread omt_1")
	p, _ := m.hit(chatsWidth+5, 3)
	require.Equal(t, paneThread, p)
	mm, _ := m.onNormalKey("h")
	m = mm.(Model)
	require.Equal(t, paneChats, m.focus, "h skips the hidden messages pane")
}

func TestOnInsertKey_EscOnFoldedLayoutShowsMessages(t *testing.T) {
	m := withThread(sized(82, 35))
	m.mode, m.focus = modeInsert, paneInput
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)
	require.Equal(t, paneMessages, m.focus)
	require.False(t, m.threadOpen, "the right pane that covered the messages closes")
}

func TestTooSmallTerminal(t *testing.T) {
	m := sized(50, 10)
	require.Contains(t, m.View().Content, "too small")
}

func TestBackgroundColorDrivesSelectionShade(t *testing.T) {
	m := New(Deps{})
	light := lipgloss.Color("#eff1f5")
	mm, _ := m.Update(tea.BackgroundColorMsg{Color: light})
	m = mm.(Model)
	require.Less(t, luma(m.th.sel.GetBackground()), luma(light), "light theme: selection darker than the background")
	dark := lipgloss.Color("#1e1e2e")
	mm, _ = m.Update(tea.BackgroundColorMsg{Color: dark})
	m = mm.(Model)
	require.Greater(t, luma(m.th.sel.GetBackground()), luma(dark), "dark theme: selection lighter than the background")
}

func TestComposerStylesFollowPalette(t *testing.T) {
	for _, dark := range []bool{true, false} {
		st := composerStyles(dark)
		require.Equal(t, lipgloss.NoColor{}, st.Focused.CursorLine.GetBackground(), "no cursor-line shade")
		require.Equal(t, colDim, st.Focused.Placeholder.GetForeground())
	}
}

func luma(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
}

func TestSearchHitAnchorsMessagePage(t *testing.T) {
	m := sized(120, 36)
	m.searching, m.focus, m.msgIdx = true, paneMessages, 0
	m.searchResults = []store.Message{{MessageID: "om_old", ChatID: "oc_2", CreateMs: 123}}
	m = m.notify("1 hit", false)
	mm, _ := m.activate()
	m = mm.(Model)
	require.Equal(t, "oc_2", m.chatID)
	require.Empty(t, m.notice, "the search notice does not outlive the search")
	require.Equal(t, "om_old", m.pendingSelect)
	require.EqualValues(t, 123, m.msgSince)
	q := messageQuery("oc_2", 123)
	require.EqualValues(t, 123, q.SinceMs)
	require.False(t, q.Desc)
	require.Greater(t, q.Limit, messagePageSize)
	q = messageQuery("oc_2", 0)
	require.True(t, q.Desc)
	require.Equal(t, messagePageSize, q.Limit)
}

func TestStatusBarStaysOneLine(t *testing.T) {
	m := sized(120, 36)
	m = m.notify("no messages match "+strings.Repeat("z", 200), true)
	s := m.renderStatus()
	require.Equal(t, 1, lipgloss.Height(s))
	require.Equal(t, m.width, lipgloss.Width(s))
}
