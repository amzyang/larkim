package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// headChats is a list whose mute settings the caller picks, one chat per rune:
// 'u' unmuted, 'm' muted. unread is keyed the same way.
func headChats(spec string) ([]store.Chat, map[string]int64) {
	chats := make([]store.Chat, 0, len(spec))
	unread := map[string]int64{}
	for i, r := range spec {
		id := string(rune('a' + i))
		chats = append(chats, store.Chat{ChatID: id, Name: id, Muted: r == 'm'})
		unread[id] = 1
	}
	return chats, unread
}

func head(t *testing.T, chats []store.Chat, unread map[string]int64, filter string, w int) string {
	t.Helper()
	line := chatsHeader(chats, unread, filter, w)
	require.Equal(t, w, lipgloss.Width(line), "the header always fills the pane")
	return ansi.Strip(line)
}

func TestUnreadChats_CountsChatsNotMessages(t *testing.T) {
	chats, unread := headChats("uu")
	unread["a"], unread["b"] = 5, 7
	n, muted := unreadChats(chats, unread)
	require.EqualValues(t, 2, n, "two chats are waiting, not twelve messages")
	require.False(t, muted)
}

func TestUnreadChats_MutedOnesOnlyRaiseTheDot(t *testing.T) {
	chats, unread := headChats("mmu")
	n, muted := unreadChats(chats, unread)
	require.EqualValues(t, 1, n, "only the unmuted chat is counted")
	require.True(t, muted)
}

func TestUnreadChats_ReadChatsCountForNeither(t *testing.T) {
	chats, unread := headChats("um")
	unread["a"], unread["b"] = 0, 0
	n, muted := unreadChats(chats, unread)
	require.Zero(t, n)
	require.False(t, muted, "a muted chat with nothing waiting raises no dot")
}

func TestChatsHeader_RaisesTheCount(t *testing.T) {
	chats, unread := headChats("uuu")
	require.Contains(t, head(t, chats, unread, "", 36), "Chats³")
}

func TestChatsHeader_CapsAt99Plus(t *testing.T) {
	chats, unread := headChats(strings.Repeat("u", 120))
	require.Contains(t, head(t, chats, unread, "", 36), "Chats⁹⁹⁺")
}

func TestChatsHeader_SaysNothingWhenEverythingIsRead(t *testing.T) {
	chats, unread := headChats("um")
	unread["a"], unread["b"] = 0, 0
	require.Equal(t, "Chats"+strings.Repeat(" ", 31), head(t, chats, unread, "", 36))
}

func TestChatsHeader_DotSitsAtTheRightEdge(t *testing.T) {
	chats, unread := headChats("mu")
	line := head(t, chats, unread, "", 36)
	require.Equal(t, "Chats¹", strings.TrimSpace(strings.TrimSuffix(line, mutedDot)))
	require.True(t, strings.HasSuffix(line, mutedDot), "the dot ends the row: %q", line)
}

func TestChatsHeader_DotStandsAloneWhenOnlyMutedChatsWait(t *testing.T) {
	chats, unread := headChats("mm")
	line := head(t, chats, unread, "", 36)
	require.Equal(t, "Chats"+strings.Repeat(" ", 30)+mutedDot, line)
}

func TestChatsHeader_CountsBehindAFilter(t *testing.T) {
	chats, unread := headChats("uuu")
	require.Contains(t, head(t, chats, unread, "zhou", 36), "Chats /zhou³",
		"the filter hides rows, not what is waiting")
}

func TestChatsHeader_LongFilterYieldsToTheSignals(t *testing.T) {
	chats, unread := headChats("mu")
	line := head(t, chats, unread, strings.Repeat("x", 60), 36)
	require.Contains(t, line, "…¹", "the filter is cut, the count is not")
	require.True(t, strings.HasSuffix(line, mutedDot))
}

func TestChatsHeader_IsDrawnInTheUnreadColour(t *testing.T) {
	chats, unread := headChats("mu")
	line := chatsHeader(chats, unread, "", 36)
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
	chats, unread := headChats("u")
	line := head(t, chats, unread, strings.Repeat("x", 60), 36)
	require.True(t, strings.HasSuffix(line, "…¹"),
		"with no dot there is no column to keep clear of: %q", line)
}
