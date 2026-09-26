package sync

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

// gate holds every caller whose call matches prefix until want of them have
// arrived, so a test fails rather than passes when the calls are made one at a
// time. It answers "were these in flight together", which counting calls
// afterwards cannot.
func gate(t *testing.T, f *larkcli.Fake, prefix string, want int) *atomic.Int64 {
	t.Helper()
	var arrived atomic.Int64
	all := make(chan struct{})
	f.Enter = func(call string) {
		if !strings.HasPrefix(call, prefix) {
			return
		}
		if arrived.Add(1) == int64(want) {
			close(all)
		}
		select {
		case <-all:
		case <-time.After(5 * time.Second):
			t.Errorf("%s was called one at a time; %d of %d arrived", prefix, arrived.Load(), want)
		}
	}
	return &arrived
}

// backfilled makes a chat one the cursor pull will accept: not backfilled yet
// is the one reason pullFromCursor steps over a chat silently.
func backfilled(t *testing.T, s *Syncer, now time.Time, ids ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		require.NoError(t, s.Store.EnsureChat(ctx, id, now.UnixMilli()))
		require.NoError(t, s.Store.SetChatBackfillDone(ctx, id, now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli()))
	}
}

func TestPullFromCursor_ListsEveryChatAtOnce(t *testing.T) {
	s, f, clk := newSyncer(t)
	ids := []string{"oc_a", "oc_b", "oc_c", "oc_d"}
	backfilled(t, s, clk.t, ids...)
	arrived := gate(t, f, "list:chat:", len(ids))

	_, _, err := s.pullFromCursor(context.Background(), ids, "test", clk.t)
	require.NoError(t, err)
	require.Equal(t, int64(len(ids)), arrived.Load())
}

func TestPullFromCursor_RecordsAPermanentRefusalAndPullsTheRest(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	ids := []string{"oc_a", "oc_quiet", "oc_b"}
	backfilled(t, s, clk.t, ids...)
	f.AddMessage(msg("om_1", "oc_a", clk.t.Add(-time.Minute), "hello"))
	f.AddMessage(msg("om_2", "oc_b", clk.t.Add(-time.Minute), "hello"))
	// "restricted mode" is how a tenant refuses one chat's listing for good.
	f.ListErr = map[string]error{"oc_quiet": &larkcli.Error{
		ExitCode: larkcli.ExitAPI, Code: 230002, Message: "restricted mode"}}

	total, _, err := s.pullFromCursor(ctx, ids, "test", clk.t)
	require.NoError(t, err, "one chat's permanent refusal stopped the sweep")
	require.Equal(t, 2, total)

	refused, err := s.Store.GetChat(ctx, "oc_quiet")
	require.NoError(t, err)
	require.Contains(t, refused.SyncError, "restricted mode")
	for _, id := range []string{"oc_a", "oc_b"} {
		c, err := s.Store.GetChat(ctx, id)
		require.NoError(t, err)
		require.Empty(t, c.SyncError)
	}
}

func TestPullFromCursor_ReturnsAFatalRefusalWithoutRecordingIt(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	backfilled(t, s, clk.t, "oc_a")
	f.ListErr = map[string]error{"oc_a": &larkcli.Error{
		ExitCode: larkcli.ExitAPI, Subtype: "rate_limit", Code: 99991400, RetryAfter: time.Minute}}

	_, _, err := s.pullFromCursor(ctx, []string{"oc_a"}, "test", clk.t)
	le, ok := errors.AsType[*larkcli.Error](err)
	require.True(t, ok, "a rate limit must reach the caller that paces the tick")
	require.True(t, le.IsRateLimit())

	// Recording it would retire the chat over an answer that is about timing.
	c, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Empty(t, c.SyncError)
}

func TestPullChat_ListsEveryThreadAtOnce(t *testing.T) {
	s, f, clk := newSyncer(t)
	at := clk.t.Add(-time.Minute)
	tids := []string{"omt_a", "omt_b", "omt_c"}
	for i, tid := range tids {
		root := msg("om_root"+string(rune('a'+i)), "oc_a", at, "root")
		root.ThreadID = tid
		f.AddMessage(root)
		reply := msg("om_reply"+string(rune('a'+i)), "oc_a", at, "reply")
		reply.ThreadID, reply.MessagePosition = tid, -1
		f.AddMessage(reply)
	}
	arrived := gate(t, f, "list:thread:", len(tids))

	n, _, err := s.pullChat(context.Background(), "oc_a", at.Add(-time.Minute), time.Time{}, clk.t)
	require.NoError(t, err)
	require.Equal(t, int64(len(tids)), arrived.Load())
	require.Equal(t, 2*len(tids), n, "every thread's reply reached the store")
}

func TestBackfillSlice_MarksEveryChatDone(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	ids := []string{"oc_a", "oc_b", "oc_c"}
	for _, id := range ids {
		require.NoError(t, s.Store.EnsureChat(ctx, id, clk.t.UnixMilli()))
		f.AddMessage(msg("om_"+id, id, clk.t.Add(-time.Hour), "hello"))
	}
	arrived := gate(t, f, "list:chat:", len(ids))

	total, err := s.backfillSlice(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, int64(len(ids)), arrived.Load())
	require.Equal(t, len(ids), total)
	for _, id := range ids {
		c, err := s.Store.GetChat(ctx, id)
		require.NoError(t, err)
		require.NotZero(t, c.BackfillDoneAt, id+" was pulled but never marked done")
	}
}
