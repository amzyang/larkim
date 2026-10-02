package tui

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// onThread is a model with a thread open in the right column, its list
// already landed, and the focus in that column.
func onThread(t *testing.T) Model {
	t.Helper()
	m := sized(140, 36)
	m.rightKind, m.threadID = rightThread, "omt_1"
	m.thread = []store.Message{
		{MessageID: "om_root", SenderName: "张三", Content: "根", RenderedAt: 1, CreateMs: 1},
		{MessageID: "om_fwd", SenderName: "李四", MsgType: "merge_forward", CreateMs: 2},
	}
	m.focus = paneThread
	m.layout()
	return m
}

func TestPushRight_AForwardOpenedInsideAThreadKeepsTheThreadUnderIt(t *testing.T) {
	m := onThread(t)

	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	require.Equal(t, rightForward, m.rightKind)
	require.Equal(t, "om_fwd", m.threadID)
	require.Len(t, m.rightStack, 1, "the thread is covered, not closed")
	require.Equal(t, rightThread, m.rightStack[0].kind)
	require.Equal(t, "omt_1", m.rightStack[0].id)
	require.Equal(t, paneThread, m.focus, "the column takes the focus it was opened from")
}

func TestOpenRight_AThreadOpenedFromTheChatPaneReplacesTheColumn(t *testing.T) {
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})
	require.Len(t, m.rightStack, 1)

	// A container reached from the message pane is a sibling of what stands
	// in the column, not something it holds.
	m, _ = m.openRight(rightFrame{kind: rightThread, id: "omt_2"})

	require.Equal(t, "omt_2", m.threadID)
	require.Empty(t, m.rightStack, "stacking siblings would turn Esc into a visit history")
}

func TestPushRight_PressingTheSameSummaryTwiceStacksOneFrame(t *testing.T) {
	m := onThread(t)
	f := rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"}

	m, _ = m.pushRight(f)
	m, cmd := m.pushRight(f)

	require.Len(t, m.rightStack, 1, "a second press asks for the place it is already at")
	require.Nil(t, cmd, "and costs no reload")
}

func TestPopRight_EscUncoversTheFrameBeneath(t *testing.T) {
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	m, cmd := m.popRight()

	require.Equal(t, rightThread, m.rightKind)
	require.Equal(t, "omt_1", m.threadID)
	require.Empty(t, m.rightStack)
	require.NotNil(t, cmd, "an uncovered frame reloads: a sync tick may have moved it")
}

func TestPopRight_ASuspendedFrameComesBackOnItsOwnCursor(t *testing.T) {
	m := onThread(t)
	m.threadIdx = 1
	was := m.thread

	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})
	m, _ = m.popRight()
	require.Equal(t, "om_fwd", m.rightPin.sel, "the frame is held by what the cursor was on, not by its index")

	// The list comes back from the store, renumbered: a row dropped above the
	// cursor would move it if the pin were an index.
	next, _ := m.Update(threadLoadedMsg{threadID: "omt_1", msgs: was[1:]})
	m = next.(Model)

	require.Equal(t, 0, m.threadIdx)
	require.Equal(t, "om_fwd", idAt(m.thread, m.threadIdx))
	require.Zero(t, m.rightPin.id, "the pin is spent on the first list to land under it")
}

func TestPopRight_EscOnTheLastFrameClosesTheColumn(t *testing.T) {
	m := onThread(t)

	m, _ = m.popRight()

	require.False(t, m.threadOpen())
	require.Equal(t, paneMessages, m.focus, "the reader is put back where the column stood")
}

func TestPopRight_HUncoversTheFrameBeneath(t *testing.T) {
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	next, cmd := m.onNormalKey("h")
	m = next.(Model)

	require.Equal(t, rightThread, m.rightKind)
	require.Equal(t, "omt_1", m.threadID)
	require.Empty(t, m.rightStack)
	require.Equal(t, paneThread, m.focus, "h steps out of the frame, not out of the column")
	require.NotNil(t, cmd, "an uncovered frame reloads: a sync tick may have moved it")
}

func TestH_OnTheLastFrameLeavesTheColumnStanding(t *testing.T) {
	m := onThread(t)

	next, _ := m.onNormalKey("h")
	m = next.(Model)

	require.True(t, m.threadOpen(), "closing the column is Esc's rung, not h's")
	require.Equal(t, paneMessages, m.focus)
}

func TestH_OutsideTheRightPaneLeavesTheStackAlone(t *testing.T) {
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})
	m.focus = paneMessages

	next, _ := m.onNormalKey("h")

	require.Len(t, next.(Model).rightStack, 1, "h outside the column is the pane key it has always been")
	require.Equal(t, paneChats, next.(Model).focus)
}

func TestEsc_OutsideTheRightPaneLeavesTheStackAlone(t *testing.T) {
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})
	m.focus = paneMessages

	next, _ := m.onNormalKey("esc")

	require.Len(t, next.(Model).rightStack, 1, "Esc in the message pane answers the quote and the filter")
	require.True(t, next.(Model).threadOpen())
}

func TestToggleInfo_ResetsTheStackToOneFrame(t *testing.T) {
	m, _ := infoModel(t)
	m.rightKind, m.threadID = rightThread, "omt_1"
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	next, _ := m.toggleInfo()
	m = next.(Model)

	require.True(t, m.infoOpen)
	require.False(t, m.threadOpen(), "the card speaks of the whole chat, not of anything in the column")
	require.Empty(t, m.rightStack)
}

func TestOpenThreadID_NamesTheThreadUnderAnOpenForward(t *testing.T) {
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	require.Equal(t, "omt_1", m.openThreadID(),
		"the thread underneath still has replies to fetch while a forward covers it")
}

func TestFocusMessages_OnAFoldedLayoutEmptiesTheWholeStack(t *testing.T) {
	m := onThread(t)
	m.width = chatsWidth + minMessagesWidth + threadWidth - 1
	m.layout()
	require.True(t, m.foldRight())
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	m, _ = m.focusMessages()

	require.False(t, m.threadOpen(), "the reader is leaving the column, not stepping back through it")
	require.Empty(t, m.rightStack)
}

func TestToggleRight_OnTheOpenContainerClosesTheColumn(t *testing.T) {
	m := onThread(t)
	m.focus, m.msgIdx = paneMessages, 0
	m.msgsBase = []store.Message{{MessageID: "om_root", ThreadID: "omt_1", Content: "根", RenderedAt: 1}}
	m.applyOutbox()

	next, _ := m.toggleRight()

	require.False(t, next.(Model).threadOpen())
}

func TestContainerOf_AThreadWinsOverAForward(t *testing.T) {
	// A forward somebody started a topic on is read in the topic, where the
	// forward is one row that opens in turn.
	kind, id, _ := containerOf(store.Message{MessageID: "om_fwd", MsgType: "merge_forward", ThreadID: "omt_1"}, "")
	require.Equal(t, rightThread, kind)
	require.Equal(t, "omt_1", id)

	kind, id, root := containerOf(store.Message{MessageID: "om_fwd", MsgType: "merge_forward"}, "")
	require.Equal(t, rightForward, kind)
	require.Equal(t, "om_fwd", id)
	require.Equal(t, "om_fwd", root, "a bundle opened from a chat is its own root")

	_, _, root = containerOf(store.Message{MessageID: "om_inner", MsgType: "merge_forward"}, "om_outer")
	require.Equal(t, "om_outer", root, "a nested one keeps the tree its rows are stored under")
}

func TestJumpTo_AFoldedReplyIsReachedThroughItsThread(t *testing.T) {
	reply := store.Message{MessageID: "om_r", ThreadID: "omt_1", MessagePosition: -3}
	require.Equal(t, pendingJump{id: "om_r", thread: "omt_1", takeFocus: true}, jumpTo(reply))

	// A root, and a bundle: both stand on the page itself.
	require.Equal(t, pendingJump{id: "om_root"},
		jumpTo(store.Message{MessageID: "om_root", ThreadID: "omt_1", MessagePosition: 3}))
	require.Equal(t, pendingJump{id: "om_fwd"},
		jumpTo(store.Message{MessageID: "om_fwd", MsgType: "merge_forward"}))
}

func TestMessagesLoaded_AThreadReplyLandsInsideItsThreadFrame(t *testing.T) {
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := sized(140, 36)
	m.deps = Deps{Store: st, Self: "ou_me"}
	m.chatID, m.pendingChat = "oc_1", "oc_1"
	// The hit is the reply, so what the reader is handed is the reply — and
	// the page it landed on does not carry one.
	m.pendingSelect = pendingJump{id: "om_r", thread: "omt_1", takeFocus: true}

	next, cmd := m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: m.msgsBase})
	m = next.(Model)

	require.Equal(t, rightThread, m.rightKind)
	require.Equal(t, "omt_1", m.threadID)
	require.Equal(t, "om_r", m.rightPin.sel, "the cursor is bound for the reply, inside the frame")
	require.Equal(t, paneThread, m.focus, "the reader asked to be at the reply")
	require.Contains(t, collect(cmd), "tui.threadLoadedMsg")
}

// A thread row of the chats list opens the same frame without the focus: the
// reader is walking the list, and a row that pulled them into the column would
// cost them the next j.
func TestMessagesLoaded_AThreadRowOpensTheColumnWithoutTakingTheFocus(t *testing.T) {
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := sized(140, 36)
	m.deps = Deps{Store: st, Self: "ou_me"}
	m.chatID, m.pendingChat = "oc_1", "oc_1"
	m.focus = paneChats
	m.pendingSelect = pendingJump{id: "om_r", thread: "omt_1"}

	next, _ := m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: m.msgsBase})
	m = next.(Model)

	require.Equal(t, rightThread, m.rightKind)
	require.Equal(t, "omt_1", m.threadID)
	require.Equal(t, paneChats, m.focus)
}

func TestMessagesLoaded_AHitOnThePageItselfLeavesTheColumnAlone(t *testing.T) {
	m := sized(140, 36)
	m.chatID, m.pendingChat = "oc_1", "oc_1"
	m.pendingSelect = pendingJump{id: "om_5"}

	next, _ := m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: m.msgsBase})
	m = next.(Model)

	require.False(t, m.threadOpen(), "a hit in the chat's own flow is found on the page")
	require.Equal(t, "om_5", idAt(m.msgs, m.msgIdx))
}

func TestOpenerSel_AContainerOpenedFromItsRootNamesNoMessage(t *testing.T) {
	root := store.Message{MessageID: "om_root", ThreadID: "omt_1", MessagePosition: 7}
	reply := store.Message{MessageID: "om_r", ThreadID: "omt_1", MessagePosition: -3}

	require.Empty(t, openerSel(rightThread, "omt_1", root),
		"a root asks for the conversation, not for the message already in front of the reader")
	require.Equal(t, "om_r", openerSel(rightThread, "omt_1", reply))

	require.Empty(t, openerSel(rightReply, "om_root", root))
	require.Equal(t, "om_mid", openerSel(rightReply, "om_root",
		store.Message{MessageID: "om_mid"}), "any member of the tree is a place to stand")

	require.Empty(t, openerSel(rightForward, "om_fwd",
		store.Message{MessageID: "om_fwd", MsgType: "merge_forward"}),
		"a bundle's card is not one of the rows it opens")
}

func TestDetailsAtCursor_PointsAtTheMessageInFrontOfTheReader(t *testing.T) {
	m := sized(140, 36)
	m.focus, m.msgIdx = paneMessages, 1
	m.msgsBase = []store.Message{
		{MessageID: "om_root", Content: "问", RenderedAt: 1},
		{MessageID: "om_mid", ReplyTo: "om_root", Content: "答", RenderedAt: 1},
	}
	m.applyOutbox()
	m.meta.replies = map[string]store.ReplyGist{
		"om_root": {Root: "om_root", Replies: 2},
		"om_mid":  {Root: "om_root", Replies: 2},
	}

	f, ok := m.detailsAtCursor()
	require.True(t, ok)
	require.Equal(t, "om_root", f.id)
	require.Equal(t, "om_mid", f.sel)

	m.msgIdx = 0
	f, ok = m.detailsAtCursor()
	require.True(t, ok)
	require.Empty(t, f.sel, "opened from the root, the tree lands where its kind says")
}

func TestContainerAtCursor_AThreadOpenedFromAReplyPointsAtIt(t *testing.T) {
	m := sized(140, 36)
	m.focus, m.msgIdx = paneMessages, 0
	m.msgsBase = []store.Message{{MessageID: "om_r", ThreadID: "omt_1", MessagePosition: -3, RenderedAt: 1}}
	m.applyOutbox()

	f, ok := m.containerAtCursor()

	require.True(t, ok)
	require.Equal(t, rightThread, f.kind)
	require.Equal(t, "om_r", f.sel)
}

// threadList is a thread's list: a root and n replies, each one row tall.
func threadList(n int) []store.Message {
	msgs := []store.Message{{MessageID: "om_root", SenderName: "张三", Content: "根", RenderedAt: 1, CreateMs: 1}}
	for i := range n {
		msgs = append(msgs, store.Message{MessageID: fmt.Sprintf("om_r%d", i), SenderName: "李四",
			Content: fmt.Sprintf("回复 %d", i), RenderedAt: 1, CreateMs: int64(2 + i),
			ThreadID: "omt_1", MessagePosition: -3})
	}
	return msgs
}

func TestThreadLoaded_AFrameOpenedFromItsRootLandsOnTheNewestReply(t *testing.T) {
	m := sized(140, 36)
	m, _ = m.openRight(rightFrame{kind: rightThread, id: "omt_1"})
	msgs := threadList(40)

	next, _ := m.Update(threadLoadedMsg{threadID: "omt_1", msgs: msgs})
	m = next.(Model)

	require.Equal(t, len(msgs)-1, m.threadIdx, "a thread is opened to see what has been said since")
	require.True(t, atTail(m.threadRows, m.threadTop, m.listHeight()))
}

func TestThreadLoaded_TheMessageTheFrameWasOpenedOnIsCentred(t *testing.T) {
	m := sized(140, 36)
	m, _ = m.openRight(rightFrame{kind: rightThread, id: "omt_1", sel: "om_r20"})
	msgs := threadList(40)

	next, _ := m.Update(threadLoadedMsg{threadID: "omt_1", msgs: msgs})
	m = next.(Model)

	require.Equal(t, "om_r20", idAt(m.thread, m.threadIdx))
	require.Equal(t, centerTo(m.threadRows, m.threadIdx, m.listHeight()), m.threadTop)
	require.Less(t, m.threadTop, firstRow(m.threadRows, m.threadIdx),
		"the answers under it are the half the reader opened the pane for")
	require.Positive(t, m.threadTop, "and the context above it is on screen too")
}

func TestThreadLoaded_AReloadUnderTheReadersHandMovesNothing(t *testing.T) {
	m := sized(140, 36)
	m, _ = m.openRight(rightFrame{kind: rightThread, id: "omt_1"})
	msgs := threadList(40)
	next, _ := m.Update(threadLoadedMsg{threadID: "omt_1", msgs: msgs})
	m = next.(Model)
	m.threadIdx, m.threadTop = 5, 4

	next, _ = m.Update(threadLoadedMsg{threadID: "omt_1", msgs: msgs})
	m = next.(Model)

	require.Equal(t, 5, m.threadIdx)
	require.Equal(t, 4, m.threadTop)
}

func TestForwardLoaded_ABundleOpensAtItsFirstMessage(t *testing.T) {
	m := sized(140, 36)
	m, _ = m.openRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})
	var msgs []store.Message
	for i := range 40 {
		msgs = append(msgs, store.Message{MessageID: fmt.Sprintf("om_c%d", i), SenderName: "王五",
			Content: fmt.Sprintf("第 %d 条", i), RenderedAt: 1, CreateMs: int64(i + 1)})
	}

	next, _ := m.Update(forwardLoadedMsg{bundleID: "om_fwd", level: "om_fwd", msgs: msgs,
		gist: store.ForwardGist{Expanded: true}})
	m = next.(Model)

	require.Equal(t, 0, m.threadIdx, "a bundle is somebody else's conversation, read from its start")
	require.Equal(t, 0, m.threadTop)
}

func TestForwardLoaded_AnUnexpandedLevelKeepsItsPinForTheReload(t *testing.T) {
	m := sized(140, 36)
	m, _ = m.openRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})

	// Feishu has not been asked for the children yet, so the first list is
	// empty and says nothing about where the frame belongs.
	next, _ := m.Update(forwardLoadedMsg{bundleID: "om_fwd", level: "om_fwd"})
	m = next.(Model)
	require.Equal(t, "om_fwd", m.rightPin.id, "an empty list is not an answer")

	var msgs []store.Message
	for i := range 40 {
		msgs = append(msgs, store.Message{MessageID: fmt.Sprintf("om_c%d", i), SenderName: "王五",
			Content: fmt.Sprintf("第 %d 条", i), RenderedAt: 1, CreateMs: int64(i + 1)})
	}
	next, _ = m.Update(forwardLoadedMsg{bundleID: "om_fwd", level: "om_fwd", msgs: msgs,
		gist: store.ForwardGist{Expanded: true}})
	m = next.(Model)

	require.Equal(t, 0, m.threadIdx)
	require.Equal(t, 0, m.threadTop)
}
