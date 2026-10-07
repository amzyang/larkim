// Package cli wires cobra commands onto the larkim packages.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	gosync "sync"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// App carries process-wide dependencies into commands.
type App struct {
	Version string
	Out     io.Writer
	Err     io.Writer
	// In is where a body flag reads from when it is passed `-`. A test hands
	// a reader in so the pipe is not the process's own.
	In           io.Reader
	configPath   string
	sets         []string
	jsonOut      bool
	debug        bool
	log          *slog.Logger
	cfg          config.Config
	sentryFlag   string
	sentryDSN    string // effective DSN after resolution
	sentrySource string
	buildDSN     string

	// clearBadge drops one chat's Feishu red dot. A test replaces it so the
	// clears are recorded rather than posted to the gateway; left nil, read-all
	// builds markread.New.
	clearBadge markread.Clear

	// todoistBase is where the todoist.project completion lists projects. A
	// test points it at a local server; left empty, the client's default.
	todoistBase string

	clientOnce gosync.Once
	// larkClient is the Feishu boundary every command shares. A test sets it
	// before the command runs so a fake stands where the subprocess would;
	// left nil, the first caller builds the real one.
	larkClient larkcli.Client
}

// New builds the root command. buildDSN is the Sentry DSN baked in at build
// time (empty in local builds, so telemetry is off unless configured).
func New(version, buildDSN string) *cobra.Command {
	app := &App{Version: version, Out: os.Stdout, Err: os.Stderr, In: os.Stdin, buildDSN: buildDSN}
	root := &cobra.Command{
		Use:           "larkim",
		Short:         "Feishu/Lark IM synced to local SQLite, with a CLI and TUI on top",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `larkim` is the TUI, the way the desktop client opens on its
		// chats. NoArgs keeps a mistyped subcommand an error instead of an
		// argument the TUI would ignore.
		Args:        cobra.NoArgs,
		Annotations: map[string]string{altScreen: "true"},
		RunE:        app.runTUI,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Follow the streams cobra was handed, so a test can read what a
			// command prints.
			app.Out, app.Err = cmd.OutOrStdout(), cmd.ErrOrStderr()
			// Shell completion must stay side-effect free and fast: no crash
			// reporter, and no log file created just by pressing Tab.
			completion := isCompletion(cmd)
			app.log = slog.New(slog.DiscardHandler)
			if !completion {
				envValue, envSet := os.LookupEnv("SENTRY_DSN")
				app.sentryDSN, app.sentrySource = resolveSentryDSN(app.sentryFlag, cmd.Flags().Changed("sentry-dsn"),
					os.Getenv("DO_NOT_TRACK"), envValue, envSet, app.buildDSN)
				initSentry(app.sentryDSN, version)
			}
			cfg, err := config.LoadWith(app.configPath, app.sets)
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
	root.PersistentFlags().StringArrayVar(&app.sets, "set", nil,
		"override one config key for this run, repeatable (--set backfill_days=7)")
	root.PersistentFlags().BoolVar(&app.jsonOut, "json", false, "JSON output (default when stdout is not a terminal)")
	root.PersistentFlags().BoolVar(&app.debug, "debug", false,
		"log every lark-cli request and response (always-on logging lives in <data_dir>/larkim.log)")
	root.PersistentFlags().StringVar(&app.sentryFlag, "sentry-dsn", "", "Sentry DSN for crash reporting (overrides SENTRY_DSN and the build-time default; empty disables)")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })
	root.AddCommand(app.syncCmd(), app.statusCmd(), app.daemonCmd(), app.chatsCmd(), app.messagesCmd(), app.contactsCmd(),
		app.sendCmd(), app.replyCmd(), app.reactCmd(), app.watchCmd(), app.readAllCmd(), app.silenceCmd(), app.tuiCmd(), app.dbCmd(),
		app.schemaCmd(), app.emojiCmd(), app.sentryCmd(), app.unreadCmd(), app.lintCmd(), app.candidatesCmd())
	mustWire(root.MarkPersistentFlagFilename("config", "yaml", "yml"))
	mustWire(root.RegisterFlagCompletionFunc("set", app.completeConfigKey))
	completeNoFileDefault(root)
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

// selfOpenID is the reader's own open id as the last sync learned it, and ""
// until one has.
func selfOpenID(ctx context.Context, st *store.Store) string {
	v, _, _ := st.GetState(ctx, sync.KeySelfOpenID)
	return v
}

func (a *App) openStore() (*store.Store, error) {
	if err := os.MkdirAll(a.cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(a.cfg.DBPath())
	if err != nil {
		return nil, err
	}
	st.SetSilence(a.cfg.Silence)
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
	s := &sync.Syncer{Client: a.client(), Store: st, Clock: sync.RealClock{},
		Log: a.logger(), OnError: captureError, Fetch: sync.HTTPFetch, Recover: sentryRecoverRepanic}
	s.SetOptions(sync.OptionsFrom(a.cfg))
	s.SetSettleSilenced(a.settle(a.cfg, st))
	return s
}

// settle is the silenced-unread settle lever cfg asks for, nil when
// silence_sync is off. cfg is a parameter rather than a.cfg because a reload
// asks this of a configuration it has not installed anywhere yet.
func (a *App) settle(cfg config.Config, st *store.Store) markread.Clear {
	if !cfg.SilenceSync {
		return nil
	}
	return markread.New(cfg.MarkRead, a.logger(), st)
}

// reloadOnChange watches the configuration file and hands the sweep what it
// now says, which is how a key retuned under a running daemon takes effect
// without one. It returns a sync.Syncer's BeforeTick, so the reread happens
// between two ticks on the sweep's own goroutine rather than inside one.
//
// The file's modification time is what it watches: the daemon already wakes
// every poll interval, so a stat per tick costs nothing, and writeDoc renames
// a whole file over the old one — there is no half-written version to read.
// A file that fails to load leaves the daemon on the configuration it has.
//
// --set still wins: the overrides are applied over the reread file the same
// way they were over the first read, so a flag is not undone by an edit.
func (a *App) reloadOnChange(s *sync.Syncer, st *store.Store) func() {
	path := config.Resolve(a.configPath)
	stamp, started := configStamp(path), a.cfg
	return func() {
		now := configStamp(path)
		if now == stamp {
			return
		}
		stamp = now
		cfg, err := config.LoadWith(a.configPath, a.sets)
		if err != nil {
			a.logger().Warn("config reload refused", "path", path, "err", err)
			return
		}
		opt := sync.OptionsFrom(cfg)
		// data_dir names the database this process opened, the lock it holds
		// and the directory lark-cli is already writing into, so the reload
		// keeps the one it started on and says it did.
		opt.DataDir = started.DataDir
		s.SetOptions(opt)
		s.SetSettleSilenced(a.settle(cfg, st))
		// The tick this runs before opens on ReapplySilence, which sees the
		// new fingerprint and rebuilds every flag from these rules.
		st.SetSilence(cfg.Silence)
		a.logger().Info("config reloaded", "path", path, "poll_interval_ms", cfg.PollIntervalMS)
		for _, k := range frozenKeys(started, cfg) {
			a.logger().Warn("config key needs a restart", "key", k)
		}
	}
}

// frozenKeys names the keys the reread file changed that a running process
// cannot honour: the database and lock live under data_dir, and lark-cli's
// lanes are built around its path.
func frozenKeys(was, now config.Config) []string {
	var out []string
	if was.DataDir != now.DataDir {
		out = append(out, "data_dir")
	}
	if was.LarkCLIPath != now.LarkCLIPath {
		out = append(out, "lark_cli_path")
	}
	return out
}

// configStamp is what a write to the file changes. A missing file stamps
// empty, so creating or deleting one is a change like any other.
func configStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(fi.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(fi.Size(), 10)
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
