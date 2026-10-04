package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestLoad_MissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	require.Equal(t, 3000, cfg.PollIntervalMS)
	require.Equal(t, int64(50<<20), cfg.Resources.MaxBytes)
	require.True(t, filepath.IsAbs(cfg.DataDir))
}

func TestLoad_OverridesAndFloors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("poll_interval_ms: 0\ndata_dir: ~/x\nbackfill_days: 7\nresources:\n  max_bytes: 1\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, 100, cfg.PollIntervalMS, "floored at 100ms")
	require.Equal(t, 7, cfg.BackfillDays)
	require.Equal(t, int64(1), cfg.Resources.MaxBytes)
	require.NotContains(t, cfg.DataDir, "~")
	require.Equal(t, filepath.Join(cfg.DataDir, "larkim.db"), cfg.DBPath())
}

func TestLoad_ParsesSilenceRules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("silence:\n  - chat: oc_quiet\n    sender: cli_c\n  - contains: nightly build\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	// The file lists the chat rule first; the canonical order puts the rule
	// with no chat before it.
	require.Equal(t, store.SilenceRules{{Contains: "nightly build"}, {Chat: "oc_quiet", Sender: "cli_c"}}, cfg.Silence)
}

func TestLoad_RejectsASilenceRuleThatMatchesEverything(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("silence:\n  - chat: \"\"\n"), 0o644))
	_, err := Load(p)
	require.ErrorContains(t, err, "silence rule 1")
}

func TestDefault_ReadsChromesJar(t *testing.T) {
	require.Equal(t, MarkRead{Browser: "chrome"}, Default().MarkRead)
}

func TestLoad_TakesTheBrowser(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("mark_read:\n  browser: edge\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, MarkRead{Browser: "edge"}, cfg.MarkRead)

	require.NoError(t, os.WriteFile(p, []byte("applink_pace_ms: 100\nmark_read:\n  mode: web\n  browser: edge\n"), 0o644))
	cfg, err = Load(p)
	require.NoError(t, err)
	require.Equal(t, MarkRead{Browser: "edge"}, cfg.MarkRead)
}

func TestLoad_RejectsWebModeWithNoBrowser(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("mark_read:\n  browser: \"\"\n"), 0o644))
	_, err := Load(p)
	require.ErrorContains(t, err, "mark_read.browser")
}

func TestLoad_RejectsABrowserWebModeCannotRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("mark_read:\n  browser: safari\n"), 0o644))
	_, err := Load(p)
	require.ErrorContains(t, err, `mark_read.browser: "safari" is not one of`)
}

func TestLoadWith_BeatsTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("poll_interval_ms: 250\nbackfill_days: 7\n"), 0o644))
	cfg, err := LoadWith(p, []string{"poll_interval_ms=1500"})
	require.NoError(t, err)
	require.Equal(t, 1500, cfg.PollIntervalMS)
	require.Equal(t, 7, cfg.BackfillDays, "a key the flag left alone keeps the file's value")
}

func TestLoadWith_AppliesToAMissingFile(t *testing.T) {
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"poll_interval_ms=1500"})
	require.NoError(t, err)
	require.Equal(t, 1500, cfg.PollIntervalMS)
}

func TestLoadWith_ReachesANestedKey(t *testing.T) {
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"resources.max_bytes=123", "ai.context=9"})
	require.NoError(t, err)
	require.Equal(t, int64(123), cfg.Resources.MaxBytes)
	require.Equal(t, 9, cfg.AI.Context)
	require.Equal(t, "cursor/composer-2.5-fast", cfg.AI.Model, "a sibling in the same section survives")
}

func TestLoadWith_ParsesAValueTheWayTheFileWould(t *testing.T) {
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"overlap=90s", "data_dir=~/x"})
	require.NoError(t, err)
	require.Equal(t, 90*time.Second, cfg.Overlap)
	require.NotContains(t, cfg.DataDir, "~", "expansion runs after the sets, not before")
}

func TestLoadWith_StillFloorsThePollInterval(t *testing.T) {
	// The reason the sets land before normalisation: the floor has to judge
	// the value that will actually be used.
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"poll_interval_ms=0"})
	require.NoError(t, err)
	require.Equal(t, 100, cfg.PollIntervalMS)
}

func TestLoad_KeepsASubSecondPollInterval(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("poll_interval_ms: 250\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, 250, cfg.PollIntervalMS)
}

func TestLoadWith_NamesAKeyTheConfigHasNot(t *testing.T) {
	_, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"pol_interval_ms=1500"})
	require.ErrorContains(t, err, "pol_interval_ms")
}

func TestLoadWith_RefusesSomethingThatIsNotAPair(t *testing.T) {
	_, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"poll_interval_ms"})
	require.ErrorContains(t, err, "key=value")
}

func TestKeys_NamesEveryFieldAndDotsTheNestedOnes(t *testing.T) {
	keys := Keys()
	require.Subset(t, keys, []string{"data_dir", "poll_interval_ms", "silence", "resources.max_bytes", "ai.model"})
	require.NotContains(t, keys, "resources", "a section is not a key a value can be set on")
	// Every key Keys names must be one --set can actually reach, or the
	// completion offers what the flag then refuses.
	for _, k := range keys {
		require.NoError(t, applySets(new(Config), []string{k + "="}), k)
	}
}

func TestGet_RoundTripsEveryKey(t *testing.T) {
	cfg := Default()
	cfg.Silence = store.SilenceRules{{Chat: "oc_quiet"}, {Contains: "nightly build"}}
	for _, k := range Keys() {
		v, ok := cfg.Get(k)
		require.True(t, ok, k)
		back := Default()
		back.Silence = cfg.Silence
		require.NoError(t, back.Set(k, v), "%s=%s", k, v)
		require.Equal(t, cfg, back, k)
	}
}

func TestGet_SpellsValuesTheWayTheFileDoes(t *testing.T) {
	cfg := Default()
	for key, want := range map[string]string{
		"poll_interval_ms":    "3000",
		"repair_every":        "6h",
		"overlap":             "2m",
		"backfill_days":       "30",
		"resources.max_bytes": "52428800",
		"ai.model":            "cursor/composer-2.5-fast",
		"silence":             "[]",
	} {
		v, ok := cfg.Get(key)
		require.True(t, ok, key)
		require.Equal(t, want, v, key)
	}
}

func TestGet_RefusesAKeyThatIsNotOne(t *testing.T) {
	_, ok := Default().Get("resources")
	require.False(t, ok, "a section holds no value")
	_, ok = Default().Get("nope")
	require.False(t, ok)
}

func TestLoad_ParsesSilenceSync(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("silence_sync: true\nmark_read:\n  browser: edge\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.True(t, cfg.SilenceSync)
	require.False(t, Default().SilenceSync, "off is the default: it writes the user's account state")
}

func TestDefault_DisablesTodoistUntilATokenIsSet(t *testing.T) {
	require.Equal(t, Todoist{}, Default().Todoist)
}

func TestLoad_TakesTheTodoistSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"todoist:\n  token: tk_1\n  project_id: proj_1\n"), 0o644))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, Todoist{Token: "tk_1", ProjectID: "proj_1"}, cfg.Todoist)
}

func TestKeys_NamesTheTodoistKeys(t *testing.T) {
	require.Contains(t, Keys(), "todoist.token")
	require.Contains(t, Keys(), "todoist.project_id")
}
