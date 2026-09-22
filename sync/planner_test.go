package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFastWindow(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	overlap := 2 * time.Minute

	w := FastWindow(time.Time{}, now, overlap)
	require.Equal(t, now.Add(-overlap), w.Start, "first run looks back one overlap")
	require.Equal(t, now, w.End)

	wm := now.Add(-10 * time.Second)
	w = FastWindow(wm, now, overlap)
	require.Equal(t, wm.Add(-overlap), w.Start)

	w = FastWindow(now.Add(time.Hour), now, overlap)
	require.Equal(t, now.Add(-overlap), w.Start, "clock skew resets to now-overlap")
}

func TestHalves(t *testing.T) {
	s := time.Unix(0, 0)
	a, b, ok := Halves(Window{s, s.Add(10 * time.Minute)})
	require.True(t, ok)
	require.Equal(t, s.Add(5*time.Minute), a.End)
	require.Equal(t, a.End, b.Start)
	_, _, ok = Halves(Window{s, s.Add(MinWindow)})
	require.False(t, ok)
}

func TestDueChunkBackoffUnique(t *testing.T) {
	now := time.Unix(1000, 0)
	require.True(t, Due(time.Time{}, time.Minute, now))
	require.False(t, Due(now.Add(-30*time.Second), time.Minute, now))
	require.True(t, Due(now.Add(-time.Minute), time.Minute, now))

	require.Equal(t, [][]int{{1, 2}, {3}}, Chunk([]int{1, 2, 3}, 2))
	require.Nil(t, Chunk([]int{}, 2))

	require.Equal(t, 30*time.Second, Backoff(1, 30*time.Second, 10*time.Minute))
	require.Equal(t, 2*time.Minute, Backoff(3, 30*time.Second, 10*time.Minute))
	require.Equal(t, 10*time.Minute, Backoff(20, 30*time.Second, 10*time.Minute))

	require.Equal(t, []string{"a", "b"}, UniqueStrings([]string{"a", "", "b", "a"}))
}
