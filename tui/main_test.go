package tui

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/sfnt"
)

// TestMain keeps the package off the machine it runs on and off the clock.
// Most fixtures build their Deps by hand, so New fills in the real opener and
// clear lever; drain and collect run every tick they are handed, so the real
// delays are waited out in full.
func TestMain(m *testing.M) {
	f, err := sfnt.Parse(gobold.TTF)
	if err != nil {
		panic(err)
	}
	// Font-dependent tests must exercise rendering even without macOS fonts.
	avatarFont = func() *sfnt.Font { return f }
	openApplink = func(context.Context, *slog.Logger, []string) error { return nil }
	newClearBadge = func(config.MarkRead, *slog.Logger, larkweb.Store) markread.Clear {
		return func(context.Context, store.ChatUnread) error { return nil }
	}
	clearPace = time.Millisecond
	chatRefreshDelay = time.Millisecond
	chatPollEvery = time.Millisecond
	os.Exit(m.Run())
}
