package store

import (
	"context"
	"fmt"
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
	require.NoError(t, s.SetReadStatus(ctx, "om_1", nil, 1000, 2000))
	inserted, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, inserted, base)

	isRead := true
	require.NoError(t, s.SetReadStatus(ctx, "om_1", &isRead, 1001, 0))
	updated, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, updated, inserted)
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
		require.NoError(t, s.SetReadStatus(ctx, fmt.Sprintf("om_%d", i), nil, 1000, 0))
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

func TestDataRev_IgnoresTheSyncersOwnBookkeeping(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	chats := []Chat{{ChatID: "oc_a", Name: "平台组"}, {ChatID: "oc_b", Name: "项目协作群"}}
	require.NoError(t, s.UpsertChats(ctx, chats, 1))
	batch := []Message{msgAt("om_a", "oc_a", 100, 1, "morning"), msgAt("om_b", "oc_b", 200, 1, "elsewhere")}
	_, err := s.UpsertMessages(ctx, batch, 1)
	require.NoError(t, err)

	before, err := s.DataRev(ctx)
	require.NoError(t, err)

	// A full refresh round over unchanged data: the listing restamps every
	// chat, the ingest restamps every message, the mute rotation and the
	// backfill advance their own cursors.
	require.NoError(t, s.UpsertChats(ctx, chats, 2))
	_, err = s.UpsertMessages(ctx, batch, 2)
	require.NoError(t, err)
	require.NoError(t, s.SetMuteStatus(ctx, nil, []string{"oc_a", "oc_b"}, 2))
	require.NoError(t, s.SetChatCursor(ctx, "oc_a", 500))

	after, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "a round that changed nothing a reader looks at asks no one to re-read")
}

func TestDataRev_AdvancesWhenSomethingVisibleChanges(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_a", Name: "平台组"}}, 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_a", 100, 1, "morning")}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_a")

	for _, tc := range []struct {
		name  string
		write func() error
	}{
		{"a rename", func() error {
			return s.UpsertChats(ctx, []Chat{{ChatID: "oc_a", Name: "平台组 (archived)"}}, 2)
		}},
		{"an edit", func() error {
			_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_a", 100, 1, "morning all")}, 2)
			return err
		}},
		{"a mute", func() error {
			return s.SetMuteStatus(ctx, map[string]bool{"oc_a": true}, nil, 2)
		}},
		{"a local read", func() error { return s.MarkChatRead(ctx, "oc_a", 2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := s.DataRev(ctx)
			require.NoError(t, err)
			require.NoError(t, tc.write())
			after, err := s.DataRev(ctx)
			require.NoError(t, err)
			require.Greater(t, after, before)
		})
	}
}
