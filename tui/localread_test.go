package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// readModel is a model over a real store holding one chat with one message
// Feishu still reports as unread.
func readModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_a", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_a", ChatID: "oc_a", MsgType: "text",
		SenderID: "ou_x", SenderName: "孙琪", ContentRaw: `{"text":"在吗"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_a", &unread, 100, 0))

	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 120, 36
	return m, st
}

// arrive plays one page of a chat into the model the way the load command
// does, running whatever the model asks for in return. It goes through Update
// rather than the dispatcher under it, because that outer pass is where a page
// landing in front of the reader is taken as read.
func arrive(t *testing.T, m Model, st *store.Store, chatID string) Model {
	t.Helper()
	msg, ok := loadMessages(Deps{Store: st}, chatID, 0, messagePageSize)().(messagesLoadedMsg)
	require.True(t, ok)
	next, cmd := m.Update(msg)
	// The applinks a landing page queues are walked here rather than left
	// hanging: a test asserting on the opener needs the whole chain run.
	return drain(t, next.(Model), cmd)
}

func TestUpdate_OpeningAChatClearsItsBadge(t *testing.T) {
	m, st := readModel(t)
	m.pendingChat = "oc_a"

	m = arrive(t, m, st, "oc_a")

	chats, err := st.ListChats(context.Background(), store.ChatQuery{})
	require.NoError(t, err)
	for _, c := range chats {
		require.Zero(t, c.UnreadCount, "the chat the reader is looking at carries no badge")
	}
	require.False(t, m.dots["om_a"], "the block the visit opens on is read as it is drawn")
}

func TestUpdate_TheUnreadMarkerOutlivesTheReadItCaused(t *testing.T) {
	m, st := readModel(t)
	// The visit opens on the newest block and reads it, so the marker under
	// test is the one above it.
	lands(t, st, "om_b", "ou_b", "李四", "在的", 300)
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")

	// Clearing the badge advances data_rev, so the pane reloads at once and
	// the page comes back with the message already read.
	m = arrive(t, m, st, "oc_a")

	require.NotZero(t, m.msgs[0].LocalReadAt, "the reload carries the write that opening made")
	require.True(t, m.dots["om_a"])
	require.Contains(t, rowText(renderRows(m.msgs, m.msgStyleFor(60, m.meta))), "●",
		"the marker must not go out under the reader's eyes")
}

func TestUpdate_TheUnreadMarkerIsGoneOnTheNextVisit(t *testing.T) {
	m, st := readModel(t)
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")

	m.pendingChat = "oc_a"
	m.enterChat()
	m = arrive(t, m, st, "oc_a")

	require.Empty(t, m.dots, "a chat already read has nothing waiting")
	require.False(t, strings.Contains(rowText(renderRows(m.msgs, m.msgStyleFor(60, m.meta))), "●"))
}

func TestUpdate_SearchHitsKeepTheirUnreadMarker(t *testing.T) {
	m, st := readModel(t)
	require.NoError(t, st.UpdateRendered(context.Background(), "om_a", "在吗", "", "", 1))

	msg, ok := localSearch(Deps{Store: st}, nil, "在吗", 1)().(searchMsg)
	require.True(t, ok)
	require.Len(t, msg.hits, 1)
	m.searching, m.searchGen = true, 1
	next, _ := m.update(msg)
	m = next.(Model)

	require.True(t, m.dots["om_a"], "a hit Feishu still reports unread carries a marker")
	require.Contains(t, rowText(m.msgRows), "●")
}

func TestUpdate_OpeningTheChatLeavesAThreadReplyUnreadUntilTheThreadIsOpened(t *testing.T) {
	m, st := readModel(t)
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_root", ChatID: "oc_a", MsgType: "text", SenderID: "ou_x", SenderName: "张三",
			ContentRaw: `{"text":"一个老话题"}`, CreateMs: 150, UpdateMs: 150, MessagePosition: 2, ThreadID: "omt_1"},
		{MessageID: "om_reply", ChatID: "oc_a", MsgType: "text", SenderID: "ou_x", SenderName: "张三",
			ContentRaw: `{"text":"接着上面那个话题"}`, CreateMs: 200, UpdateMs: 200, MessagePosition: -3, ThreadID: "omt_1"},
	}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_reply", &unread, 200, 0))

	m.pendingChat = "oc_a"
	m.enterChat()
	m = arrive(t, m, st, "oc_a")

	require.NotContains(t, idsOf(m.msgs), "om_reply", "the page folds a reply into its root's line")
	reply, err := st.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.Zero(t, reply.LocalReadAt, "a visit cannot read what it never showed")

	// Opening the thread is what puts them in front of the reader, and a
	// thread is one screenful: having it on top is having read it.
	m, cmd := m.openRight(rightFrame{kind: rightThread, id: "omt_1"})
	collect(cmd)
	next, cmd := m.Update(loadThread(Deps{Store: st}, "omt_1")().(threadLoadedMsg))
	collect(cmd)

	reply, err = st.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.NotZero(t, reply.LocalReadAt)
	require.Contains(t, idsOf(next.(Model).thread), "om_reply")
}

func idsOf(msgs []store.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, x := range msgs {
		out = append(out, x.MessageID)
	}
	return out
}

// lands writes a message into the chat the way sync does, Feishu still
// reporting it unread.
func lands(t *testing.T, st *store.Store, id, sender, name, text string, ms int64) {
	t.Helper()
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: id, ChatID: "oc_a", MsgType: "text",
		SenderID: sender, SenderName: name, ContentRaw: `{"text":"` + text + `"}`, CreateMs: ms, UpdateMs: ms}}, ms)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, id, &unread, ms, 0))
}

// watching opens a chat with the message pane focused, which is where the
// cursor keys act.
func watching(t *testing.T, m Model, st *store.Store) Model {
	t.Helper()
	m.focus, m.pendingChat = paneMessages, "oc_a"
	return arrive(t, m, st, "oc_a")
}

func TestUpdate_AMessageLandingInTheWatchedChatWearsNoMarker(t *testing.T) {
	m, st := readModel(t)
	lands(t, st, "om_b", "ou_b", "李四", "在的", 300)
	m = watching(t, m, st)
	require.True(t, m.dots["om_a"], "the block above the one the visit opened on still waits")

	lands(t, st, "om_c", "ou_c", "王五", "收到", 500)
	m = arrive(t, m, st, "oc_a")

	require.False(t, m.dots["om_c"], "what landed under the reader's eyes was read as it landed")
	require.True(t, m.dots["om_a"], "what was waiting when the visit began keeps its marker")
	require.Equal(t, 1, strings.Count(rowText(m.msgRows), "●"))
}

func TestUpdate_AMessageLandingWhileAwayKeepsItsMarkerOnTheReturn(t *testing.T) {
	m, st := readModel(t)
	m = watching(t, m, st)

	away, _ := m.update(tea.BlurMsg{})
	m = away.(Model)
	lands(t, st, "om_b", "ou_b", "李四", "改到下午", 300)
	m = arrive(t, m, st, "oc_a")
	require.True(t, m.dots["om_b"], "what landed while the reader was away says so")

	back, _ := m.update(tea.FocusMsg{})
	m = back.(Model)

	require.True(t, m.dots["om_b"], "coming back does not erase what was missed")
	require.Contains(t, rowText(m.msgRows), "●")
}

func TestUpdate_AJumpClearsTheMarkerOfTheHitItLandsOn(t *testing.T) {
	m, st := readModel(t)
	lands(t, st, "om_b", "ou_b", "李四", "在的", 300)
	m = watching(t, m, st)
	require.True(t, m.dots["om_a"], "the visit opened on om_b's block, so om_a still waits")

	// A hit inside the chat already open: the page comes back with the cursor
	// asked for it rather than for the newest message.
	m.pendingSelect = pendingJump{id: "om_a"}
	m = arrive(t, m, st, "oc_a")

	require.False(t, m.dots["om_a"], "the hit the jump landed on is read as it is drawn")
}

func TestMove_TheCursorClearsTheMarkerOfTheBlockItLandsOn(t *testing.T) {
	m, st := readModel(t)
	lands(t, st, "om_b", "ou_b", "李四", "在的", 300)
	lands(t, st, "om_c", "ou_c", "王五", "我来看看", 500)
	m = watching(t, m, st)
	require.True(t, m.dots["om_a"] && m.dots["om_b"], "the two blocks above the landing one still wait")

	out, _ := m.move(-1)
	m = out.(Model)

	require.False(t, m.dots["om_b"], "the block the cursor landed on lost its marker")
	require.True(t, m.dots["om_a"], "the block it has not reached keeps its own")
	require.Equal(t, 1, strings.Count(rowText(m.msgRows), "●"))
}

func TestMove_ClearingAMarkerTakesEveryMessageUnderTheSenderLine(t *testing.T) {
	m, st := readModel(t)
	lands(t, st, "om_b", "ou_b", "李四", "在的", 300)
	lands(t, st, "om_c", "ou_b", "李四", "马上处理", 400)
	lands(t, st, "om_d", "ou_c", "王五", "辛苦", 600)
	m = watching(t, m, st)

	// The cursor steps back off the block the visit opened on and onto the
	// second message of the one above, which hides under om_b's sender line.
	out, _ := m.move(-1)
	m = out.(Model)

	require.False(t, m.dots["om_c"], "the message the cursor landed on lost its marker")
	require.False(t, m.dots["om_b"], "a half-cleared block would open a second sender line")
	require.True(t, m.dots["om_a"])
	require.Equal(t, 1, strings.Count(rowText(m.msgRows), "●"))
}

func TestMove_SearchHitsKeepTheirMarker(t *testing.T) {
	m, st := readModel(t)
	require.NoError(t, st.UpdateRendered(context.Background(), "om_a", "在吗", "", "", 1))
	msg, ok := localSearch(Deps{Store: st}, nil, "在吗", 1)().(searchMsg)
	require.True(t, ok)
	m.searching, m.searchGen, m.focus = true, 1, paneMessages
	next, _ := m.update(msg)
	m = next.(Model)
	require.True(t, m.dots["om_a"])

	out, _ := m.move(1)
	m = out.(Model)

	require.True(t, m.dots["om_a"], "a hit the reader has not opened is still waiting")
	require.Contains(t, rowText(m.msgRows), "●")
}

func TestThreadLoaded_LightsAMarkerForAReplyTheChatPaneNeverShowed(t *testing.T) {
	m, st := readModel(t)
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_root", ChatID: "oc_a", MsgType: "text", SenderID: "ou_x", SenderName: "张三",
			ContentRaw: `{"text":"一个老话题"}`, CreateMs: 150, UpdateMs: 150, MessagePosition: 2, ThreadID: "omt_1"},
		{MessageID: "om_reply", ChatID: "oc_a", MsgType: "text", SenderID: "ou_x", SenderName: "张三",
			ContentRaw: `{"text":"接着上面那个话题"}`, CreateMs: 200, UpdateMs: 200, MessagePosition: -3, ThreadID: "omt_1"},
	}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_reply", &unread, 200, 0))
	m.chatID = "oc_a"

	// The chat's page carries no replies, so this pane is the only place
	// their markers can be lit.
	m.rightKind, m.threadID = rightThread, "omt_1"
	next, cmd := m.Update(loadThread(Deps{Store: st}, "omt_1")().(threadLoadedMsg))
	m = next.(Model)
	collect(cmd)

	require.True(t, m.dots["om_reply"])
	require.Contains(t, rowText(m.threadRows), "●")
}
