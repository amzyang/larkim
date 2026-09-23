package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// cursorModel is the chat list with a real store behind it, so opening a chat
// runs the queries it runs in the app.
func cursorModel(t *testing.T) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := sized(120, 36)
	m.deps.Store = st
	m.chatID, m.chatIdx, m.chatTop = "oc_0", 0, 0
	return m
}

// reorder moves the chat at from to index to, the way a read status flipping
// carries a chat out of the unread group and down the list.
func reorder(chats []store.Chat, from, to int) []store.Chat {
	c := chats[from]
	rest := append(append([]store.Chat{}, chats[:from]...), chats[from+1:]...)
	return append(append(append([]store.Chat{}, rest[:to]...), c), rest[to:]...)
}

func TestRepinChat_HoldsTheRowTheCursorSitsOn(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_40", 40, 30
	row := m.chatIdx - m.chatTop

	m.chats = reorder(m.chats, 40, 45)
	m.repinChat(m.chatID)

	require.Equal(t, 45, m.chatIdx, "the cursor follows the chat, not the row")
	require.Equal(t, row, m.chatIdx-m.chatTop, "and the chat stays on the screen row it was on")
}

func TestRepinChat_StaysInsideTheListWhenTheChatMovesToTheTop(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_40", 40, 30

	m.chats = reorder(m.chats, 40, 1)
	m.repinChat(m.chatID)

	require.Equal(t, 1, m.chatIdx)
	require.GreaterOrEqual(t, m.chatTop, 0, "renderChats indexes the list straight from chatTop")
	require.Less(t, m.chatIdx-m.chatTop, m.chatListHeight(), "the cursor is on screen")
}

func TestRepinChat_StaysInsideTheListWhenItShrinks(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_40", 40, 30

	m.chats = m.chats[:3]
	m.chatID = "oc_1"
	m.repinChat(m.chatID)

	require.Equal(t, 1, m.chatIdx)
	require.GreaterOrEqual(t, m.chatTop, 0)
	require.LessOrEqual(t, m.chatTop, max(0, len(m.chats)-m.chatListHeight()))
}

func TestMoveToChat_OpensWhatACursorAtRestLandsOn(t *testing.T) {
	m := cursorModel(t)
	m.chatIdx = 1

	require.Contains(t, collect(m.moveToChat(time.Unix(1000, 0))), "tui.messagesLoadedMsg",
		"a single move costs no delay")
	require.Equal(t, "oc_1", m.pendingChat)
}

func TestMoveToChat_WaitsWhileTheCursorIsStillMoving(t *testing.T) {
	m := cursorModel(t)
	now := time.Unix(1000, 0)
	m.chatIdx = 1
	m.moveToChat(now)

	m.chatIdx = 2
	cmd := m.moveToChat(now.Add(30 * time.Millisecond))

	require.Equal(t, "oc_1", m.pendingChat, "the second move asks for nothing yet")
	require.Equal(t, []string{"tui.chatRestMsg"}, collect(cmd))
}

func TestMoveToChat_ASweepLoadsOnePageNotOnePerRow(t *testing.T) {
	m := cursorModel(t)
	now := time.Unix(1000, 0)

	// Each page costs a message query and three detail queries, so this counts
	// what a held j spends on the rows it passes.
	var loaded []string
	for i := 1; i <= 30; i++ {
		m.chatIdx = i
		was := m.pendingChat
		m.moveToChat(now.Add(time.Duration(i) * 30 * time.Millisecond))
		if m.pendingChat != was {
			loaded = append(loaded, m.pendingChat)
		}
	}

	require.Equal(t, []string{"oc_1"}, loaded, "the row the sweep set off from, and nothing it swept past")
}

func TestClaimChatOpen_OnlyForTheChatTheCursorStoppedOn(t *testing.T) {
	m := cursorModel(t)
	m.chatIdx = 9

	require.False(t, m.claimChatOpen("oc_5"), "a row the cursor has already left")
	require.True(t, m.claimChatOpen("oc_9"))
	m.chatID = "oc_9"
	require.False(t, m.claimChatOpen("oc_9"), "the chat is already the one on screen")
}

func TestOpenChat_KeepsThePageItIsOnUntilTheNewOneArrives(t *testing.T) {
	m := cursorModel(t)
	m.msgs = []store.Message{{MessageID: "om_old", ChatID: "oc_0", RenderedAt: 1, Content: "old"}}
	m.rebuildMessages()

	m.openChat("oc_7")
	require.Equal(t, "oc_0", m.chatID, "the panes stay on the chat they are showing")
	require.Equal(t, "om_old", m.msgs[0].MessageID)
	require.NotEmpty(t, m.msgRows)

	next, _ := m.Update(messagesLoadedMsg{chatID: "oc_7",
		msgs: []store.Message{{MessageID: "om_new", ChatID: "oc_7", RenderedAt: 1, Content: "new"}}})
	m = next.(Model)

	require.Equal(t, "oc_7", m.chatID, "and change over when its page lands")
	require.Equal(t, "om_new", m.msgs[0].MessageID)
	require.Empty(t, m.pendingChat)
}

func TestActivate_OpensTheHighlightedChatWithoutWaiting(t *testing.T) {
	m := cursorModel(t)
	m.focus, m.chatIdx = paneChats, 7

	next, cmd := m.activate()
	m = next.(Model)

	require.Equal(t, "oc_7", m.pendingChat, "Enter skips the rest delay")
	require.Equal(t, paneMessages, m.focus)
	require.Contains(t, collect(cmd), "tui.messagesLoadedMsg")
}

func TestChatsLoaded_ACursorAheadOfTheOpenChatKeepsItsPlace(t *testing.T) {
	m := sized(120, 36)
	// A sweep down the list left the cursor far from the chat whose page is
	// still the one on screen.
	m.chatID, m.chatIdx, m.chatTop = "oc_1", 40, 30

	mm, _ := m.update(chatsLoadedMsg{chats: m.chats})
	m = mm.(Model)

	require.Equal(t, 40, m.chatIdx, "a reload belongs to the list, not to the cursor")
	require.Equal(t, 30, m.chatTop)
}
