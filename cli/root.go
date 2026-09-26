// Package cli wires cobra commands onto the larkim packages.
package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	gosync "sync"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// App carries process-wide dependencies into commands.
type App struct {
	Version      string
	Out          io.Writer
	Err          io.Writer
	configPath   string
	jsonOut      bool
	debug        bool
	log          *slog.Logger
	cfg          config.Config
	sentryFlag   string
	sentryDSN    string // effective DSN after resolution
	sentrySource string
	buildDSN     string

	clientOnce gosync.Once
	// larkClient is the Feishu boundary every command shares. A test sets it
	// before the command runs so a fake stands where the subprocess would;
	// left nil, the first caller builds the real one.
	larkClient larkcli.Client
}

// New builds the root command. buildDSN is the Sentry DSN baked in at build
// time (empty in local builds, so telemetry is off unless configured).
func New(version, buildDSN string) *cobra.Command {
	app := &App{Version: version, Out: os.Stdout, Err: os.Stderr, buildDSN: buildDSN}
	root := &cobra.Command{
		Use:           "larkim",
		Short:         "Feishu/Lark IM synced to local SQLite, with a CLI and TUI on top",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Follow the streams cobra was handed, so a test can read what a
			// command prints.
			app.Out, app.Err = cmd.OutOrStdout(), cmd.ErrOrStderr()
			// Shell completion must stay side-effect free and fast: no crash
			// reporter, and no log file created just by pressing Tab.
			completion := cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd
			app.log = slog.New(slog.DiscardHandler)
			if !completion {
				envValue, envSet := os.LookupEnv("SENTRY_DSN")
				app.sentryDSN, app.sentrySource = resolveSentryDSN(app.sentryFlag, cmd.Flags().Changed("sentry-dsn"),
					os.Getenv("DO_NOT_TRACK"), envValue, envSet, app.buildDSN)
				initSentry(app.sentryDSN, version)
			}
			cfg, err := config.Load(app.configPath)
			if err != nil {
				return err
			}
			app.cfg = cfg
			if !completion {
				app.initLog(cmd)
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&app.configPath, "config", "", "config file (default ~/.larkim/config.yaml)")
	root.PersistentFlags().BoolVar(&app.jsonOut, "json", false, "JSON output (default when stdout is not a terminal)")
	root.PersistentFlags().BoolVar(&app.debug, "debug", false,
		"log every lark-cli request and response (always-on logging lives in <data_dir>/larkim.log)")
	root.PersistentFlags().StringVar(&app.sentryFlag, "sentry-dsn", "", "Sentry DSN for crash reporting (overrides SENTRY_DSN and the build-time default; empty disables)")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })
	root.AddCommand(app.syncCmd(), app.statusCmd(), app.daemonCmd(), app.chatsCmd(), app.messagesCmd(), app.contactsCmd(),
		app.sendCmd(), app.replyCmd(), app.reactCmd(), app.watchCmd(), app.silenceCmd(), app.tuiCmd(), app.dbCmd(),
		app.schemaCmd(), app.emojiCmd(), app.sentryCmd())
	return root
}

// Execute runs the CLI and returns the process exit code. Defects are
// reported to Sentry when telemetry is enabled; expected failures are not.
func Execute(version, buildDSN string) int {
	defer sentryRecoverRepanic()
	if err := New(version, buildDSN).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "larkim:", err)
		captureError(err)
		return 1
	}
	return 0
}

func (a *App) json() bool {
	if a.jsonOut {
		return true
	}
	f, ok := a.Out.(*os.File)
	return ok && !term.IsTerminal(int(f.Fd()))
}

// logger tolerates a nil log, which is what a test building an App directly
// rather than through New has.
func (a *App) logger() *slog.Logger {
	if a.log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return a.log
}

func (a *App) openStore() (*store.Store, error) {
	if err := os.MkdirAll(a.cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(a.cfg.DBPath())
	if err != nil {
		return nil, err
	}
	st.Silence = a.cfg.Silence
	return st, nil
}

// client is the one lark-cli runner this process has. It is shared rather
// than built per caller because the lanes inside it are a per-process budget:
// a second client would be a second pair of lanes, and the TUI's sweeps and
// its keystrokes would stop knowing about each other.
func (a *App) client() larkcli.Client {
	a.clientOnce.Do(func() {
		if a.larkClient != nil {
			return
		}
		if err := os.MkdirAll(a.cfg.ResourcesDir(), 0o700); err != nil {
			a.logger().Warn("resources dir", "err", err)
		}
		a.larkClient = &larkcli.ExecClient{Path: a.cfg.LarkCLIPath, Dir: a.cfg.ResourcesDir(), Log: a.logger()}
	})
	return a.larkClient
}

func (a *App) syncer(st *store.Store) *sync.Syncer {
	return &sync.Syncer{Client: a.client(), Store: st, Clock: sync.RealClock{}, Opt: sync.OptionsFrom(a.cfg),
		Log: a.logger(), OnError: captureError, Fetch: sync.HTTPFetch}
}

// parseTime accepts YYYY-MM-DD, RFC 3339, or a duration such as 24h (relative to now).
func parseTime(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (use 2026-09-01, 2026-09-01T10:00:00+08:00 or 24h)", s)
}
