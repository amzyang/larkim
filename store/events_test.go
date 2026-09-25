package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func eventStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordEvent_ReadsBackNewestFirst(t *testing.T) {
	s := eventStore(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		require.NoError(t, s.RecordEvent(ctx, Event{AtMs: int64(i), Kind: EventResourceGone,
			Subject: "om_" + strconv.Itoa(i) + " img_x", Detail: "deleted"}))
	}

	got, err := s.LastEvents(ctx, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "om_3 img_x", got[0].Subject)
	require.Equal(t, "om_2 img_x", got[1].Subject)
	require.Equal(t, EventResourceGone, got[0].Kind)
	require.Equal(t, "deleted", got[0].Detail)
}

func TestRecordEvent_PrunesToTheNewestThousand(t *testing.T) {
	s := eventStore(t)
	ctx := context.Background()
	for i := 1; i <= 1005; i++ {
		require.NoError(t, s.RecordEvent(ctx, Event{AtMs: int64(i), Kind: EventResourceGone}))
	}

	got, err := s.LastEvents(ctx, 2000)
	require.NoError(t, err)
	require.Len(t, got, 1000)
	require.Equal(t, int64(1005), got[0].AtMs)
	require.Equal(t, int64(6), got[999].AtMs, "the oldest five were dropped")
}
