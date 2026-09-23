package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDataRev_AdvancesOnRenderingUpdate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base, err := s.DataRev(ctx)
	require.NoError(t, err)

	m := Message{MessageID: "om_1", ChatID: "oc_a", MsgType: "text", ContentRaw: `{"text":"1"}`, CreateMs: 100, UpdateMs: 100, RawJSON: "{}"}
	_, err = s.UpsertMessages(ctx, []Message{m}, 1000)
	require.NoError(t, err)
	inserted, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, inserted, base)

	rowID, err := s.MaxMessageRowID(ctx)
	require.NoError(t, err)
	require.NoError(t, s.UpdateRendered(ctx, "om_1", "1", "", "", 1001))
	after, err := s.MaxMessageRowID(ctx)
	require.NoError(t, err)
	require.Equal(t, rowID, after, "a rendering inserts no row, which is what the ingest id misses")

	rendered, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, rendered, inserted)
}

func TestDataRev_AdvancesOnReadState(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.NoError(t, s.MarkConsumed(ctx, []string{"om_1"}, 1000))
	consumed, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, consumed, base)

	isRead := true
	require.NoError(t, s.SetReadStatus(ctx, "om_1", &isRead, 1001, 0))
	read, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, read, consumed)
}

func TestWatchRev_DeliversUpdateWithoutInsert(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := Message{MessageID: "om_1", ChatID: "oc_a", MsgType: "text", ContentRaw: `{"text":"1"}`, CreateMs: 100, UpdateMs: 100, RawJSON: "{}"}
	_, err := s.UpsertMessages(ctx, []Message{m}, 1000)
	require.NoError(t, err)

	ch := s.WatchRev(ctx, 5*time.Millisecond, nil)
	// The watcher takes its baseline in a goroutine, so a single write could
	// land before it and go unnoticed. Keep re-rendering until one arrives.
	deadline := time.After(2 * time.Second)
	for i := 0; ; i++ {
		require.NoError(t, s.UpdateRendered(ctx, "om_1", "1", "", "", int64(1001+i)))
		select {
		case rev := <-ch:
			require.NotZero(t, rev)
			return
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("no revision delivered for renderings that landed without an insert")
		}
	}
}

func TestWatchRev_NudgeBringsTheComparisonForward(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nudge := make(chan struct{}, 1)
	// An interval far beyond the test's patience: only the nudge can deliver.
	ch := s.WatchRev(ctx, time.Hour, nudge)

	deadline := time.After(2 * time.Second)
	for i := 0; ; i++ {
		require.NoError(t, s.MarkConsumed(ctx, []string{"om_1"}, int64(1000+i)))
		select {
		case nudge <- struct{}{}:
		default:
		}
		select {
		case rev := <-ch:
			require.NotZero(t, rev)
			return
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("a nudge did not bring the revision comparison forward")
		}
	}
}

func TestWatchRev_NudgeWithoutAChangeDeliversNothing(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nudge := make(chan struct{}, 1)
	ch := s.WatchRev(ctx, time.Hour, nudge)

	for range 5 {
		nudge <- struct{}{}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case rev := <-ch:
		t.Fatalf("delivered revision %d although nothing was written", rev)
	default:
	}
}
