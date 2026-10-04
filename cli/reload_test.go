package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// reloadApp is a daemon's App over a config file a test can rewrite.
func reloadApp(t *testing.T, body string, sets ...string) (*App, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	cfg, err := config.LoadWith(path, sets)
	require.NoError(t, err)
	cfg.DataDir = dir
	a := &App{cfg: cfg, configPath: path, sets: sets}
	st, err := storetest.Open(t, filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return a, st
}

// rewrite puts a new file in place with a modification time the stamp can
// tell from the old one, which a same-second rewrite would otherwise hide on
// a filesystem with coarse timestamps.
func rewrite(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, time.Now(), time.Now().Add(time.Second)))
}

func TestReloadOnChange(t *testing.T) {
	t.Run("hands the sweep what the file now says", func(t *testing.T) {
		a, st := reloadApp(t, "poll_interval_ms: 3000\n")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		rewrite(t, a.configPath, "poll_interval_ms: 9000\nactive_top_k: 7\n")

		reload()

		require.Equal(t, 9*time.Second, s.Opt().PollInterval)
		require.Equal(t, 7, s.Opt().ActiveTopK)
	})

	t.Run("keeps --set over an edited file", func(t *testing.T) {
		a, st := reloadApp(t, "poll_interval_ms: 3000\n", "poll_interval_ms=1000")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		require.Equal(t, time.Second, s.Opt().PollInterval)
		rewrite(t, a.configPath, "poll_interval_ms: 9000\nactive_top_k: 7\n")

		reload()

		require.Equal(t, time.Second, s.Opt().PollInterval, "the flag still wins")
		require.Equal(t, 7, s.Opt().ActiveTopK, "and the rest of the file lands")
	})

	t.Run("keeps the data dir it opened the database in", func(t *testing.T) {
		a, st := reloadApp(t, "poll_interval_ms: 3000\n")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		started := a.cfg.DataDir
		rewrite(t, a.configPath, "data_dir: /tmp/somewhere-else\n")

		reload()

		require.Equal(t, started, s.Opt().DataDir,
			"the database, the lock and lark-cli's downloads are all under the one it started on")
	})

	t.Run("stays on what it has when the file stops loading", func(t *testing.T) {
		a, st := reloadApp(t, "poll_interval_ms: 9000\n")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		rewrite(t, a.configPath, "poll_interval_ms: [not a number\n")

		reload()

		require.Equal(t, 9*time.Second, s.Opt().PollInterval)
	})

	t.Run("turns the silence settle on and off", func(t *testing.T) {
		a, st := reloadApp(t, "mark_read:\n  browser: chrome\n")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		require.Nil(t, s.SettleSilenced())

		rewrite(t, a.configPath, "mark_read:\n  browser: chrome\nsilence_sync: true\n")
		reload()
		require.NotNil(t, s.SettleSilenced())

		rewrite(t, a.configPath, "mark_read:\n  browser: chrome\nsilence_sync: false\n")
		reload()
		require.Nil(t, s.SettleSilenced())
	})

	t.Run("hands the store the new silence rules", func(t *testing.T) {
		a, st := reloadApp(t, "poll_interval_ms: 3000\n")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		require.Empty(t, st.Silence())

		rewrite(t, a.configPath, "silence:\n  - sender: cli_c\n")
		reload()

		require.Equal(t, store.SilenceRules{{Sender: "cli_c"}}, st.Silence(),
			"the next tick's ReapplySilence rebuilds the flags from these")
	})

	t.Run("leaves the options alone until the file changes", func(t *testing.T) {
		a, st := reloadApp(t, "poll_interval_ms: 3000\n")
		s := a.syncer(st)
		reload := a.reloadOnChange(s, st)
		before := s.Opt()

		reload()

		require.Same(t, before, s.Opt(), "an unchanged file costs a stat and nothing else")
	})
}

func TestFrozenKeys_NamesWhatARunningProcessCannotHonour(t *testing.T) {
	was := config.Config{DataDir: "/a", LarkCLIPath: "/bin/lark-cli"}
	now := config.Config{DataDir: "/b", LarkCLIPath: "/usr/bin/lark-cli",
		Silence: store.SilenceRules{{Sender: "cli_c"}}}
	require.Equal(t, []string{"data_dir", "lark_cli_path"}, frozenKeys(was, now),
		"silence is reloaded, not frozen")
	require.Empty(t, frozenKeys(was, was))
}
