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
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{{MessageID: "om_1", FileKey: "img_a", Type: "image"}, {MessageID: "om_2", FileKey: "file_b", Type: "file"}}))
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{{MessageID: "om_1", FileKey: "img_a", Type: "image"}}), "re-adding is a no-op")

	due, err := s.ResourceMessagesDue(ctx, 100, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_2", "om_1"}, due, "newest message first")

	require.NoError(t, s.MarkResourceDone(ctx, "img_a", "resources/lark-im-resources/img_a.jpg", 123))
	require.NoError(t, s.MarkResourceFailed(ctx, "file_b", "timeout", 500))
	due, _ = s.ResourceMessagesDue(ctx, 100, 10)
	require.Empty(t, due, "failed row waits for next_attempt_at")
	due, _ = s.ResourceMessagesDue(ctx, 500, 10)
	require.Equal(t, []string{"om_2"}, due)
	require.NoError(t, s.MarkResourceFailed(ctx, "file_b", "gave up", 0))
	due, _ = s.ResourceMessagesDue(ctx, 1e12, 10)
	require.Empty(t, due, "next_attempt_at = 0 means permanently failed")

	rs, err := s.ResourcesFor(ctx, "om_2")
	require.NoError(t, err)
	require.Equal(t, 2, rs[0].Attempts)
	require.Equal(t, "failed", rs[0].Status)
	counts, _ := s.ResourceCounts(ctx)
	require.Equal(t, int64(1), counts["done"])
	require.NoError(t, s.MarkResourceSkipped(ctx, "img_a", 999, "too large"))
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
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
		{MessageID: "om_pending", FileKey: "k_pending", Type: "image"},
		{MessageID: "om_failed", FileKey: "k_failed", Type: "file"},
		{MessageID: "om_done", FileKey: "k_done", Type: "image"},
		{MessageID: "om_skipped", FileKey: "k_skipped", Type: "file"},
	}))
	require.NoError(t, s.MarkResourceFailed(ctx, "k_failed", "timeout", 500))
	require.NoError(t, s.MarkResourceDone(ctx, "k_done", "resources/k_done.jpg", 1))
	require.NoError(t, s.MarkResourceSkipped(ctx, "k_skipped", 999, "too large"))

	ids, err := s.UnrenderedMessageIDs(ctx, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_plain", "om_done", "om_skipped"}, ids, "pending/failed downloads and deleted messages wait; newest first")

	require.NoError(t, s.UpdateRendered(ctx, "om_plain", "hi", "", "", 2))
	ids, _ = s.UnrenderedMessageIDs(ctx, "", 10)
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

	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_a", &read, 4000, 0))
	m, _ := s.GetMessage(ctx, "om_a")
	require.True(t, *m.IsReadRemote)
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

func TestListChats_CountTheBadgeOfMutedChats(t *testing.T) {
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

	require.Equal(t, map[string]int64{"oc_loud": 1, "oc_muted": 1}, unreadCounts(t, s),
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
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
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

func TestListChats_LeaveThreadRepliesOutOfTheBadge(t *testing.T) {
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

	require.Equal(t, map[string]int64{"oc_a": 1}, unreadCounts(t, s), "only the main-flow root is badged")

	total, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(2), total, "the backlog measure counts the reply all the same")
}

func TestMarkChatRead_ClearsTheBadgeOfOneChat(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	require.NoError(t, s.EnsureChat(ctx, "oc_b", 1))
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

	require.Equal(t, map[string]int64{"oc_b": 1}, unreadCounts(t, s), "the chat that was read carries no badge")

	total, _ := s.UnreadCount(ctx)
	require.Equal(t, int64(3), total, "the poller's backlog is about Feishu, which still has all three unread")

	m, _ := s.GetMessage(ctx, "om_a1")
	require.Equal(t, int64(5000), m.LocalReadAt)
	require.False(t, *m.IsReadRemote, "the remote receipt is untouched: larkim cannot write it")
}

func TestMarkChatRead_TakesTheThreadRepliesThePaneShowed(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	reply := msgAt("om_reply", "oc_a", 20, -3, "answered an old topic")
	reply.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_root", "oc_a", 10, 1, "an old topic"), reply}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_root")
	markUnread(t, s, "om_reply")

	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 5000))

	m, _ := s.GetMessage(ctx, "om_reply")
	require.Equal(t, int64(5000), m.LocalReadAt,
		"the reply renders in the chat's flow, so the visit that read the chat read it too")
	require.Empty(t, unreadCounts(t, s), "the badge never counted the reply and still does not")
}

func TestMarkChatRead_LeavesReadAndDeletedMessagesAlone(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	deleted := msgAt("om_gone", "oc_a", 30, 1, "recalled")
	deleted.Deleted = true
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_seen", "oc_a", 10, 1, "read"), deleted}, 1)
	require.NoError(t, err)
	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_seen", &read, 100, 0))
	markUnread(t, s, "om_gone")

	require.NoError(t, s.MarkChatRead(ctx, "oc_a", 5000))

	for _, id := range []string{"om_seen", "om_gone"} {
		m, _ := s.GetMessage(ctx, id)
		require.Zero(t, m.LocalReadAt, "%s is nothing the reader has waiting, so reading the chat says nothing about it", id)
	}
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

func sharedFixture(t *testing.T) *Store {
	t.Helper()
	s := openTest(t)
	msgs := []Message{}
	for _, id := range []string{"om_a", "om_b", "om_c"} {
		msgs = append(msgs, Message{MessageID: id, ChatID: "oc_a", CreateMs: 10, RawJSON: "{}"})
	}
	_, err := s.UpsertMessages(context.Background(), msgs, 1)
	require.NoError(t, err)
	return s
}

func TestAddPendingResources_OneLedgerRowPerKey(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
		{MessageID: "om_a", FileKey: "img_shared", Type: "image"},
		{MessageID: "om_b", FileKey: "img_shared", Type: "image"},
		{MessageID: "om_c", FileKey: "img_own", Type: "image"},
	}))
	require.NoError(t, s.MarkResourceDone(ctx, "img_shared", "resources/img_shared.png", 42))

	for _, id := range []string{"om_a", "om_b"} {
		rs, err := s.ResourcesFor(ctx, id)
		require.NoError(t, err)
		require.Len(t, rs, 1)
		require.Equal(t, "done", rs[0].Status, id)
		require.Equal(t, "resources/img_shared.png", rs[0].LocalPath, id)
		require.Equal(t, int64(42), rs[0].SizeBytes, id)
	}
	counts, _ := s.ResourceCounts(ctx)
	require.Equal(t, int64(1), counts["done"], "two messages, one key, one download")
	require.Equal(t, int64(1), counts["pending"])
}

func TestResourceMessagesDue_LeavesOutMessagesWhoseKeyIsSettled(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
		{MessageID: "om_a", FileKey: "img_gone", Type: "image"},
		{MessageID: "om_b", FileKey: "img_gone", Type: "image"},
		{MessageID: "om_c", FileKey: "img_slow", Type: "image"},
	}))
	require.NoError(t, s.MarkResourceFailed(ctx, "img_gone", "Resource Has Been Deleted", 0))
	require.NoError(t, s.MarkResourceFailed(ctx, "img_slow", "timeout", 500))

	due, err := s.ResourceMessagesDue(ctx, 100, 10)
	require.NoError(t, err)
	require.Empty(t, due, "one refusal answers every message that names the key")

	due, err = s.ResourceMessagesDue(ctx, 500, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_c"}, due, "a failure still in backoff is asked again")
}

func TestResourcesFor_ReadsThroughTheReferences(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
		{MessageID: "om_a", FileKey: "img_1", Type: "image"},
		{MessageID: "om_a", FileKey: "img_2", Type: "image"},
		{MessageID: "om_b", FileKey: "img_2", Type: "image"},
	}))

	rs, err := s.ResourcesFor(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, []string{"img_1", "img_2"}, []string{rs[0].FileKey, rs[1].FileKey})
	byMsg, err := s.ResourcesForMessages(ctx, []string{"om_a", "om_b", "om_c"})
	require.NoError(t, err)
	require.Len(t, byMsg["om_a"], 2)
	require.Len(t, byMsg["om_b"], 1)
	require.NotContains(t, byMsg, "om_c", "a message with no references is absent")
}

func TestResourcesDueFor_LeavesALandedKeyAloneWhenItsMessageIsReachedForAnother(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
		{MessageID: "om_a", FileKey: "img_gone", Type: "image"},
		{MessageID: "om_a", FileKey: "img_new", Type: "image"},
		{MessageID: "om_a", FileKey: "img_kept", Type: "image"},
		{MessageID: "om_a", FileKey: "img_big", Type: "image"},
		{MessageID: "om_a", FileKey: "v3_face", Type: "sticker"},
	}))
	require.NoError(t, s.MarkResourceFailed(ctx, "img_gone", "Resource Has Been Deleted", 0))
	require.NoError(t, s.MarkResourceDone(ctx, "img_kept", "resources/img_kept.png", 9))
	require.NoError(t, s.MarkResourceSkipped(ctx, "img_big", 999, "too large"))

	due, err := s.ResourcesDueFor(ctx, "om_a", 100)
	require.NoError(t, err)
	keys := make([]string, len(due))
	for i, r := range due {
		keys[i] = r.FileKey
	}
	require.Equal(t, []string{"img_new"}, keys,
		"a landed refusal, a fetched key, a skipped one and a sticker are all settled business")
}

func TestResourcesDueFor_AsksAgainOnceTheBackoffIsOwed(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{{MessageID: "om_a", FileKey: "img_slow", Type: "image"}}))
	require.NoError(t, s.MarkResourceFailed(ctx, "img_slow", "timeout", 500))

	due, err := s.ResourcesDueFor(ctx, "om_a", 100)
	require.NoError(t, err)
	require.Empty(t, due)
	due, err = s.ResourcesDueFor(ctx, "om_a", 500)
	require.NoError(t, err)
	require.Len(t, due, 1)
}
