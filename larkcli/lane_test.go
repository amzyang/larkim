package larkcli

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slowBinary plays a lark-cli that dawdles over the path /slow and answers
// anything else at once, so a test can hold one lane occupied and time a call
// in the other without the fake itself being the delay.
func slowBinary(t *testing.T, seconds string) *ExecClient {
	t.Helper()
	return fakeBinary(t, `case "$3" in /slow) sleep `+seconds+`;; esac
echo '{"ok":true,"data":{}}'`)
}

func TestLane_AcquireReturnsWhenContextEnds(t *testing.T) {
	l := make(lane, 1)
	require.NoError(t, l.acquire(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := l.acquire(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	l.release()
	require.NoError(t, l.acquire(context.Background()))
}

func TestLane_AcquireAdmitsUpToCapacity(t *testing.T) {
	l := make(lane, 2)
	require.NoError(t, l.acquire(context.Background()))
	require.NoError(t, l.acquire(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.Error(t, l.acquire(ctx))
}

func TestLaneOf_DefaultsToBackground(t *testing.T) {
	require.Equal(t, LaneBackground, LaneOf(context.Background()))
	require.Equal(t, LaneInteractive, LaneOf(WithLane(context.Background(), LaneInteractive)))
	require.Equal(t, LaneBeat, LaneOf(WithLane(context.Background(), LaneBeat)))
	require.Equal(t, LaneBackground, LaneOf(WithLane(context.Background(), LaneBackground)))
}

func TestExec_InteractiveDoesNotQueueBehindBackground(t *testing.T) {
	c := slowBinary(t, "2")
	occupy(t, c, LaneBackground)

	start := time.Now()
	_, err := c.run(WithLane(context.Background(), LaneInteractive), "api", "GET", "/quick")
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second,
		"an interactive call waited out a background sweep")
}

func TestExec_BeatDoesNotQueueBehindBackground(t *testing.T) {
	c := slowBinary(t, "2")
	occupy(t, c, LaneBackground)

	start := time.Now()
	_, err := c.run(WithLane(context.Background(), LaneBeat), "api", "GET", "/quick")
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second,
		"the open chat's beat waited out a background sweep")
}

func TestExec_InteractiveDoesNotQueueBehindTheBeat(t *testing.T) {
	c := slowBinary(t, "2")
	occupy(t, c, LaneBeat)

	start := time.Now()
	_, err := c.run(WithLane(context.Background(), LaneInteractive), "api", "GET", "/quick")
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second,
		"a keystroke waited out the beat it did not ask for")
}

func TestExec_BackgroundCallsStillQueue(t *testing.T) {
	c := slowBinary(t, "1")
	occupy(t, c, LaneBackground)

	start := time.Now()
	_, err := c.run(context.Background(), "api", "GET", "/second")
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 500*time.Millisecond,
		"the background lane admitted more calls than its width")
}

func TestExec_TimeoutStartsAfterTheLaneIsFree(t *testing.T) {
	c := slowBinary(t, "1")
	// Shorter than the wait the next call is about to sit through, so a
	// timeout clock started before the lane was free would expire on it.
	c.Timeout = 600 * time.Millisecond
	occupy(t, c, LaneBackground)

	_, err := c.run(context.Background(), "api", "GET", "/second")
	require.NoError(t, err, "the queued call spent its timeout waiting for the lane")
}

func TestExec_CancelledCallGivesUpItsPlaceInLine(t *testing.T) {
	c := slowBinary(t, "2")
	occupy(t, c, LaneBackground)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := c.run(ctx, "api", "GET", "/queued")
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), time.Second,
		"a cancelled call stayed pinned behind the sweep")
}

func TestExec_ResolvePathFailsBeforeTakingALane(t *testing.T) {
	c := &ExecClient{Path: filepath.Join(t.TempDir(), "nope")}
	_, err := c.run(context.Background(), "api", "GET", "/x")
	require.Error(t, err)
	// The lane must be free: a call that never ran must not hold one.
	require.NoError(t, c.laneFor(LaneBackground).acquire(context.Background()))
}

// occupy fills every slot in lane l with a slow call and waits until they are
// all in flight, so the call a test times next is certainly behind them.
func occupy(t *testing.T, c *ExecClient, l Lane) {
	t.Helper()
	line := c.laneFor(l)
	var wg sync.WaitGroup
	for range cap(line) {
		wg.Go(func() {
			_, _ = c.run(WithLane(context.Background(), l), "api", "GET", "/slow")
		})
	}
	t.Cleanup(wg.Wait)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(line) == cap(line) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("lane never filled")
}

func TestSearchMessages_SendsTheQueryAndOnePage(t *testing.T) {
	c := fakeBinary(t, `
echo "$@" >&2
cat <<'JSON'
{"ok":true,"data":{"items":[
 {"meta_data":{"message_id":"om_1","chat_id":"oc_a","from_id":"ou_x","position":7,"type":"TEXT","create_time":"2026-09-15T10:19:45Z"}}
],"has_more":true}}
JSON`)
	hits, err := c.SearchMessages(context.Background(), "预算", 20)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "om_1", hits[0].MessageID)
	require.Equal(t, int64(7), hits[0].Position)
	// The raw endpoint dates a hit to the second, which is what a cursor
	// needs; `im +messages-search` renders it for a person to read.
	require.Equal(t, time.Date(2026, 9, 15, 10, 19, 45, 0, time.UTC), hits[0].CreateTime.UTC())
}

func TestSearchMessages_AsksForNoMoreThanOnePage(t *testing.T) {
	c := fakeBinary(t, `echo "$*" > "$(dirname "$0")/argv"; echo '{"ok":true,"data":{"items":[]}}'`)
	_, err := c.SearchMessages(context.Background(), "预算", 500)
	require.NoError(t, err)
	argv, err := os.ReadFile(filepath.Join(filepath.Dir(c.Path), "argv"))
	require.NoError(t, err)
	require.Contains(t, string(argv), `"query":"预算"`)
	require.Contains(t, string(argv), "--page-size 50", "a page size over the cap is clamped")
	require.NotContains(t, string(argv), "--page-all", "a reader waits for one page, not the archive")
}

func TestSearchMessages_EmptyQueryAsksNothing(t *testing.T) {
	c := fakeBinary(t, `exit 9`)
	hits, err := c.SearchMessages(context.Background(), "  ", 10)
	require.NoError(t, err)
	require.Empty(t, hits)
}
