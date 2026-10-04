// Package markread is the lever that drops the Feishu client's own red dot
// once larkim has taken a chat as read, by posting the read watermark through
// larkweb. The TUI, larkim read-all, and the silence settle share it.
package markread

import (
	"context"
	"log/slog"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/store"
)

// Clear drops one chat's red dot.
type Clear func(ctx context.Context, chat store.ChatUnread) error

// timeout bounds one clear. Both callers wait on each before the next, so one
// that never returns would stop a sweep for good; a first web clear lists the
// whole inbox, which this has to cover.
const timeout = 30 * time.Second

// New builds the clear lever cfg names.
func New(cfg config.MarkRead, log *slog.Logger, st larkweb.Store) Clear {
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
