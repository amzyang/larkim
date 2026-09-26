package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// headRows is a list whose mute settings the caller picks, one chat per rune:
// 'u' unmuted, 'm' muted. unread is keyed the same way.
func headRows(spec string) ([]listRow, map[string]int64) {
	rows := make([]listRow, 0, len(spec))
	unread := map[string]int64{}
	for i, r := range spec {
		id := string(rune('a' + i))
		rows = append(rows, listRow{chat: store.Chat{ChatID: id, Name: id, Muted: r == 'm'}})
		unread[id] = 1
	}
	return rows, unread
}

func head(t *testing.T, rows []listRow, unread map[string]int64, filter string, w int) string {
	t.Helper()
	line := chatsHeader(rows, unread, filter, w)
	require.Equal(t, w, lipgloss.Width(line), "the header always fills the pane")
	return ansi.Strip(line)
}

func TestUnreadMessages_SumsAcrossChats(t *testing.T) {
	rows, unread := headRows("uu")
	unread["a"], unread["b"] = 5, 7
	n, muted := unreadMessages(rows, unread)
	require.EqualValues(t, 12, n, "twelve messages are waiting, spread over two chats")
	require.False(t, muted)
}

func TestUnreadMessages_MutedOnesOnlyRaiseTheDot(t *testing.T) {
	rows, unread := headRows("mmu")
	unread["a"], unread["c"] = 9, 4
	n, muted := unreadMessages(rows, unread)
	require.EqualValues(t, 4, n, "only the unmuted chat's messages are counted")
	require.True(t, muted)
}

func TestUnreadMessages_ReadChatsCountForNeither(t *testing.T) {
	rows, unread := headRows("um")
	unread["a"], unread["b"] = 0, 0
	n, muted := unreadMessages(rows, unread)
	require.Zero(t, n)
	require.False(t, muted, "a muted chat with nothing waiting raises no dot")
}

func TestChatsHeader_RaisesTheCount(t *testing.T) {
	rows, unread := headRows("uuu")
	unread["a"] = 3
	require.Contains(t, head(t, rows, unread, "", 36), "Chats⁵")
}

func TestChatsHeader_CapsAt99Plus(t *testing.T) {
	rows, unread := headRows(strings.Repeat("u", 120))
	require.Contains(t, head(t, rows, unread, "", 36), "Chats⁹⁹⁺")
}

func TestChatsHeader_SaysNothingWhenEverythingIsRead(t *testing.T) {
	rows, unread := headRows("um")
	unread["a"], unread["b"] = 0, 0
	require.Equal(t, "Chats"+strings.Repeat(" ", 31), head(t, rows, unread, "", 36))
}

func TestChatsHeader_DotSitsAtTheRightEdge(t *testing.T) {
	rows, unread := headRows("mu")
	line := head(t, rows, unread, "", 36)
	require.Equal(t, "Chats¹", strings.TrimSpace(strings.TrimSuffix(line, mutedDot)))
	require.True(t, strings.HasSuffix(line, mutedDot), "the dot ends the row: %q", line)
}

func TestChatsHeader_DotStandsAloneWhenOnlyMutedChatsWait(t *testing.T) {
	rows, unread := headRows("mm")
	line := head(t, rows, unread, "", 36)
	require.Equal(t, "Chats"+strings.Repeat(" ", 30)+mutedDot, line)
}

func TestChatsHeader_CountsBehindAFilter(t *testing.T) {
	rows, unread := headRows("uuu")
	require.Contains(t, head(t, rows, unread, "zhou", 36), "Chats /zhou³",
		"the filter hides rows, not what is waiting")
}

func TestChatsHeader_LongFilterYieldsToTheSignals(t *testing.T) {
	rows, unread := headRows("mu")
	line := head(t, rows, unread, strings.Repeat("x", 60), 36)
	require.Contains(t, line, "…¹", "the filter is cut, the count is not")
	require.True(t, strings.HasSuffix(line, mutedDot))
}

func TestChatsHeader_IsDrawnInTheUnreadColour(t *testing.T) {
	rows, unread := headRows("mu")
	line := chatsHeader(rows, unread, "", 36)
	require.Contains(t, line, stUnread.Render("¹"), "the count carries the unread red")
	require.Contains(t, line, stDim.Render(mutedDot), "the dot stays dim")
}

func TestSuperscript_RaisesEveryDigitAndTheCap(t *testing.T) {
	require.Equal(t, "⁰¹²³⁴⁵⁶⁷⁸⁹", superscript("0123456789"))
	require.Equal(t, "⁹⁹⁺", superscript("99+"))
	for _, r := range superscript("0123456789+") {
		require.Equal(t, 1, lipgloss.Width(string(r)), "%q has to stay one cell", string(r))
	}
}

func TestCounterStyle_MatchesThePictureItStandsInFor(t *testing.T) {
	require.Equal(t, stUnread, counterStyle(store.Chat{}),
		"a chat whose picture failed sits beside discs drawn in the unread red")
	require.Equal(t, stDim, counterStyle(store.Chat{Muted: true}))
}

func TestChatsHeader_FilterTakesTheDotsColumnWhenNoDotIsDrawn(t *testing.T) {
	rows, unread := headRows("u")
	line := head(t, rows, unread, strings.Repeat("x", 60), 36)
	require.True(t, strings.HasSuffix(line, "…¹"),
		"with no dot there is no column to keep clear of: %q", line)
}
