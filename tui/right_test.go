package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
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

	m = m.focusMessages()

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
