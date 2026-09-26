package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

func TestRepinChat_CursorAndViewportFollowTheirOwnChats(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_40", 40, 34
	// Moving 40 down to 45 walks 41..45 up one place each, carrying both the
	// cursor's chat and the chat on the top row with it.
	m.chats = reorder(m.chats, 40, 45)
	m.repinChat("oc_40", "oc_34")

	require.Equal(t, 45, m.chatIdx, "the cursor follows the chat, not the row")
	require.Equal(t, 34, m.chatTop, "and the viewport follows the chat on its top row")
	require.Equal(t, 11, m.chatIdx-m.chatTop, "so the cursor moved down the screen and the list did not move")
}

func TestRepinChat_ViewportHoldsItsTopRowWhenTheCursorsChatIsCarriedAway(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_40", 40, 30

	// A message lands in the chat under the cursor and carries it to the top.
	// The Feishu client does not scroll the sidebar after it; the message
	// pane's head is what names the chat that is open.
	m.chats = reorder(m.chats, 40, 1)
	m.repinChat("oc_40", "oc_30")

	require.Equal(t, 1, m.chatIdx, "the cursor follows its chat")
	require.Equal(t, 31, m.chatTop, "the chat on the top row stayed on the top row")
	require.Less(t, m.chatIdx, m.chatTop, "the cursor is allowed off screen")
}

func TestRepinChat_StaysInsideTheListWhenItShrinks(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_40", 40, 30

	m.chats = m.chats[:3]
	m.chatID = "oc_1"
	m.repinChat(m.chatID, "oc_30")

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

func TestClaimRowOpen_OnlyForTheRowTheCursorStoppedOn(t *testing.T) {
	m := cursorModel(t)
	m.chatIdx = 9

	_, ok := m.claimRowOpen("oc_5")
	require.False(t, ok, "a row the cursor has already left")
	_, ok = m.claimRowOpen("oc_9")
	require.True(t, ok)
	m.chatID = "oc_9"
	_, ok = m.claimRowOpen("oc_9")
	require.False(t, ok, "the chat is already the one on screen")
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

// Walking onto a thread row opens its frame beside the reader: the list is
// what they are reading, and the next j has to still move it.
func TestMoveToChat_AThreadRowOpensTheColumnWithoutTakingTheFocus(t *testing.T) {
	m := cursorModel(t)
	m.chats, m.threads = []store.Chat{chatAt("oc_0", 300)}, []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.focus, m.chatIdx = paneChats, 1

	// The cursor was at rest, so the row opens at once.
	m.cursorMovedAt = time.Now().Add(-time.Second)
	require.NotNil(t, m.moveToChat(time.Now()))
	next, _ := m.Update(messagesLoadedMsg{chatID: "oc_0"})
	m = next.(Model)

	require.Equal(t, rightThread, m.rightKind, "the thread is up")
	require.Equal(t, "omt_a", m.threadID)
	require.Equal(t, paneChats, m.focus)
}

// Enter says the reader means this row, and a thread row leads into the
// column rather than into the chat's page.
func TestActivate_EnterOnAThreadRowLandsInTheColumn(t *testing.T) {
	m := cursorModel(t)
	m.chats, m.threads = []store.Chat{chatAt("oc_0", 300)}, []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.focus, m.chatIdx = paneChats, 1

	next, _ := m.activate()
	next, _ = next.(Model).Update(messagesLoadedMsg{chatID: "oc_0"})
	m = next.(Model)

	require.Equal(t, "omt_a", m.threadID)
	require.Equal(t, paneThread, m.focus)
}

// The row the cursor previewed has nothing left to load, so the focus cannot
// wait for a frame to land — there is none coming.
func TestActivate_EnterOnAnAlreadyOpenThreadRowTakesTheFocus(t *testing.T) {
	m := cursorModel(t)
	m.chats, m.threads = []store.Chat{chatAt("oc_0", 300)}, []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.focus, m.chatIdx = paneChats, 1
	m.rightKind, m.threadID = rightThread, "omt_a"

	next, cmd := m.activate()
	m = next.(Model)

	require.Nil(t, cmd, "nothing to reload")
	require.Equal(t, paneThread, m.focus)
}

// A click lands in the list, so it leaves the reader there — the same place
// the TUI's other clicks leave them, the pane they clicked.
func TestOnClick_AThreadRowKeepsTheFocusInTheChatsPane(t *testing.T) {
	m := cursorModel(t)
	m.chats, m.threads = []store.Chat{chatAt("oc_0", 300)}, []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.focus, m.chatIdx, m.chatTop = paneMessages, 0, 0

	// Row 1 of the chats pane: below its border and its header, second pair.
	next, _ := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: 4, Y: 1 + headerHeight + chatRowStride})
	m = next.(Model)

	require.Equal(t, 1, m.chatIdx, "the cursor is on the thread row")
	require.Equal(t, pendingJump{id: "om_last_omt_a", thread: "omt_a"}, m.pendingSelect)
	require.Equal(t, paneChats, m.focus)
}

func TestChatsLoaded_ACursorAheadOfTheOpenChatKeepsItsPlace(t *testing.T) {
	m := sized(120, 36)
	// A sweep down the list left the cursor far from the chat whose page is
	// still the one on screen.
	m.chatID, m.chatIdx, m.chatTop = "oc_1", 40, 34

	mm, _ := m.update(chatsLoadedMsg{chats: m.chats})
	m = mm.(Model)

	require.Equal(t, 40, m.chatIdx, "a reload belongs to the list, not to the cursor")
	require.Equal(t, 34, m.chatTop)
}

// wheelChats turns the wheel n notches over the chat list, the way a hand does
// when it is looking for a chat it can see rather than moving the cursor.
func wheelChats(m Model, n int, b tea.MouseButton) Model {
	for range n {
		mm, _ := m.onWheel(tea.Mouse{Button: b, X: 5, Y: 5})
		m = mm.(Model)
	}
	return m
}

func TestOnWheel_ChatsKeepsItsScrollAcrossAReload(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatIdx, m.chatTop = "oc_0", 0, 0

	m = wheelChats(m, 3, tea.MouseWheelDown)
	top := m.chatTop
	require.Positive(t, top, "the wheel has to have somewhere to scroll")
	require.Less(t, m.chatIdx, top, "the wheel left the cursor off screen")

	mm, _ := m.update(chatsLoadedMsg{chats: m.chats})
	m = mm.(Model)

	require.Equal(t, top, m.chatTop, "a reload must not undo a wheel scroll")
	require.Equal(t, 0, m.chatIdx, "and must not move the cursor either")

	// Nothing downstream may assume the cursor is among the rows being drawn.
	require.NotPanics(t, func() { m.renderChats(m.bodyHeight()) })
	require.NotPanics(t, func() { m.avatarPrepare() })
}

func TestScrollChatToCursor_ACursorMoveBringsTheListBack(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.chatID, m.chatIdx, m.chatTop = paneChats, "oc_0", 0, 0
	m = wheelChats(m, 3, tea.MouseWheelDown)
	require.Less(t, m.chatIdx, m.chatTop)

	mm, _ := m.move(1)
	m = mm.(Model)

	require.GreaterOrEqual(t, m.chatIdx, m.chatTop, "moving the cursor is what scrolls back to it")
	require.Less(t, m.chatIdx, m.chatTop+m.chatListHeight())
}
