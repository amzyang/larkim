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
	ids, err := s.ReadStatusCandidates(ctx, "ou_me", 100, 2000, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_a"}, ids, "own and too-old messages excluded")

	unread := false
	require.NoError(t, s.SetReadStatus(ctx, "om_a", &unread, 2000, 3000))
	ids, _ = s.ReadStatusCandidates(ctx, "ou_me", 100, 2500, 10)
	require.Empty(t, ids, "not due yet")
	ids, _ = s.ReadStatusCandidates(ctx, "ou_me", 100, 3000, 10)
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
	ids, _ = s.ReadStatusCandidates(ctx, "ou_me", 100, 1e12, 10)
	require.Empty(t, ids, "read messages are never re-checked")
}
