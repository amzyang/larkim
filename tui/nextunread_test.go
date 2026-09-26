package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chatList(ids ...string) []store.Chat {
	out := make([]store.Chat, 0, len(ids))
	for _, id := range ids {
		out = append(out, store.Chat{ChatID: id})
	}
	return out
}

// rowsOf is the chats as the pane walks them, for the tests whose subject is
// the list rather than what is in it.
func rowsOf(chats []store.Chat) []listRow {
	out := make([]listRow, 0, len(chats))
	for _, c := range chats {
		out = append(out, listRow{chat: c})
	}
	return out
}

func TestNextUnread_FindsTheNextChatWaiting(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_c": 2}

	assert.Equal(t, 2, nextUnread(rowsOf(chats), unread, 0, 1))
}

// The scan starts past the cursor, so the key moves on rather than standing
// still on a chat that still has something waiting.
func TestNextUnread_LeavesTheChatUnderTheCursor(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_a": 1, "oc_c": 1}

	assert.Equal(t, 2, nextUnread(rowsOf(chats), unread, 0, 1))
}

func TestNextUnread_WrapsAroundTheEnd(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_a": 1}

	assert.Equal(t, 0, nextUnread(rowsOf(chats), unread, 2, 1))
}

func TestNextUnread_WalksBackwards(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_a": 1}

	assert.Equal(t, 0, nextUnread(rowsOf(chats), unread, 2, -1))
	assert.Equal(t, 0, nextUnread(rowsOf(chats), unread, 1, -1))
}

// A single waiting chat is reachable from itself: one press round the whole
// list comes back to it rather than reporting nothing.
func TestNextUnread_TheOnlyWaitingChatIsReachableFromItself(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_b": 1}

	assert.Equal(t, 1, nextUnread(rowsOf(chats), unread, 1, 1))
}

// Silencing a chat is a request not to be pulled by it, and being walked onto
// it is that pull.
func TestNextUnread_SkipsMutedChats(t *testing.T) {
	chats := []store.Chat{{ChatID: "oc_a"}, {ChatID: "oc_loud", Muted: true}, {ChatID: "oc_c"}}
	unread := map[string]int64{"oc_loud": 9, "oc_c": 1}

	assert.Equal(t, 2, nextUnread(rowsOf(chats), unread, 0, 1))
}

func TestNextUnread_NothingWaitingAnswersMinusOne(t *testing.T) {
	assert.Equal(t, -1, nextUnread(rowsOf(chatList("oc_a", "oc_b")), nil, 0, 1))
	assert.Equal(t, -1, nextUnread(nil, map[string]int64{"oc_a": 1}, 0, 1))
}

// The list the key walks is the one on screen, so a filter narrows it.
func TestJumpUnread_WalksOnlyTheVisibleChats(t *testing.T) {
	m, st := draftModel(t)
	defer st.Close()
	m.chats = chatList("oc_group", "oc_peer")
	m.unread = map[string]int64{"oc_peer": 1}
	m.chatIx = newChatIndex()
	m.chatFilter = "平台"

	next, _ := m.jumpUnread(1)

	assert.Contains(t, next.(Model).notice, "nothing unread",
		"the only waiting chat is filtered out of view")
}

func TestJumpUnread_TakesTheChatsPane(t *testing.T) {
	m, st := draftModel(t)
	defer st.Close()
	m.chats = chatList("oc_group", "oc_peer")
	m.unread = map[string]int64{"oc_peer": 1}
	m.focus = paneMessages

	next, _ := m.jumpUnread(1)
	m = next.(Model)

	assert.Equal(t, paneChats, m.focus)
	assert.Equal(t, 1, m.chatIdx)
}

func TestJumpUnread_NothingWaitingLeavesTheCursorAlone(t *testing.T) {
	m, st := draftModel(t)
	defer st.Close()
	m.chats = chatList("oc_group", "oc_peer")
	m.chatIdx = 1

	next, cmd := m.jumpUnread(1)
	m = next.(Model)

	assert.Equal(t, 1, m.chatIdx)
	assert.Nil(t, cmd)
	assert.Contains(t, m.notice, "nothing unread")
}

// A thread carries a badge of its own here, so it is a row of the queue like
// any other: the key means "clear the queue", and the thread is in it.
func TestNextUnread_WalksOntoAThreadWithRepliesWaiting(t *testing.T) {
	rows := []listRow{
		{chat: store.Chat{ChatID: "oc_a"}},
		{chat: store.Chat{ChatID: "oc_a"}, thread: store.ThreadFeed{ThreadID: "omt_x", Unread: 2}},
	}

	require.Equal(t, 1, nextUnread(rows, nil, 0, 1))
	require.Equal(t, -1, nextUnread(rows[:1], map[string]int64{}, 0, 1))
}

// A thread inherits the silence of the chat it happens in.
func TestNextUnread_SkipsAThreadInAMutedChat(t *testing.T) {
	rows := []listRow{
		{chat: store.Chat{ChatID: "oc_a"}},
		{chat: store.Chat{ChatID: "oc_quiet", Muted: true}, thread: store.ThreadFeed{ThreadID: "omt_x", Unread: 2}},
	}

	require.Equal(t, -1, nextUnread(rows, nil, 0, 1))
}
