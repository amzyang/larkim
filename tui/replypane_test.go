package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// theAnswered is a message the chat has answered, as the flow holds it.
func theAnswered() store.Message {
	return store.Message{MessageID: "om_1", ChatID: "oc_1", SenderName: "张三", Content: "1",
		RenderedAt: 1, CreateMs: msgAt(23, 19, 21), MessagePosition: 3}
}

// replyStyle renders a page whose messages all belong to om_1's tree.
func replyStyle(n int) msgStyle {
	st := baseStyle()
	st.replies = map[string]store.ReplyGist{
		"om_1": {Root: "om_1", Replies: n},
		"om_2": {Root: "om_1", Replies: n},
	}
	return st
}

func TestRenderRows_AnAnsweredMessageCountsItsWholeTree(t *testing.T) {
	out := rowText(renderRows([]store.Message{theAnswered()}, replyStyle(5)))

	require.Contains(t, out, " 5 replies")
	require.Contains(t, out, "1", "the body stays: a reply folds nothing away")
}

func TestRenderRows_TheReplyCountSitsUnderTheBody(t *testing.T) {
	// The client puts it under the bubble. It is a footer about the message,
	// not the head of a container standing in for hidden content.
	lines := strings.Split(strings.TrimRight(rowText(renderRows(
		[]store.Message{theAnswered()}, replyStyle(5))), "\n"), "\n")

	require.Equal(t, "1", strings.TrimSpace(lines[len(lines)-2]))
	require.Contains(t, lines[len(lines)-1], "5 replies")
}

func TestRenderRows_AReplyDrawsNoCountOfItsOwn(t *testing.T) {
	// Only the message the conversation started from carries the line; the
	// answers under it are ordinary messages of the flow.
	reply := store.Message{MessageID: "om_2", ChatID: "oc_1", SenderName: "李四", Content: "2",
		RenderedAt: 1, CreateMs: msgAt(23, 19, 22), MessagePosition: 4, ReplyTo: "om_1"}

	out := rowText(renderRows([]store.Message{reply}, replyStyle(5)))

	require.NotContains(t, out, "replies")
}

func TestRenderRows_TheReplyCountIsNotDrawnInsideItsOwnPane(t *testing.T) {
	st := replyStyle(5)
	st.inFrame = true

	out := rowText(renderRows([]store.Message{theAnswered()}, st))

	require.NotContains(t, out, "replies", "the answers stand right below the root there")
}

func TestRenderRows_AThreadRootDrawsItsThreadRatherThanItsReplies(t *testing.T) {
	// A thread's replies are read in the thread's own frame, and a chat is
	// either a topic group or it is not.
	x := theAnswered()
	x.ThreadID = "omt_1"
	st := replyStyle(5)
	st.threads = map[string]store.ThreadGist{"omt_1": {Replies: 2, SenderName: "李四",
		MsgType: "text", ContentRaw: `{"text":"收到"}`}}

	out := rowText(renderRows([]store.Message{x}, st))

	require.Contains(t, out, "⤷ 2 replies")
	require.NotContains(t, out, " 5 replies")
}

func TestRenderRows_AReplyCountCarriesAnOpenZone(t *testing.T) {
	rows := renderRows([]store.Message{theAnswered()}, replyStyle(5))

	zoned := 0
	for _, r := range rows {
		for _, z := range r.zones {
			if z.open == "" {
				continue
			}
			zoned++
			require.Equal(t, rightReply, z.openKind)
			require.Equal(t, "om_1", z.open)
		}
	}
	require.Equal(t, 1, zoned)
}

// onReplies is a model reading a chat whose page holds a reply tree.
func onReplies(t *testing.T) Model {
	t.Helper()
	m := sized(140, 36)
	m.msgsBase = []store.Message{
		theAnswered(),
		{MessageID: "om_2", ChatID: "oc_1", SenderName: "李四", Content: "2", RenderedAt: 1,
			CreateMs: msgAt(23, 19, 22), MessagePosition: 4, ReplyTo: "om_1"},
	}
	m.meta.replies = map[string]store.ReplyGist{
		"om_1": {Root: "om_1", Replies: 5},
		"om_2": {Root: "om_1", Replies: 5},
	}
	m.applyOutbox()
	m.focus, m.msgIdx = paneMessages, 0
	m.layout()
	return m
}

func TestToggleRight_OpensTheDetailsOfTheTreeUnderTheCursor(t *testing.T) {
	m := onReplies(t)

	got, _ := m.toggleRight()
	m = got.(Model)

	require.Equal(t, rightReply, m.rightKind)
	require.Equal(t, "om_1", m.threadID)
	require.Equal(t, "1", m.rightName, "the frame is titled by the message it opens on")
}

func TestToggleRight_OpensTheDetailsFromAReplyToo(t *testing.T) {
	// The message a conversation started from is often further up than the
	// page reaches, and the reader asking about the one in front of them
	// means the same conversation either way.
	m := onReplies(t)
	m.msgIdx = 1

	got, _ := m.toggleRight()
	m = got.(Model)

	require.Equal(t, rightReply, m.rightKind)
	require.Equal(t, "om_1", m.threadID)
}

func TestActivate_EnterOnAnAnsweredMessageStillAnswersIt(t *testing.T) {
	// A reply lands in the flow, so Enter keeps its meaning here — unlike a
	// thread root, which is answered inside the thread it opens.
	m := onReplies(t)

	got, _ := m.activate()
	m = got.(Model)

	require.Equal(t, modeInsert, m.mode)
	require.NotNil(t, m.replyTo)
	require.Equal(t, "om_1", m.replyTo.MessageID)
	require.Equal(t, rightNone, m.rightKind, "Enter opened no column")
}

func TestOnReplyLoaded_FillsTheFrameAndNamesItAfterTheRoot(t *testing.T) {
	m := onReplies(t)
	got, _ := m.toggleRight()
	m = got.(Model)

	tree := []store.Message{theAnswered(),
		{MessageID: "om_2", ChatID: "oc_1", SenderName: "李四", Content: "2", RenderedAt: 1,
			CreateMs: msgAt(23, 19, 22), MessagePosition: 4, ReplyTo: "om_1"}}
	got, _ = m.onReplyLoaded(replyLoadedMsg{root: "om_1", msgs: tree})
	m = got.(Model)

	require.Len(t, m.thread, 2)
	require.Equal(t, "1", m.rightName)
	require.Contains(t, rowText(m.threadRows), "2")
}

func TestOnReplyLoaded_IgnoresATreeTheColumnHasMovedOnFrom(t *testing.T) {
	m := onReplies(t)
	got, _ := m.toggleRight()
	m = got.(Model)

	got, _ = m.onReplyLoaded(replyLoadedMsg{root: "om_elsewhere", msgs: []store.Message{theAnswered()}})

	require.Empty(t, got.(Model).thread)
}

func TestActivate_EnterInsideTheDetailsPaneRepliesInTheMainFlow(t *testing.T) {
	// The rows there are the chat's own messages, so an answer belongs beside
	// them; only a thread's replies go inside a thread.
	m := onReplies(t)
	got, _ := m.toggleRight()
	m = got.(Model)
	got, _ = m.onReplyLoaded(replyLoadedMsg{root: "om_1", msgs: []store.Message{theAnswered()}})
	m = got.(Model)
	m.focus, m.threadIdx = paneThread, 0

	got, _ = m.activate()
	m = got.(Model)

	require.Equal(t, modeInsert, m.mode)
	require.False(t, m.inThrd)
}

func TestRightTitle_NamesTheDetailsPane(t *testing.T) {
	m := onReplies(t)
	got, _ := m.toggleRight()
	m = got.(Model)

	require.Contains(t, ansi.Strip(m.rightTitle(60)), "Details 1")
}
