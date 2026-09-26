package sync

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncer_StateTimeReturnsZeroForAKeyNeverWritten(t *testing.T) {
	s, _, _ := newSyncer(t)
	got, err := s.stateTime(t.Context(), KeyWatermark)
	require.NoError(t, err)
	require.True(t, got.IsZero())
}

// A watermark that cannot be read is not a first run: FastWindow would take
// the zero for one and collapse the search to a single overlap, and the tick
// would then write that window's end over the watermark it never saw.
func TestSyncer_StateTimeReportsAReadFailureRatherThanAFirstRun(t *testing.T) {
	s, _, _ := newSyncer(t)
	require.NoError(t, s.Store.Close())
	_, err := s.stateTime(t.Context(), KeyWatermark)
	require.Error(t, err)
}
