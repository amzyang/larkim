package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/sync"
	"github.com/amzyang/larkim/tui"
	"github.com/spf13/cobra"
)

func (a *App) tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "tui",
		Short:       "Interactive client: chats, messages, threads, composer (vim keys + mouse)",
		Annotations: map[string]string{altScreen: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			client := a.client()
			// The follow-up command a copy ends with is pasted into an agent
			// with a working directory of its own, so the config it names is
			// the absolute path this process actually loaded.
			deps := tui.Deps{Store: st, Client: client, Version: a.Version, AIContext: a.cfg.AI.Context,
				DataDir: a.cfg.DataDir, ConfigPath: config.Resolve(a.configPath), Log: a.logger()}
			if key := os.Getenv(a.cfg.AI.APIKeyEnv); key != "" {
				deps.AI = ai.New(key, a.cfg.AI.Model)
			}
			if v, _, _ := st.GetState(ctx, sync.KeySelfOpenID); v != "" {
				deps.Self = v
			}
			// Every process pulls what the reader asks for: each of those
			// calls names ids Feishu just answered for and upserts them, so
			// they are safe beside a daemon. The lock decides one thing only,
			// which is who runs the sweep and its global cursors.
			s := a.syncer(st)
			// Depth one, dropping when full: the watch only ever compares
			// revisions, so a queued signal is as good as several.
			nudge := make(chan struct{}, 1)
			s.OnChange = func() {
				select {
				case nudge <- struct{}{}:
				default:
				}
			}
			deps.Syncer, deps.Nudge = s, nudge
			if lock, err := sync.TryLock(a.cfg.DataDir); err == nil {
				defer lock.Unlock()
				a.logger().Info("tui", "sync", "embedded")
				deps.Embedded = true
				go func() {
					defer sentryRecoverRepanic()
					s.Run(ctx)
				}()
			} else if errors.Is(err, sync.ErrLocked) {
				a.logger().Info("tui", "sync", "daemon", "reason", "a daemon owns the sweep")
			} else {
				return err
			}
			// The pictures are cut from the sheet this binary carries, so a build
			// with a newer sheet cuts them again rather than asking anyone to.
			// Failing costs only the pictures: the list names what it cannot draw.
			if _, err := emoji.Ensure(a.cfg.DataDir); err != nil {
				captureError(err)
			}
			return tui.Run(ctx, deps)
		},
	}
}
