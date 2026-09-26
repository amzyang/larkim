package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chatAt(id string, ms int64) store.Chat {
	return store.Chat{ChatID: id, Name: id, LastUnsilencedMs: ms, LastMessageMs: ms}
}

func feedAt(threadID, chatID string, ms int64) store.ThreadFeed {
	return store.ThreadFeed{ThreadID: threadID, ChatID: chatID, ChatName: chatID,
		Root: store.Message{MessageID: "om_root_" + threadID, SenderName: "张三",
			MsgType: "text", Content: "发版流程", RenderedAt: 1},
		Last: store.Message{MessageID: "om_last_" + threadID, SenderName: "李四",
			MsgType: "text", Content: "收到", RenderedAt: 1, CreateMs: ms}}
}

func keysOf(rows []listRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.key()
	}
	return out
}

func TestListRows_InterleavesThreadsWithChatsByTime(t *testing.T) {
	chats := []store.Chat{chatAt("oc_new", 300), chatAt("oc_old", 100)}
	threads := []store.ThreadFeed{feedAt("omt_a", "oc_old", 200), feedAt("omt_b", "oc_old", 50)}

	rows := listRows(chats, threads)

	assert.Equal(t, []string{"oc_new", "omt_a", "oc_old", "omt_b"}, keysOf(rows))
}

// A thread's root is a message of the chat, so the chat is where it came from.
func TestListRows_AChatComesFirstAtTheSameMoment(t *testing.T) {
	rows := listRows([]store.Chat{chatAt("oc_a", 200)}, []store.ThreadFeed{feedAt("omt_a", "oc_a", 200)})

	assert.Equal(t, []string{"oc_a", "omt_a"}, keysOf(rows))
}

// A thread and the chat it happens in are two rows leading to the same chat,
// which is why the cursor is carried by a key rather than by a chat id.
func TestListRow_AThreadLeadsIntoItsChatButKeysItself(t *testing.T) {
	r := listRows(nil, []store.ThreadFeed{feedAt("omt_a", "oc_a", 200)})[0]

	assert.True(t, r.isThread())
	assert.Equal(t, "oc_a", r.chatID())
	assert.Equal(t, "omt_a", r.key())
	assert.Equal(t, -1, indexOfChatRow([]listRow{r}, "oc_a"),
		"opening a chat pins the chat, not a conversation inside it")
}

func TestListRows_WithoutThreadsTheListIsTheChats(t *testing.T) {
	chats := []store.Chat{chatAt("oc_a", 300), chatAt("oc_b", 100)}

	assert.Equal(t, []string{"oc_a", "oc_b"}, keysOf(listRows(chats, nil)))
}

// The filter is a lens on the list: a thread answers through its chat, so a
// query naming the chat keeps both rows.
func TestVisibleRows_AFilterKeepsAThreadWithItsChat(t *testing.T) {
	m := New(Deps{Self: "ou_me"})
	m.chats = []store.Chat{chat("oc_1", "平台组"), chat("oc_2", "财务组")}
	feed := feedAt("omt_a", "oc_1", 200)
	feed.ChatName = "平台组"
	m.threads = []store.ThreadFeed{feed}
	m.chatIx = newChatIndex()
	m.chatFilter = "平台"

	// The chat has nothing newer than the thread, so the thread stands above
	// it — the filter decides who comes in, never who comes first.
	assert.Equal(t, []string{"omt_a", "oc_1"}, keysOf(m.visibleRows()))
}

// A thread row is titled by the words its root opened with, so those are what
// the reader has to type at.
func TestVisibleRows_AThreadAnswersToItsRootsOwnWords(t *testing.T) {
	m := New(Deps{Self: "ou_me"})
	m.chats = []store.Chat{chat("oc_1", "平台组")}
	feed := feedAt("omt_a", "oc_1", 200)
	feed.ChatName = "平台组"
	m.threads = []store.ThreadFeed{feed}
	m.chatIx = newChatIndex()
	m.chatFilter = "发版"

	assert.Equal(t, []string{"omt_a"}, keysOf(m.visibleRows()))
}

// Opening a thread row opens the chat under it and lands on its newest reply:
// the reader asked for the conversation, not for the word it started with.
func TestOpenRow_AThreadOpensItsChatAndLandsOnTheNewestReply(t *testing.T) {
	m := cursorModel(t)
	r := listRows(nil, []store.ThreadFeed{feedAt("omt_a", "oc_a", 200)})[0]

	require.NotNil(t, m.openRow(r))

	assert.Equal(t, "oc_a", m.pendingChat)
	assert.Equal(t, pendingJump{id: "om_last_omt_a", thread: "omt_a"}, m.pendingSelect)
}

func TestOpenRow_AChatPinsNothingInsideIt(t *testing.T) {
	m := cursorModel(t)

	require.NotNil(t, m.openRow(listRow{chat: chatAt("oc_a", 200)}))

	assert.Equal(t, "oc_a", m.pendingChat)
	assert.Zero(t, m.pendingSelect)
}

// The chat under a thread is often the open one, and the thread over it still
// has to be opened.
func TestHighlightedRow_AThreadOverTheOpenChatIsStillWorthOpening(t *testing.T) {
	m := cursorModel(t)
	m.chats = []store.Chat{chatAt("oc_0", 300)}
	m.threads = []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.chatIdx = 1

	r, ok := m.highlightedRow()

	require.True(t, ok)
	assert.Equal(t, "omt_a", r.key())
}

func TestHighlightedRow_AThreadAlreadyInTheRightColumnIsNot(t *testing.T) {
	m := cursorModel(t)
	m.chats = []store.Chat{chatAt("oc_0", 300)}
	m.threads = []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.chatIdx = 1
	m.rightKind, m.threadID = rightThread, "omt_a"

	_, ok := m.highlightedRow()

	assert.False(t, ok)
}

// The open came from the cursor, so the cursor stays: re-pinning would hand
// it straight back to the chat the thread happens in.
func TestOpenRow_LeavesTheCursorOnTheThreadRow(t *testing.T) {
	m := cursorModel(t)
	m.chats = []store.Chat{chatAt("oc_0", 300)}
	m.threads = []store.ThreadFeed{feedAt("omt_a", "oc_0", 200)}
	m.chatIdx = 1

	m.openRow(m.visibleRows()[1])

	assert.Equal(t, 1, m.chatIdx)
	assert.Equal(t, "omt_a", rowKeyAt(m.visibleRows(), m.chatIdx))
}

// A thread is drawn in its chat's own colours, picture and all, so the row
// takes the chat the listing already answered with rather than rebuilding one.
func TestListRows_AThreadTakesItsChatFromTheListing(t *testing.T) {
	c := chatAt("oc_1", 300)
	c.Name, c.ChatMode, c.AvatarPath = "平台组", "group", "resources/avatars/chats/oc_1.png"

	rows := listRows([]store.Chat{c}, []store.ThreadFeed{feedAt("omt_a", "oc_1", 200)})

	require.Len(t, rows, 2)
	assert.Equal(t, "平台组", rows[1].chat.Name)
	assert.NotEmpty(t, rows[1].chat.AvatarFile(), "the badge is the chat's own picture, not a drawn stand-in")
}

// A p2p thread's picture is the peer's, which only the listing's contact join
// knows about.
func TestListRows_AThreadOfTwoTakesThePeersPicture(t *testing.T) {
	c := chatAt("oc_1", 300)
	c.ChatMode, c.P2PTargetID, c.PeerAvatarPath = "p2p", "ou_a", "resources/avatars/users/ou_a.png"

	rows := listRows([]store.Chat{c}, []store.ThreadFeed{feedAt("omt_a", "oc_1", 200)})

	require.Len(t, rows, 2)
	assert.Equal(t, c.PeerAvatarPath, rows[1].chat.PeerAvatarPath)
	assert.Equal(t, "ou_a", rows[1].chat.AvatarSeed(), "and its colours are the peer's too")
}

// A thread in a chat past the end of the listing still draws and still opens.
func TestListRows_AThreadPastTheListingKeepsWhatItCarries(t *testing.T) {
	feed := feedAt("omt_a", "oc_far", 200)
	feed.ChatName = "财务组"

	rows := listRows([]store.Chat{chatAt("oc_1", 300)}, []store.ThreadFeed{feed})

	require.Len(t, rows, 2)
	assert.Equal(t, "oc_far", rows[1].chatID())
	assert.Equal(t, "财务组", rows[1].chat.Name)
}

func TestRowsCache_HoldsTheInterleaveUntilTheListIsReplaced(t *testing.T) {
	c := newRowsCache()
	chats := []store.Chat{chat("oc_1", "平台组"), chat("oc_2", "财务组")}

	first := c.all(chats, nil)
	require.Len(t, first, 2)
	require.Equal(t, &first[0], &c.all(chats, nil)[0], "the same lists give back the same interleave")

	replaced := append([]store.Chat{chat("oc_3", "项目协作群")}, chats...)
	again := c.all(replaced, nil)
	require.Len(t, again, 3, "a reload that brings a new list is interleaved again")
	require.Equal(t, "oc_3", again[0].chat.ChatID)
}

func TestRowsCache_AThreadArrivingRebuildsIt(t *testing.T) {
	c := newRowsCache()
	chats := []store.Chat{chat("oc_1", "平台组")}
	require.Len(t, c.all(chats, nil), 1)

	feed := store.ThreadFeed{ThreadID: "omt_x", ChatID: "oc_1", ChatName: "平台组",
		Root: store.Message{MessageID: "om_root", CreateMs: 10},
		Last: store.Message{MessageID: "om_last", CreateMs: 20}}
	require.Len(t, c.all(chats, []store.ThreadFeed{feed}), 2, "the thread takes a row of its own")
}

func TestRowsCache_EachFilterIsNarrowedOnce(t *testing.T) {
	c := newRowsCache()
	rows := c.all([]store.Chat{chat("oc_1", "平台组"), chat("oc_2", "财务组")}, nil)
	require.Len(t, rows, 2)

	calls := 0
	narrow := func(in []listRow) []listRow { calls++; return in[:1] }

	require.Len(t, c.narrowed("平台", narrow), 1)
	require.Len(t, c.narrowed("平台", narrow), 1)
	require.Equal(t, 1, calls, "the same filter is answered from the pass before")

	c.narrowed("财务", narrow)
	require.Equal(t, 2, calls, "a filter that moved is narrowed again")
}

func TestRowsCache_AFilterThatAnswersNothingIsStillAnAnswer(t *testing.T) {
	c := newRowsCache()
	c.all([]store.Chat{chat("oc_1", "平台组")}, nil)

	calls := 0
	narrow := func([]listRow) []listRow { calls++; return nil }

	require.Empty(t, c.narrowed("没有这个群", narrow))
	require.Empty(t, c.narrowed("没有这个群", narrow))
	require.Equal(t, 1, calls, "an empty answer is not a cache that never filled")
}
