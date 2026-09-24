package tui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

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
		name := fmt.Sprintf("群 %d 【语言】示例问题及需求沟通群很长很长的名字 %d", i, i)
		if i%3 == 0 {
			name = "line1\nline2 " + name
		}
		m.chats = append(m.chats, store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: name, ChatMode: "group"})
	}
	m.chatID = "oc_1"
	for i := range 40 {
		m.msgsBase = append(m.msgsBase, store.Message{MessageID: fmt.Sprintf("om_%d", i), ChatID: "oc_1", SenderName: "林岚", SenderID: "ou_me",
			Content: strings.Repeat("内容很长 content ", 12), RenderedAt: 1, CreateMs: int64(i) * 60_000})
	}
	m.applyOutbox()
	m.layout()
	return m
}

func withThread(m Model) Model {
	m.threadOpen, m.threadID, m.thread = true, "omt_1", m.msgs[:3]
	m.layout()
	return m
}

func TestView_PanesShareHeight(t *testing.T) {
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

func TestRenderChats_OneRowPerChat(t *testing.T) {
	m := sized(120, 30)
	h := m.bodyHeight()
	lines := strings.Split(m.renderChats(h), "\n")
	require.Len(t, lines, h+2)
	require.Contains(t, lines[2], "line1 line2", "multi-line names are flattened onto one row")
}

func TestScrollTo_KeepsSelectionVisible(t *testing.T) {
	m := sized(120, 20)
	m.msgs = append(m.msgs, store.Message{MessageID: "om_last", ChatID: "oc_1", SenderName: "林岚", Content: "LASTLINE", RenderedAt: 1, CreateMs: 99 * 60_000})
	m.layout()
	m.msgIdx = len(m.msgs) - 1
	m.scrollMessagesToSelection()
	require.Contains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), "LASTLINE", "the selected message's last row is on screen")
	m.msgIdx = 0
	m.scrollMessagesToSelection()
	require.Zero(t, m.msgTop)
}

func TestMove_LastChatStaysVisibleAfterG(t *testing.T) {
	m := sized(120, 36)
	m.focus = paneChats
	mm, _ := m.move(1 << 30)
	m = mm.(Model)
	require.Equal(t, len(m.chats)-1, m.chatIdx)
	require.Contains(t, ansi.Strip(m.renderChats(m.bodyHeight())), "群 79 ", "the selected last chat is rendered")
}

func TestHit_MapsPanesBelowTitles(t *testing.T) {
	m := sized(120, 30)
	p, row := m.hit(2, 2)
	require.Equal(t, paneChats, p)
	require.Equal(t, 0, row, "the first chat starts on the line below the title")
	_, row = m.hit(2, 3)
	require.Equal(t, 0, row, "its second line maps to the same chat")
	_, row = m.hit(2, 4)
	require.Equal(t, 0, row, "so does the blank line that holds off the next chat")
	_, row = m.hit(2, 5)
	require.Equal(t, 1, row, "the next chat starts a stride on")
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

func TestHighlight_SurvivesInnerResets(t *testing.T) {
	m := sized(120, 30)
	line := stDim.Render("12:00") + " sender " + stBold.Render("om_1")
	out := m.highlight(line, true)
	bg := ansi.Style{}.BackgroundColor(m.th.sel.GetBackground()).String()
	require.GreaterOrEqual(t, strings.Count(out, bg), 3, "background re-applied after every embedded style: %q", out)
}

func TestOnFilterKey_EnterOpensHighlightedChat(t *testing.T) {
	m := sized(120, 36)
	m.mode, m.chatFilter, m.chatIdx = modeFilter, "群 70 ", 0
	mm, _ := m.onFilterKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)
	require.Equal(t, "oc_70", m.pendingChat, "the page is on its way; the panes change over when it lands")
	require.Equal(t, modeNormal, m.mode)
}

func TestOnNormalKey_EscClearsFilterAndKeepsCurrentChat(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatFilter = "oc_5", "群 7"
	m.clampChat()
	mm, _ := m.onNormalKey("esc")
	m = mm.(Model)
	require.Empty(t, m.chatFilter)
	require.Equal(t, "oc_5", m.visibleChats()[m.chatIdx].ChatID)
}

func TestOpenChat_DropsFilterHidingIt(t *testing.T) {
	m := sized(120, 36)
	m.chatFilter = "群 7"
	m.openChat("oc_1")
	require.Empty(t, m.chatFilter)
	require.Equal(t, "oc_1", m.visibleChats()[m.chatIdx].ChatID)
}

func TestOnNormalKey_TabCyclesListPanesOnly(t *testing.T) {
	m := sized(130, 36) // wide enough for three panes: chats + messages + thread
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

func TestOnNormalKey_GPrefixDiesWithTheKeyAfterIt(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.msgIdx = paneMessages, 20

	mm, _ := m.onNormalKey("g")
	require.True(t, mm.(Model).pendingG)
	mm, _ = mm.(Model).onNormalKey("k")
	require.False(t, mm.(Model).pendingG, "an unrelated key cancels the half-typed gg")

	mm, _ = mm.(Model).onNormalKey("g")
	require.Equal(t, 19, mm.(Model).msgIdx, "the second g arms a fresh prefix rather than completing the cancelled one")

	mm, _ = mm.(Model).onNormalKey("g")
	require.Zero(t, mm.(Model).msgIdx, "gg typed back to back still jumps to the top")
}

func TestView_FoldsRightPaneOnNarrowTerminal(t *testing.T) {
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

func TestView_TooSmallTerminal(t *testing.T) {
	m := sized(50, 10)
	require.Contains(t, m.View().Content, "too small")
}

func TestUpdate_BackgroundColorDrivesSelectionShade(t *testing.T) {
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

func TestComposerStyles_FollowPalette(t *testing.T) {
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

func TestActivate_SearchHitAnchorsMessagePage(t *testing.T) {
	m := sized(120, 36)
	m.searching, m.focus, m.msgIdx = true, paneMessages, 0
	m.searchResults = []store.Message{{MessageID: "om_old", ChatID: "oc_2", CreateMs: 123}}
	m = m.notify("1 hit", false)
	mm, _ := m.activate()
	m = mm.(Model)
	require.Equal(t, "oc_2", m.pendingChat)
	require.Empty(t, m.notice, "the search notice does not outlive the search")
	require.Equal(t, "om_old", m.pendingSelect)
	require.EqualValues(t, 123, m.pendingSince)
	q := messageQuery("oc_2", 123)
	require.EqualValues(t, 123, q.SinceMs)
	require.False(t, q.Desc)
	require.Greater(t, q.Limit, messagePageSize)
	q = messageQuery("oc_2", 0)
	require.True(t, q.Desc)
	require.Equal(t, messagePageSize, q.Limit)
}

func TestRenderStatus_StaysOneLine(t *testing.T) {
	m := sized(120, 36)
	m = m.notify("no messages match "+strings.Repeat("z", 200), true)
	s := m.renderStatus()
	require.Equal(t, 1, lipgloss.Height(s))
	require.Equal(t, m.width, lipgloss.Width(s))
}

func TestChatsLoaded_CursorFollowsItsOwnChat(t *testing.T) {
	m := sized(120, 36)
	m.chatID = "oc_3"
	m.repinChat(m.chatID)
	require.Equal(t, "oc_3", m.visibleChats()[m.chatIdx].ChatID)

	// A message in another chat re-sorts the list; the cursor belongs to the
	// chat, not to the row it happened to be on.
	reordered := append([]store.Chat{m.chats[7]}, slices.Delete(slices.Clone(m.chats), 7, 8)...)
	mm, _ := m.update(chatsLoadedMsg{chats: reordered})
	m = mm.(Model)
	require.Equal(t, "oc_3", m.visibleChats()[m.chatIdx].ChatID)
}

func TestChatsLoaded_FilterBeingTypedKeepsItsCursor(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.mode, m.chatFilter, m.chatIdx = "oc_3", modeFilter, "群 1", 2
	mm, _ := m.update(chatsLoadedMsg{chats: m.chats})
	m = mm.(Model)
	require.Equal(t, 2, m.chatIdx)
}

func TestSelection_TheSelectedMessageShowsItsTime(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.msgIdx = paneMessages, 0
	m.rebuildMessages()
	stamp := msgTime(m.msgs[3].CreateMs, time.Now())
	require.NotContains(t, fmtStatus(m), stamp)

	mm, _ := m.move(3)
	m = mm.(Model)
	require.Contains(t, fmtStatus(m), stamp, "moving the cursor spells out the new time")
	require.NotContains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), stamp,
		"no row carries a clock, so the rows do not move as the cursor does")

	mm, _ = m.onClick(tea.Mouse{Button: tea.MouseLeft, X: chatsWidth + 5, Y: 2})
	m = mm.(Model)
	require.Contains(t, fmtStatus(m), msgTime(m.msgs[m.msgIdx].CreateMs, time.Now()),
		"clicking a message spells out its time too")
}

func TestMove_StepsThroughEveryMessageAcrossMergedBlocks(t *testing.T) {
	m := sized(120, 24)
	m.focus, m.msgIdx = paneMessages, 0
	for i := range m.msgs {
		require.Equal(t, i, m.msgIdx, "j stops on every message, not on every block")
		require.GreaterOrEqual(t, firstRow(m.msgRows, i), m.msgTop, "the selected message is on screen")
		require.Less(t, lastRow(m.msgRows, i), m.msgTop+m.listHeight())
		mm, _ := m.move(1)
		m = mm.(Model)
	}
}

func TestModelPicHeight_DoesNotMoveWithTheReplyBar(t *testing.T) {
	m := sized(120, 40)
	was := m.picHeight()
	require.Equal(t, m.listHeight(), was, "a picture may fill the pane it is drawn in")

	m.replyTo = &store.Message{MessageID: "om_1"}
	require.Equal(t, was, m.picHeight(),
		"opening the reply bar must not resize every picture on screen")
	require.Equal(t, m.listHeight()+1, m.picHeight(), "at the cost of one row while it is open")
}

func TestHighlightChat_FocusedRowTakesTheFixedTint(t *testing.T) {
	tint := "48;2;231;238;252"
	m := sized(120, 36)
	m.focus = paneChats
	require.Contains(t, m.renderChats(m.bodyHeight()), tint, "the row under the cursor carries the client's tint")
	require.NotContains(t, m.highlightChat("plain", false), tint,
		"without focus the row falls back to the shaded selection")
}

func TestHighlightChat_KeepsARunsOwnColours(t *testing.T) {
	m := sized(120, 36)
	line := m.highlightChat(stUnread.Render("3")+" "+stBold.Render("群"), true)
	require.Contains(t, ansi.Strip(line), "3 群")
	require.Contains(t, line, "31m3", "a run naming its own colour keeps it")
	require.GreaterOrEqual(t, strings.Count(line, "48;2;231;238;252"), 3,
		"the tint is re-applied after every embedded style: %q", line)
}
