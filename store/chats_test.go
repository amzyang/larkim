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

func TestListChats_LeavesAChatWhereAThreadReplyLandsIt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_recent", "oc_thread"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	reply := msgAt("om_reply", "oc_thread", 900, -3, "someone answered an old topic")
	reply.ThreadID = "omt_1"
	root := msgAt("om_root", "oc_thread", 200, 1, "root")
	root.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_recent", "oc_recent", 800, 1, "hello"),
		root, reply,
	}, 1)
	require.NoError(t, err)

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"oc_recent", "oc_thread"}, chatIDs(chats),
		"a thread reply does not pull its chat past a newer main-flow message")
}

func TestListChats_ThreadRootCountsAsMainFlow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_topic", 1))
	root := msgAt("om_root", "oc_topic", 100, 1, "a new topic")
	root.ThreadID = "omt_1"
	reply := msgAt("om_reply", "oc_topic", 400, -1, "an answer")
	reply.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{root, reply}, 1)
	require.NoError(t, err)

	c, err := s.GetChat(ctx, "oc_topic")
	require.NoError(t, err)
	require.Equal(t, int64(100), c.LastUnsilencedMs, "opening a thread is a main-flow event, answering it is not")
	require.Equal(t, "om_root", c.LastMessageID)
}

func TestListChats_KeepsAChatInPlaceWhenItIsRead(t *testing.T) {
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
	before := chatIDs(listChats(t, s))

	require.NoError(t, s.MarkChatRead(ctx, "oc_stale", 5000))

	require.Equal(t, before, chatIDs(listChats(t, s)),
		"reading a chat is not news about the chat, so it does not move")
}

func TestListChats_TiebreaksOnChatID(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_c", "oc_a", "oc_b"} {
		require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: id, Name: "平台组"}}, 1))
	}

	first := chatIDs(listChats(t, s))
	require.Equal(t, []string{"oc_a", "oc_b", "oc_c"}, first,
		"same name and no messages still yields one order, not an arbitrary one")
	require.Equal(t, first, chatIDs(listChats(t, s)), "and the same order on every read")
}

func TestListChats_CarriesTheUnreadCount(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Sender: "cli_c"}}
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_loud", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_1", "oc_loud", 100, 1, "did anyone look at it"),
		msgAt("om_2", "oc_loud", 200, 1, "bumping this"),
		msgAt("om_3", "oc_loud", 300, -1, "in a thread"),
		fromBot("om_4", "oc_loud", 400, "nightly build #418 passed"),
	}, 1)
	require.NoError(t, err)
	for _, id := range []string{"om_1", "om_2", "om_3", "om_4"} {
		markUnread(t, s, id)
	}

	chats := listChats(t, s)
	require.Len(t, chats, 1)
	require.Equal(t, int64(2), chats[0].UnreadCount,
		"the badge counts unread main-flow messages, thread replies and silenced ones left out")

	require.NoError(t, s.MarkChatRead(ctx, "oc_loud", 5000))
	require.Equal(t, int64(0), listChats(t, s)[0].UnreadCount)
}

// unreadCounts is the badge of every chat that carries one, the shape the
// sidebar builds from ListChats.
func unreadCounts(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, c := range listChats(t, s) {
		if c.UnreadCount > 0 {
			out[c.ChatID] = c.UnreadCount
		}
	}
	return out
}

func listChats(t *testing.T, s *Store) []Chat {
	t.Helper()
	chats, err := s.ListChats(context.Background(), ChatQuery{})
	require.NoError(t, err)
	return chats
}
