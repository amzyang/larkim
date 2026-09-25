package sync

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestReport_ChangedOnlyWhenSomethingLanded(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
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
	s.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	return &buf
}

func TestRun_IdleTickStaysOutOfTheRecord(t *testing.T) {
	s, _, _ := newSyncer(t)
	buf := atLevel(s, slog.LevelInfo)

	runOneTick(t, s)

	require.NotContains(t, buf.String(), "msg=tick",
		"the daemon ticks every few seconds; an idle one would drown the log")
}

func TestRun_TickThatLandedSomethingIsRecorded(t *testing.T) {
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
	s, _, _ := newSyncer(t)
	buf := atLevel(s, slog.LevelDebug)

	runOneTick(t, s)

	require.Contains(t, buf.String(), "msg=tick")
	require.Contains(t, buf.String(), "level=DEBUG")
}

func TestErrClass_NamesWhyTheLoopBacksOff(t *testing.T) {
	require.Equal(t, "auth", errClass(&larkcli.Error{ExitCode: larkcli.ExitAuth}))
	require.Equal(t, "network", errClass(&larkcli.Error{ExitCode: larkcli.ExitNetwork}))
	require.Equal(t, "rate_limit", errClass(&larkcli.Error{ExitCode: larkcli.ExitAPI, Subtype: "rate_limit"}))
	require.Equal(t, "api", errClass(&larkcli.Error{ExitCode: larkcli.ExitAPI}))
	require.Equal(t, "other", errClass(context.DeadlineExceeded))
}
