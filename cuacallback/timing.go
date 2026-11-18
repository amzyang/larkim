package cuacallback

import (
	"context"
	"log/slog"
	"time"
)

// TimedTransport measures the whole CLI round trip, including subprocess startup.
type TimedTransport struct {
	Transport Transport
	Log       *slog.Logger
	Now       func() time.Time
}

func (t TimedTransport) Call(ctx context.Context, tool string, args map[string]any, out any) error {
	started := t.Now()
	err := t.Transport.Call(ctx, tool, args, out)
	t.Log.InfoContext(ctx, "cua call done", "tool", tool, "dur_ms", t.Now().Sub(started).Milliseconds(), "err", err)
	return err
}
