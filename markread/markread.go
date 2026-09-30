// Package markread is the lever mark_read.mode names: what drops the Feishu
// client's own red dot once larkim has taken a chat as read. The TUI and
// larkim read-all share it, so both drop the same dot the same way, and
// neither knows which lever it holds.
package markread

import (
	"context"
	"log/slog"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/store"
)

// Clear drops one chat's red dot.
type Clear func(ctx context.Context, chat store.ChatUnread) error

// timeout bounds one clear. Both callers wait on each before the next, so one
// that never returns would stop a sweep for good; applink's open carries its
// own, and a first web clear lists the whole inbox, which this has to cover.
const timeout = 30 * time.Second

// New builds the lever cfg names. applink hands the desktop client a URL;
// web posts the read watermark with the browser's Feishu login. st is read
// only by web, which is where matched web ids are kept; open only by applink.
func New(cfg config.MarkRead, log *slog.Logger, st larkweb.Store, open func([]string, bool) error) Clear {
	if cfg.Mode == config.MarkReadWeb {
		r := &larkweb.Resolver{
			Client: &larkweb.Client{
				Cookies: larkweb.BrowserJar{Browser: cfg.Browser, Log: log},
				Log:     log,
			},
			Store: st,
		}
		return func(ctx context.Context, chat store.ChatUnread) error {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return r.MarkRead(ctx, chat.ChatID, chat.Position)
		}
	}
	return func(_ context.Context, chat store.ChatUnread) error {
		return open([]string{applink.ChatLink(chat.ChatID, chat.Position)}, true)
	}
}

// Pace is the gap a sweep leaves between two chats. applink's is the reader's
// applink_pace_ms, because the desktop client drops a chat it was hurried
// through; web's is larkweb.Pace, a bound on the burst rather than a wait for
// anything to draw.
func Pace(cfg config.Config) time.Duration {
	if cfg.MarkRead.Mode == config.MarkReadWeb {
		return larkweb.Pace
	}
	return time.Duration(cfg.ApplinkPaceMS) * time.Millisecond
}

// Confirms reports whether a lever's success is Feishu's answer. web's is:
// the gateway said the chat is read. applink's is only macOS saying it
// launched the URL, which is not the client saying it drew the chat.
func Confirms(cfg config.MarkRead) bool {
	return cfg.Mode == config.MarkReadWeb
}
