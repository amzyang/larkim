package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// msgHead renders the messages pane's title row for one chat and hands back
// its plain text.
func msgHead(t *testing.T, c store.Chat, w int) string {
	t.Helper()
	m := New(Deps{Self: "ou_me"})
	m.chats, m.chatID = []store.Chat{c}, c.ChatID
	line := m.renderHeader(w)
	require.Equal(t, w, lipgloss.Width(line), "the header always fills the pane")
	return ansi.Strip(line)
}

func TestRenderHeader_NamesTheChatWithoutItsID(t *testing.T) {
	line := msgHead(t, store.Chat{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"}, 40)
	require.Contains(t, line, "平台组")
	require.NotContains(t, line, "oc_", "the id is for machines, not for the header")
	require.NotContains(t, line, "group", "the mode is a glyph now, not a word")
}

func TestRenderHeader_MarksTheChatMode(t *testing.T) {
	for mode, glyph := range map[string]string{"p2p": "", "group": "", "topic": ""} {
		line := msgHead(t, store.Chat{ChatID: "oc_quiet", Name: "平台组", ChatMode: mode}, 40)
		require.Equal(t, glyph+" 平台组", strings.TrimRight(line, " "), "mode %q", mode)
	}
}

func TestRenderHeader_UnknownModeTakesNoColumn(t *testing.T) {
	line := msgHead(t, store.Chat{ChatID: "oc_quiet", Name: "平台组"}, 40)
	require.Equal(t, "平台组", strings.TrimRight(line, " "), "no glyph means no leading gap")
}

func TestRenderHeader_KeepsTheSyncError(t *testing.T) {
	line := msgHead(t, store.Chat{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group", SyncError: "boom"}, 40)
	require.Contains(t, line, "history unavailable")
}

func TestRenderHeader_UnnamedChatStillReads(t *testing.T) {
	line := msgHead(t, store.Chat{ChatID: "oc_quiet", ChatMode: "p2p"}, 40)
	require.Contains(t, line, "(unnamed)")
}

func TestRenderMessages_RulesOffTheHeader(t *testing.T) {
	m := sized(120, 30)
	w := m.messagesWidth() - 2
	lines := strings.Split(m.renderMessages(m.bodyHeight()), "\n")
	rule := ansi.Strip(lines[2]) // past the top border and the header
	require.Contains(t, rule, strings.Repeat("─", w), "the rule spans the whole pane")
	require.Equal(t, w+2, lipgloss.Width(rule), "and meets the border either side")
}

func TestHit_TheHeaderAndItsRuleAreNotRows(t *testing.T) {
	m := sized(120, 30)
	m.msgIdx = len(m.msgs) - 1
	m.layout()
	require.Positive(t, m.msgTop, "the list has to be scrolled for a negative row to be dangerous")
	was := m.msgIdx
	for _, y := range []int{1, 2} {
		p, row := m.hit(chatsWidth+5, y)
		require.Equal(t, paneMessages, p)
		require.Negative(t, row, "y=%d is the pane's own head", y)
		mm, _ := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: chatsWidth + 5, Y: y})
		require.Equal(t, was, mm.(Model).msgIdx, "clicking the head moves no cursor")
	}
}

func TestDaySeparator_ReachesBothPaneEdges(t *testing.T) {
	for w := 40; w < 48; w++ {
		line := daySeparator("今天", w)
		require.Equal(t, w, lipgloss.Width(line), "width %d", w)
		plain := ansi.Strip(line)
		require.True(t, strings.HasPrefix(plain, "─"), "width %d: %q", w, plain)
		require.True(t, strings.HasSuffix(plain, "─"), "width %d: %q", w, plain)
	}
}
