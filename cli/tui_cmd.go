package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/sync"
	"github.com/amzyang/larkim/tui"
	"github.com/spf13/cobra"
)

func (a *App) tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "tui",
		Short:       "Interactive client: chats, messages, threads, composer (vim keys + mouse)",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{altScreen: "true"},
		RunE:        a.runTUI,
	}
}

// runTUI is the entry point both the bare command and `larkim tui` run.
func (a *App) runTUI(_ *cobra.Command, _ []string) error {
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
	deps := tui.Deps{Store: st, Client: client, Version: a.Version,
		DataDir: a.cfg.DataDir, ConfigPath: config.Resolve(a.configPath), Log: a.logger(),
		Config: a.cfg}
	// The agent runs in a directory of its own with nothing in it, since the
	// transcript is all it is meant to read.
	aiDir := filepath.Join(a.cfg.DataDir, "ai")
	if err := os.MkdirAll(aiDir, 0o700); err != nil {
		return err
	}
	deps.NewAI = func(agent, model string) tui.AIStreamer {
		argv := strings.Fields(agent)
		if len(argv) == 0 {
			return nil
		}
		if _, err := exec.LookPath(argv[0]); err != nil {
			return nil
		}
		return ai.New(argv, model, aiDir, deps.Log)
	}
	deps.AI = deps.NewAI(a.cfg.AI.Agent, a.cfg.AI.Model)
	deps.NewSuggest = func(keyEnv, endpoint string) tui.ReactSuggester {
		key := os.Getenv(keyEnv)
		if key == "" {
			return nil
		}
		return jev.New(key, endpoint)
	}
	deps.Suggest = deps.NewSuggest(a.cfg.AI.JevKeyEnv, a.cfg.AI.JevEndpoint)
	deps.Self = selfOpenID(ctx, st)
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
	// The model starts focused (tui.New), and the terminal reports only
	// changes, so discovery starts out attended with it.
	s.SetAttended(true)
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
	// with a newer sheet cuts them again rather than asking anyone to. Every
	// emoji is drawn as its picture, so a start without them is a broken one.
	if _, err := emoji.Ensure(a.cfg.DataDir); err != nil {
		return err
	}
	return tui.Run(ctx, deps)
}
