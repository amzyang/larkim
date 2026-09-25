package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
// does, running whatever the model asks for in return.
func arrive(t *testing.T, m Model, st *store.Store, chatID string) Model {
	t.Helper()
	msg, ok := loadMessages(st, chatID, 0)().(messagesLoadedMsg)
	require.True(t, ok)
	next, cmd := m.update(msg)
	collect(cmd)
	return next.(Model)
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
	require.True(t, m.dots["om_a"], "what was waiting when the visit began still wears a marker")
}

func TestUpdate_TheUnreadMarkerOutlivesTheReadItCaused(t *testing.T) {
	m, st := readModel(t)
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

	msg, ok := searchMessages(st, "在吗")().(searchMsg)
	require.True(t, ok)
	require.Len(t, msg.msgs, 1)
	next, _ := m.update(msg)
	m = next.(Model)

	require.True(t, m.dots["om_a"], "a hit Feishu still reports unread carries a marker")
	require.Contains(t, rowText(m.msgRows), "●")
}

func TestUpdate_AThreadReplysMarkerIsGoneOnTheNextVisit(t *testing.T) {
	m, st := readModel(t)
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: "om_reply", ChatID: "oc_a", MsgType: "text",
		SenderID: "ou_x", SenderName: "张三", ContentRaw: `{"text":"接着上面那个话题"}`,
		CreateMs: 200, UpdateMs: 200, MessagePosition: -3, ThreadID: "omt_1"}}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_reply", &unread, 200, 0))
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")
	require.True(t, m.dots["om_reply"], "a reply that landed while the reader was away wears a marker")

	m.pendingChat = "oc_a"
	m.enterChat()
	m = arrive(t, m, st, "oc_a")

	require.Empty(t, m.dots, "the reply the pane put in front of the reader was read with the chat")
	require.False(t, strings.Contains(rowText(renderRows(m.msgs, m.msgStyleFor(60, m.meta))), "●"))
}
