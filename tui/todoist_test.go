package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/todoist"
	"github.com/stretchr/testify/require"
)

// fakeTasks stands in for the endpoint: it records what it was handed and
// answers a task back, or the failure it was loaded with.
type fakeTasks struct {
	got []todoist.Task
	err error
}

func (f *fakeTasks) CreateTask(_ context.Context, t todoist.Task) (todoist.Task, error) {
	f.got = append(f.got, t)
	return todoist.Task{ID: "6Xv", Content: t.Content}, f.err
}

// taskPage stands a message pane up with the T key armed by f.
func taskPage(t *testing.T, f *fakeTasks, msg store.Message) Model {
	t.Helper()
	m := New(Deps{Self: "ou_me", Todoist: f})
	m.width, m.height = 120, 36
	m.chatID = msg.ChatID
	m.msgsBase = []store.Message{msg}
	m.applyOutbox()
	m.layout()
	m.focus, m.msgIdx = paneMessages, 0
	m.rebuildMessages()
	return m
}

func fileTask(msg store.Message) store.Message {
	msg.MessageID, msg.ChatID, msg.MessagePosition = "om_1", "oc_a", 227
	return msg
}

func TestTodoistTask_FilesTheSelectedMessage(t *testing.T) {
	f := &fakeTasks{}
	m := taskPage(t, f, fileTask(store.Message{MsgType: "post",
		Content: "发布单合了吗\n还没", RenderedAt: 1}))

	out, cmd := m.onNormalKey("T")

	msg := cmd()
	require.IsType(t, noticeMsg{}, msg)
	require.Equal(t, noticeMsg{"todoist: 发布单合了吗"}, msg)
	require.Equal(t, paneMessages, out.(Model).focus, "the key files, it does not move")
	require.Equal(t, []todoist.Task{{
		Content:     "发布单合了吗",
		Description: applink.ChatLink("oc_a", "om_1", 227),
	}}, f.got)
}

func TestTodoistTask_TakesTheFirstLineAndNamesAttachments(t *testing.T) {
	f := &fakeTasks{}
	m := taskPage(t, f, fileTask(store.Message{MsgType: "post",
		Content: "![图](img_v3_abc) 先看这个\n后面还有", RenderedAt: 1}))

	_, cmd := m.onNormalKey("T")
	require.NotNil(t, cmd())

	// The title is the first line only, with the image named the way the
	// chat list names it; a task titled by a post's later paragraphs says
	// nothing.
	require.Equal(t, "[Image] 先看这个", f.got[0].Content)
}

func TestTodoistTask_CutsALongFirstLine(t *testing.T) {
	f := &fakeTasks{}
	m := taskPage(t, f, fileTask(store.Message{MsgType: "text",
		Content: strings.Repeat("很", 200), RenderedAt: 1}))

	_, cmd := m.onNormalKey("T")
	require.NotNil(t, cmd())

	content := f.got[0].Content
	// truncate cuts by display width, so a CJK line stops at half its runes.
	require.LessOrEqual(t, lipgloss.Width(content), taskContentMax)
	require.True(t, strings.HasSuffix(content, "…"))
}

func TestTodoistTask_AThreadReplyLinksTheChatWithoutAPosition(t *testing.T) {
	f := &fakeTasks{}
	msg := fileTask(store.Message{MsgType: "text", Content: "线程里的一句", RenderedAt: 1})
	msg.MessagePosition = -3
	m := taskPage(t, f, msg)

	_, cmd := m.onNormalKey("T")
	require.NotNil(t, cmd())

	require.Equal(t,
		"lark://applink.feishu.cn/client/chat/open?openChatId=oc_a&messageId=om_1",
		f.got[0].Description)
}

func TestTodoistTask_FromTheChatListFilesTheChat(t *testing.T) {
	f := &fakeTasks{}
	m := sized(120, 36)
	m.deps.Todoist = f
	m.focus, m.chatIdx = paneChats, rowOf(0)

	_, cmd := m.onNormalKey("T")
	require.NotNil(t, cmd())

	require.Equal(t, []todoist.Task{{
		Content:     truncate(flatten(m.chats[0].Name), taskContentMax),
		Description: applink.ChatLink("oc_0", "", 0),
	}}, f.got)
}

func TestTodoistTask_FromTheUnreadRowHasNoChatToName(t *testing.T) {
	f := &fakeTasks{}
	m := sized(120, 36)
	m.deps.Todoist = f
	m.focus, m.chatIdx = paneChats, 0
	require.True(t, m.visibleRows()[m.chatIdx].isFeed(), "the row above every chat")

	out, cmd := m.onNormalKey("T")

	require.Nil(t, cmd)
	require.Empty(t, f.got)
	require.Equal(t, "no chat under the cursor", out.(Model).notice)
}

func TestTodoistTask_WithoutATokenSaysSo(t *testing.T) {
	m := sized(120, 36)
	m.deps.Todoist = nil
	m.focus = paneMessages

	out, cmd := m.onNormalKey("T")

	require.Nil(t, cmd)
	require.Equal(t, "todoist not configured", out.(Model).notice)
}

func TestTodoistTask_WithNothingUnderTheCursorSaysSo(t *testing.T) {
	m := sized(120, 36)
	m.deps.Todoist = &fakeTasks{}
	m.focus, m.msgIdx = paneMessages, 0
	m.msgs, m.msgsBase = nil, nil

	out, cmd := m.onNormalKey("T")

	require.Nil(t, cmd)
	require.Equal(t, "no message under the cursor", out.(Model).notice)
}

func TestTodoistTask_AFailureReachesTheNoticeBar(t *testing.T) {
	boom := errors.New("todoist: 401 Unauthorized: bad token")
	f := &fakeTasks{err: boom}
	m := taskPage(t, f, fileTask(store.Message{MsgType: "text",
		Content: "发我一下", RenderedAt: 1}))

	_, cmd := m.onNormalKey("T")
	require.Equal(t, errMsg{boom}, cmd())
}
