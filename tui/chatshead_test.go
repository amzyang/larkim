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
	require.Equal(t, "Chats"+strings.Repeat(" ", 28)+markAllGlyph+"  ", head(t, rows, unread, "", 36),
		"the button stands whether or not anything is waiting; nothing else does")
}

func TestChatsHeader_DotSitsAtTheRightEdge(t *testing.T) {
	rows, unread := headRows("mu")
	line := head(t, rows, unread, "", 36)
	require.True(t, strings.HasSuffix(line, mutedDot), "the dot ends the row: %q", line)
	require.Equal(t, "Chats¹", strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, mutedDot), markAllGlyph+" ")))
}

// The mute column is the one every row's bell right-aligns to, and the button
// sits beside it rather than in it — so both are where the eye already reads
// them, whichever of the two is lit.
func TestChatsHeader_HoldsTheButtonColumnWhetherOrNotADotIsDrawn(t *testing.T) {
	quiet, unreadQuiet := headRows("uu")
	muted, unreadMuted := headRows("mu")
	for _, tc := range []struct {
		name   string
		rows   []listRow
		unread map[string]int64
	}{{"no dot", quiet, unreadQuiet}, {"dot", muted, unreadMuted}} {
		t.Run(tc.name, func(t *testing.T) {
			line := []rune(head(t, tc.rows, tc.unread, "", 36))
			require.Equal(t, markAllGlyph, string(line[markAllCol(36)]),
				"a target that moves when another chat is silenced is one the reader has to look for")
		})
	}
}

func TestOnClick_TheChatsHeaderButtonLeavesTheCursorAlone(t *testing.T) {
	m, _, _ := badgeModel(t)
	m.chatIdx, m.focus = 3, paneMessages

	next, cmd := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: 1 + markAllCol(chatsWidth-2), Y: 1})
	out := next.(Model)

	require.NotNil(t, cmd, "the press is the whole point of the button")
	require.Equal(t, 3, out.chatIdx, "the head carries no row to put the cursor on")
	require.Equal(t, paneChats, out.focus)
}

func TestOnClick_TheChatsHeaderBesideTheButtonOnlyTakesFocus(t *testing.T) {
	m, _, _ := badgeModel(t)
	m.chatIdx, m.focus = 3, paneMessages

	next, cmd := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: 2, Y: 1})
	out := next.(Model)

	require.Nil(t, cmd)
	require.Equal(t, 3, out.chatIdx)
	require.Equal(t, paneChats, out.focus)
}

func TestChatsHeader_DotStandsAloneWhenOnlyMutedChatsWait(t *testing.T) {
	rows, unread := headRows("mm")
	line := head(t, rows, unread, "", 36)
	require.Equal(t, "Chats"+strings.Repeat(" ", 28)+markAllGlyph+" "+mutedDot, line)
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

func TestChatsHeader_LongFilterYieldsToTheButtonToo(t *testing.T) {
	rows, unread := headRows("u")
	line := head(t, rows, unread, strings.Repeat("x", 60), 36)
	require.Contains(t, line, "…¹", "the filter is cut, the count is not: %q", line)
	require.True(t, strings.HasSuffix(line, markAllGlyph+"  "),
		"the right-hand strip is three columns whatever the name does: %q", line)
}
