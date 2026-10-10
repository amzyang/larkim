package sync

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/internal/oplog"
	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestReport_ChangedOnlyWhenSomethingLanded(t *testing.T) {
	t.Parallel()
	require.False(t, Report{Hits: 3, Chats: 2}.changed(), "discovering and listing is not landing")
	require.True(t, Report{New: 1}.changed())
	require.True(t, Report{Rendered: 1}.changed())
	require.True(t, Report{Backfilled: 1}.changed())
	require.True(t, Report{SlowPath: 1}.changed())
	require.True(t, Report{Downloaded: 1}.changed())
	require.True(t, Report{History: 1}.changed())
	require.True(t, Report{Repaired: 1}.changed())
}

// runOneTick runs the loop for exactly one tick. OnChange also fires as the
// tick writes, so the tick stamp is what says the tick is over.
func runOneTick(t *testing.T, s *Syncer) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.OnChange = func() {
		if v, ok, _ := s.Store.GetState(ctx, KeyLastTickAt); ok && v != "" {
			cancel()
		}
	}
	_ = s.Run(ctx)
}

func atLevel(s *Syncer, level slog.Level) *bytes.Buffer {
	var buf bytes.Buffer
	s.Log = oplog.NewText(&buf, level)
	return &buf
}

func TestRun_IdleTickStaysOutOfTheRecord(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	buf := atLevel(s, slog.LevelInfo)

	runOneTick(t, s)

	require.NotContains(t, buf.String(), "msg=tick",
		"the daemon ticks every few seconds; an idle one would drown the log")
}

func TestRun_TickThatLandedSomethingIsRecorded(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.AddMessage(msg("om_new", "oc_a", clk.t.Add(-30*time.Second), "fresh"))
	buf := atLevel(s, slog.LevelInfo)

	runOneTick(t, s)

	require.Contains(t, buf.String(), "msg=tick")
	require.Contains(t, buf.String(), "new=1")
	require.Contains(t, buf.String(), "level=INFO")
}

func TestRun_IdleTickIsStillThereUnderDebug(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	buf := atLevel(s, slog.LevelDebug)

	runOneTick(t, s)

	require.Contains(t, buf.String(), "msg=tick")
	require.Contains(t, buf.String(), "level=DEBUG")
}

// logLine is the first record buf holds for a message of more than one word,
// which the text handler quotes, or "".
func logLine(buf *bytes.Buffer, msg string) string {
	for line := range strings.Lines(buf.String()) {
		if strings.Contains(line, "msg="+strconv.Quote(msg)) {
			return line
		}
	}
	return ""
}

func TestRun_ATimedOutTickReachesTheDefaultLog(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	f.Err = handshakeTimeout()
	buf := atLevel(s, slog.LevelInfo)

	runOneTick(t, s)

	line := logLine(buf, "tick timed out")
	require.Contains(t, line, "level=INFO", "a timeout is excused, not hidden: the daemon logs at info")
	require.Contains(t, line, "TLS handshake timeout")
}

func TestRunDiscovery_ATimedOutProbeReachesTheDefaultLog(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	s.SetAttended(true)
	f.Err = handshakeTimeout()
	buf := atLevel(s, slog.LevelInfo)

	// Shorter than the attended pause, so exactly one cycle runs.
	ctx, cancel := context.WithTimeout(t.Context(), attendedDiscoveryPause/2)
	defer cancel()
	s.runDiscovery(ctx)

	line := logLine(buf, "discovery timed out")
	require.Contains(t, line, "level=INFO")
	require.Contains(t, line, "active probe")
	require.Empty(t, logLine(buf, "discovery failed"), "inside the grace a timeout is not a failure")
}

func TestErrClass_NamesWhyTheLoopBacksOff(t *testing.T) {
	t.Parallel()
	require.Equal(t, "auth", errClass(&larkcli.Error{ExitCode: larkcli.ExitAuth}))
	require.Equal(t, "network", errClass(&larkcli.Error{ExitCode: larkcli.ExitNetwork}))
	require.Equal(t, "rate_limit", errClass(&larkcli.Error{ExitCode: larkcli.ExitAPI, Subtype: "rate_limit"}))
	require.Equal(t, "api", errClass(&larkcli.Error{ExitCode: larkcli.ExitAPI}))
	require.Equal(t, "other", errClass(context.DeadlineExceeded))
}

func TestReport_NamingTheHeadIsNotARecord(t *testing.T) {
	t.Parallel()
	require.False(t, Report{Moved: 1}.changed(),
		"the probe names the head on every tick, so naming alone says nothing landed")
	require.True(t, Report{Probed: 1}.changed(),
		"reaching a message the store had not seen does")
}

// opIDs collects the op_id of every record in buf, and fails on a record that
// carries none: a line outside the operation is one a grep cannot follow.
func opIDs(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var ids []string
	for line := range strings.Lines(buf.String()) {
		_, after, ok := strings.Cut(line, " op_id=")
		require.True(t, ok, "record outside the operation: %s", line)
		id, _, _ := strings.Cut(strings.TrimSpace(after), " ")
		ids = append(ids, id)
	}
	return ids
}

func TestTick_LogsEveryRecordUnderOneOperation(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.AddMessage(msg("om_new", "oc_a", clk.t.Add(-30*time.Second), "fresh"))
	first := atLevel(s, slog.LevelDebug)

	_, err := s.Tick(t.Context())
	require.NoError(t, err)
	a := opIDs(t, first)
	require.NotEmpty(t, a)
	require.Len(t, slices.Compact(slices.Clone(a)), 1, "one tick is one operation")
	require.Contains(t, first.String(), "op=tick")

	second := atLevel(s, slog.LevelDebug)
	_, err = s.Tick(t.Context())
	require.NoError(t, err)
	b := opIDs(t, second)
	require.NotEmpty(t, b)
	require.NotEqual(t, a[0], b[0], "the next tick is the next operation")
}

func TestTick_StaysInsideTheCallersOperation(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	buf := atLevel(s, slog.LevelDebug)
	ctx := oplog.With(t.Context(), "sync")
	op, _ := oplog.From(ctx)

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	for _, id := range opIDs(t, buf) {
		require.Equal(t, op.ID, id, ":sync from the TUI reads as the keypress that asked for it")
	}
}

func TestRun_LogsATimedOutTickUnderItsOperation(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	f.Err = handshakeTimeout()
	buf := atLevel(s, slog.LevelInfo)

	runOneTick(t, s)

	line := logLine(buf, "tick timed out")
	require.Contains(t, line, "op=tick")
	require.Contains(t, line, "op_id=")
}

func TestRunDiscovery_LogsEachCycleUnderItsOwnOperation(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	s.SetAttended(true)
	f.Err = handshakeTimeout()
	buf := atLevel(s, slog.LevelInfo)

	ctx, cancel := context.WithTimeout(t.Context(), attendedDiscoveryPause/2)
	defer cancel()
	s.runDiscovery(ctx)

	line := logLine(buf, "discovery timed out")
	require.Contains(t, line, "op=discover")
	require.Contains(t, line, "op_id=")
}
