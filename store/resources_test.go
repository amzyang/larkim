package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResources_Lifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{{MessageID: "om_1", ChatID: "oc", CreateMs: 10, RawJSON: "{}"}, {MessageID: "om_2", ChatID: "oc", CreateMs: 20, RawJSON: "{}"}}, 1)
	require.NoError(t, err)
	require.NoError(t, s.AddPendingResources(ctx, []Resource{{MessageID: "om_1", FileKey: "img_a", Type: "image"}, {MessageID: "om_2", FileKey: "file_b", Type: "file"}}))
	require.NoError(t, s.AddPendingResources(ctx, []Resource{{MessageID: "om_1", FileKey: "img_a", Type: "image"}}), "re-adding is a no-op")

	due, err := s.ResourceMessagesDue(ctx, 100, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_2", "om_1"}, due, "newest message first")

	require.NoError(t, s.MarkResourceDone(ctx, "om_1", "img_a", "resources/lark-im-resources/img_a.jpg", 123))
	require.NoError(t, s.MarkResourceFailed(ctx, "om_2", "file_b", "timeout", 500))
	due, _ = s.ResourceMessagesDue(ctx, 100, 10)
	require.Empty(t, due, "failed row waits for next_attempt_at")
	due, _ = s.ResourceMessagesDue(ctx, 500, 10)
	require.Equal(t, []string{"om_2"}, due)
	require.NoError(t, s.MarkResourceFailed(ctx, "om_2", "file_b", "gave up", 0))
	due, _ = s.ResourceMessagesDue(ctx, 1e12, 10)
	require.Empty(t, due, "next_attempt_at = 0 means permanently failed")

	rs, err := s.ResourcesFor(ctx, "om_2")
	require.NoError(t, err)
	require.Equal(t, 2, rs[0].Attempts)
	require.Equal(t, "failed", rs[0].Status)
	counts, _ := s.ResourceCounts(ctx)
	require.Equal(t, int64(1), counts["done"])
	require.NoError(t, s.MarkResourceSkipped(ctx, "om_1", "img_a", 999, "too large"))
	counts, _ = s.ResourceCounts(ctx)
	require.Equal(t, int64(1), counts["skipped"])
}

func TestUnrenderedMessageIDs_SkipsMessagesWithUnfinishedResources(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_plain", ChatID: "oc", CreateMs: 60, RawJSON: "{}"},
		{MessageID: "om_pending", ChatID: "oc", CreateMs: 50, RawJSON: "{}"},
		{MessageID: "om_failed", ChatID: "oc", CreateMs: 40, RawJSON: "{}"},
		{MessageID: "om_done", ChatID: "oc", CreateMs: 30, RawJSON: "{}"},
		{MessageID: "om_skipped", ChatID: "oc", CreateMs: 20, RawJSON: "{}"},
		{MessageID: "om_deleted", ChatID: "oc", CreateMs: 10, Deleted: true, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.AddPendingResources(ctx, []Resource{
		{MessageID: "om_pending", FileKey: "k_pending", Type: "image"},
		{MessageID: "om_failed", FileKey: "k_failed", Type: "file"},
		{MessageID: "om_done", FileKey: "k_done", Type: "image"},
		{MessageID: "om_skipped", FileKey: "k_skipped", Type: "file"},
	}))
	require.NoError(t, s.MarkResourceFailed(ctx, "om_failed", "k_failed", "timeout", 500))
	require.NoError(t, s.MarkResourceDone(ctx, "om_done", "k_done", "resources/k_done.jpg", 1))
	require.NoError(t, s.MarkResourceSkipped(ctx, "om_skipped", "k_skipped", 999, "too large"))

	ids, err := s.UnrenderedMessageIDs(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_plain", "om_done", "om_skipped"}, ids, "pending/failed downloads and deleted messages wait; newest first")

	require.NoError(t, s.UpdateRendered(ctx, "om_plain", "hi", "", "", 2))
	ids, _ = s.UnrenderedMessageIDs(ctx, 10)
	require.Equal(t, []string{"om_done", "om_skipped"}, ids)
}

func TestReadStatus_CandidatesAndSchedule(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_mine", ChatID: "oc", SenderID: "ou_me", CreateMs: 1000, RawJSON: "{}"},
		{MessageID: "om_a", ChatID: "oc", SenderID: "ou_x", CreateMs: 900, RawJSON: "{}"},
		{MessageID: "om_old", ChatID: "oc", SenderID: "ou_x", CreateMs: 1, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	q := ReadCheckQuery{Self: "ou_me", SinceMs: 100, DueAt: 2000, Limit: 10}
	ids, err := s.ReadStatusCandidates(ctx, q)
	require.NoError(t, err)
	require.Equal(t, []string{"om_a"}, ids, "own and too-old messages excluded")

	unread := false
	require.NoError(t, s.SetReadStatus(ctx, "om_a", &unread, 2000, 3000))
	q.DueAt = 2500
	ids, _ = s.ReadStatusCandidates(ctx, q)
	require.Empty(t, ids, "not due yet")
	q.DueAt = 3000
	ids, _ = s.ReadStatusCandidates(ctx, q)
	require.Equal(t, []string{"om_a"}, ids)
	n, _ := s.ReadCheckCount(ctx, "om_a")
	require.Equal(t, 1, n)
	unreadN, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(1), unreadN)

	require.NoError(t, s.MarkConsumed(ctx, []string{"om_a"}, 4000))
	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_a", &read, 4000, 0))
	m, _ := s.GetMessage(ctx, "om_a")
	require.True(t, *m.IsReadRemote)
	require.Equal(t, int64(4000), m.ConsumedAt, "consumed_at survives read-status updates")
	n, _ = s.ReadCheckCount(ctx, "om_a")
	require.Equal(t, 2, n)
	q.DueAt = 1e12
	ids, _ = s.ReadStatusCandidates(ctx, q)
	require.Empty(t, ids, "read messages are never re-checked")
}

func TestReadStatusCandidates_ChatScopeOvertakesTheBackoff(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_here", ChatID: "oc_here", SenderID: "ou_x", CreateMs: 900, RawJSON: "{}"},
		{MessageID: "om_elsewhere", ChatID: "oc_other", SenderID: "ou_x", CreateMs: 901, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, s.SetReadStatus(ctx, "om_here", &unread, 1000, 9e12))
	require.NoError(t, s.SetReadStatus(ctx, "om_elsewhere", &unread, 1000, 9e12))

	ids, err := s.ReadStatusCandidates(ctx, ReadCheckQuery{Self: "ou_me", SinceMs: 100, DueAt: 2000, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, ids, "both sit far out in the backoff")

	ids, err = s.ReadStatusCandidates(ctx, ReadCheckQuery{Self: "ou_me", SinceMs: 100, ChatID: "oc_here", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"om_here"}, ids, "no DueAt asks now, and only for the named chat")
}

func TestExpireReadStatus_RelaxesUnreadPastTheHorizon(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_old", ChatID: "oc", SenderID: "ou_x", CreateMs: 100, RawJSON: "{}"},
		{MessageID: "om_recent", ChatID: "oc", SenderID: "ou_x", CreateMs: 900, RawJSON: "{}"},
		{MessageID: "om_old_read", ChatID: "oc", SenderID: "ou_x", CreateMs: 101, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	unread, read := false, true
	require.NoError(t, s.SetReadStatus(ctx, "om_old", &unread, 200, 300))
	require.NoError(t, s.SetReadStatus(ctx, "om_recent", &unread, 200, 300))
	require.NoError(t, s.SetReadStatus(ctx, "om_old_read", &read, 200, 0))

	n, err := s.ExpireReadStatus(ctx, 500)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	m, _ := s.GetMessage(ctx, "om_old")
	require.Nil(t, m.IsReadRemote, "out of polling reach, so no longer claimed unread")
	m, _ = s.GetMessage(ctx, "om_recent")
	require.False(t, *m.IsReadRemote)
	m, _ = s.GetMessage(ctx, "om_old_read")
	require.True(t, *m.IsReadRemote, "a read flag stays true however old")

	count, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(1), count)
}

func TestUnreadCountsByChat_CountsMutedChats(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_loud", Name: "loud"}, {ChatID: "oc_muted", Name: "muted"}}, 1))
	require.NoError(t, s.SetMuteStatus(ctx, map[string]bool{"oc_muted": true}, nil, 1))
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_loud", ChatID: "oc_loud", SenderID: "ou_x", CreateMs: 10, RawJSON: "{}"},
		{MessageID: "om_muted", ChatID: "oc_muted", SenderID: "ou_x", CreateMs: 20, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, s.SetReadStatus(ctx, "om_loud", &unread, 100, 0))
	require.NoError(t, s.SetReadStatus(ctx, "om_muted", &unread, 100, 0))

	counts, err := s.UnreadCountsByChat(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"oc_loud": 1, "oc_muted": 1}, counts,
		"the badge is drawn grey for a muted chat, not withheld")

	total, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(2), total)
}

func TestResourcesForMessages_GroupsByMessage(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_1", ChatID: "oc", CreateMs: 10, RawJSON: "{}"},
		{MessageID: "om_2", ChatID: "oc", CreateMs: 20, RawJSON: "{}"},
		{MessageID: "om_3", ChatID: "oc", CreateMs: 30, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.AddPendingResources(ctx, []Resource{
		{MessageID: "om_1", FileKey: "img_b", Type: "image"},
		{MessageID: "om_1", FileKey: "img_a", Type: "image"},
		{MessageID: "om_2", FileKey: "file_c", Type: "file"},
	}))

	got, err := s.ResourcesForMessages(ctx, []string{"om_1", "om_2", "om_3"})
	require.NoError(t, err)
	require.Len(t, got, 2, "a message without attachments has no entry")
	require.Equal(t, []string{"img_a", "img_b"}, []string{got["om_1"][0].FileKey, got["om_1"][1].FileKey})
	require.Equal(t, "file_c", got["om_2"][0].FileKey)
}

func TestUnreadCountsByChat_SkipsThreadReplies(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	root := msgAt("om_root", "oc_a", 100, 1, "root")
	root.ThreadID = "omt_1"
	reply := msgAt("om_reply", "oc_a", 900, -3, "answered months later")
	reply.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{root, reply}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_root")
	markUnread(t, s, "om_reply")

	counts, err := s.UnreadCountsByChat(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"oc_a": 1}, counts, "only the main-flow root is badged")

	total, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(2), total, "the backlog measure counts the reply all the same")
}

func TestMarkChatRead_ClearsTheBadgeOfOneChat(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_a1", "oc_a", 10, 1, "one"),
		msgAt("om_a2", "oc_a", 20, 1, "two"),
		msgAt("om_b", "oc_b", 30, 1, "elsewhere"),
	}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_a1")
	markUnread(t, s, "om_a2")
	markUnread(t, s, "om_b")

	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 5000))

	counts, err := s.UnreadCountsByChat(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"oc_b": 1}, counts, "the chat that was read carries no badge")

	total, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(3), total, "the poller's backlog is about Feishu, which still has all three unread")

	m, _ := s.GetMessage(ctx, "om_a1")
	require.Equal(t, int64(5000), m.LocalReadAt)
	require.False(t, *m.IsReadRemote, "the remote receipt is untouched: larkim cannot write it")
}

func TestMarkChatRead_LeavesThreadRepliesAndReadMessagesAlone(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	reply := msgAt("om_reply", "oc_a", 20, -3, "answered an old topic")
	reply.ThreadID = "omt_1"
	deleted := msgAt("om_gone", "oc_a", 30, 1, "recalled")
	deleted.Deleted = true
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_seen", "oc_a", 10, 1, "read"), reply, deleted}, 1)
	require.NoError(t, err)
	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_seen", &read, 100, 0))
	markUnread(t, s, "om_reply")
	markUnread(t, s, "om_gone")

	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 5000))

	for _, id := range []string{"om_seen", "om_reply", "om_gone"} {
		m, _ := s.GetMessage(ctx, id)
		require.Zero(t, m.LocalReadAt, "%s is outside what the badge counts, so reading the chat says nothing about it", id)
	}
}

func TestMarkChatRead_KeepsTheConsumedCursor(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_a", 10, 1, "one")}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_a")
	require.NoError(t, s.MarkConsumed(ctx, []string{"om_a"}, 4000))

	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 5000))

	m, _ := s.GetMessage(ctx, "om_a")
	require.Equal(t, int64(4000), m.ConsumedAt, "the CLI cursor is not what a reader moves")
	require.Equal(t, int64(5000), m.LocalReadAt)
}

func TestMarkChatRead_WritesNothingWhenTheChatIsAlreadyRead(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_a", 10, 1, "one")}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_a")
	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 5000))

	before, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 6000))
	after, err := s.DataRev(ctx)
	require.NoError(t, err)

	require.Equal(t, before, after, "an inert call must not wake the watchers that reload on it")
	m, _ := s.GetMessage(ctx, "om_a")
	require.Equal(t, int64(5000), m.LocalReadAt, "the first reading is when it was read")
}
