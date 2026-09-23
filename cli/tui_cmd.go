package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/sync"
	"github.com/amzyang/larkim/tui"
	"github.com/spf13/cobra"
)

func (a *App) tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive client: chats, messages, threads, composer (vim keys + mouse)",
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
				DataDir: a.cfg.DataDir, ConfigPath: config.Resolve(a.configPath)}
			if key := os.Getenv(a.cfg.AI.APIKeyEnv); key != "" {
				deps.AI = ai.New(key, a.cfg.AI.Model)
			}
			if v, _, _ := st.GetState(ctx, sync.KeySelfOpenID); v != "" {
				deps.Self = v
			}
			// Sync in-process when no daemon holds the lock; otherwise read only.
			if lock, err := sync.TryLock(a.cfg.DataDir); err == nil {
				defer lock.Unlock()
				s := a.syncer(st)
				s.Log = quietLogger()
				deps.Syncer = s
				deps.Embedded = true
				go func() {
					defer sentryRecoverRepanic()
					s.Run(ctx)
				}()
			} else if !errors.Is(err, sync.ErrLocked) {
				return err
			}
			return tui.Run(ctx, deps)
		},
	}
}
