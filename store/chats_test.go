package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func chatIDs(chats []Chat) []string {
	out := make([]string, len(chats))
	for i, c := range chats {
		out[i] = c.ChatID
	}
	return out
}

func markUnread(t *testing.T, s *Store, messageID string) {
	t.Helper()
	unread := false
	require.NoError(t, s.SetReadStatus(context.Background(), messageID, &unread, 100, 0))
}

func TestListChats_LiftsChatsUnreadInTheMainFlow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_recent", "oc_stale", "oc_thread"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	reply := msgAt("om_reply", "oc_thread", 900, -3, "someone answered an old topic")
	reply.ThreadID = "omt_1"
	root := msgAt("om_root", "oc_thread", 200, 1, "root")
	root.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_recent", "oc_recent", 800, 1, "read"),
		msgAt("om_stale", "oc_stale", 100, 1, "unread"),
		root, reply,
	}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_stale")
	markUnread(t, s, "om_reply")

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"oc_stale", "oc_recent", "oc_thread"}, chatIDs(chats),
		"main-flow unread comes first; a thread reply leaves its chat where it was")
}

func TestListChats_LiftsMutedChatsOnTheSameRule(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_recent"}, {ChatID: "oc_muted"}}, 1))
	require.NoError(t, s.SetMuteStatus(ctx, map[string]bool{"oc_muted": true}, nil, 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_recent", "oc_recent", 800, 1, "read"),
		msgAt("om_muted", "oc_muted", 100, 1, "unread"),
	}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_muted")

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"oc_muted", "oc_recent"}, chatIDs(chats),
		"do-not-disturb changes how the count is drawn, not whether the chat is waiting")
}

func TestListChats_ThreadRootCountsAsMainFlow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_recent", 1))
	require.NoError(t, s.EnsureChat(ctx, "oc_topic", 1))
	root := msgAt("om_root", "oc_topic", 100, 1, "a new topic")
	root.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_recent", "oc_recent", 800, 1, "read"), root}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_root")

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"oc_topic", "oc_recent"}, chatIDs(chats),
		"opening a thread is a main-flow event")
}

func TestListChats_DropsALocallyReadChatOutOfTheUnreadGroup(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_recent", "oc_stale"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_recent", "oc_recent", 800, 1, "read"),
		msgAt("om_stale", "oc_stale", 100, 1, "unread"),
	}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_stale")
	require.Equal(t, []string{"oc_stale", "oc_recent"}, chatIDs(listChats(t, s)))

	require.NoError(t, s.MarkChatRead(ctx, "oc_stale", 5000))

	require.Equal(t, []string{"oc_recent", "oc_stale"}, chatIDs(listChats(t, s)),
		"reading a chat in larkim settles it back into date order")
}

func listChats(t *testing.T, s *Store) []Chat {
	t.Helper()
	chats, err := s.ListChats(context.Background(), ChatQuery{})
	require.NoError(t, err)
	return chats
}
