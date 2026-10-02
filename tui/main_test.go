package tui

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestMain keeps the package off the machine it runs on and off the clock.
// Most fixtures build their Deps by hand, so New fills in the real opener and
// pace; drain and collect run every tick they are handed, so the real delays
// are waited out in full.
func TestMain(m *testing.M) {
	openApplink = func(*slog.Logger, []string, bool) error { return nil }
	defaultApplinkPaceMS = testPace
	chatRefreshDelay = time.Millisecond
	chatPollEvery = time.Millisecond
	os.Exit(m.Run())
}
