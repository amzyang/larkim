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

func TestNextUnread_FindsTheNextChatWaiting(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_c": 2}

	assert.Equal(t, 2, nextUnread(chats, unread, 0, 1))
}

// The scan starts past the cursor, so the key moves on rather than standing
// still on a chat that still has something waiting.
func TestNextUnread_LeavesTheChatUnderTheCursor(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_a": 1, "oc_c": 1}

	assert.Equal(t, 2, nextUnread(chats, unread, 0, 1))
}

func TestNextUnread_WrapsAroundTheEnd(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_a": 1}

	assert.Equal(t, 0, nextUnread(chats, unread, 2, 1))
}

func TestNextUnread_WalksBackwards(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_a": 1}

	assert.Equal(t, 0, nextUnread(chats, unread, 2, -1))
	assert.Equal(t, 0, nextUnread(chats, unread, 1, -1))
}

// A single waiting chat is reachable from itself: one press round the whole
// list comes back to it rather than reporting nothing.
func TestNextUnread_TheOnlyWaitingChatIsReachableFromItself(t *testing.T) {
	chats := chatList("oc_a", "oc_b", "oc_c")
	unread := map[string]int64{"oc_b": 1}

	assert.Equal(t, 1, nextUnread(chats, unread, 1, 1))
}

// Silencing a chat is a request not to be pulled by it, and being walked onto
// it is that pull.
func TestNextUnread_SkipsMutedChats(t *testing.T) {
	chats := []store.Chat{{ChatID: "oc_a"}, {ChatID: "oc_loud", Muted: true}, {ChatID: "oc_c"}}
	unread := map[string]int64{"oc_loud": 9, "oc_c": 1}

	assert.Equal(t, 2, nextUnread(chats, unread, 0, 1))
}

func TestNextUnread_NothingWaitingAnswersMinusOne(t *testing.T) {
	assert.Equal(t, -1, nextUnread(chatList("oc_a", "oc_b"), nil, 0, 1))
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

func TestNextUnread_SkipsAChatWhoseOnlyUnreadIsAThread(t *testing.T) {
	// n means "clear the queue", and the queue is the badge's. Two levels of
	// waiting would stop it being a key anybody can learn.
	chats := []store.Chat{
		{ChatID: "oc_thread", ThreadWaiting: true},
		{ChatID: "oc_badge"},
	}
	unread := map[string]int64{"oc_badge": 2}

	require.Equal(t, 1, nextUnread(chats, unread, 0, 1))
	require.Equal(t, -1, nextUnread(chats[:1], map[string]int64{}, 0, 1))
}
