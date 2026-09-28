package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestLoad_MissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.PollInterval)
	require.Equal(t, int64(50<<20), cfg.Resources.MaxBytes)
	require.True(t, filepath.IsAbs(cfg.DataDir))
}

func TestLoad_OverridesAndFloors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("poll_interval: 100ms\ndata_dir: ~/x\nbackfill_days: 7\nresources:\n  max_bytes: 1\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, time.Second, cfg.PollInterval, "floored at 1s")
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
	require.Equal(t, store.SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}, {Contains: "nightly build"}}, cfg.Silence)
}

func TestLoad_RejectsASilenceRuleThatMatchesEverything(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("silence:\n  - chat: \"\"\n"), 0o644))
	_, err := Load(p)
	require.ErrorContains(t, err, "silence rule 1")
}

func TestDefault_TakesThePaceFromApplink(t *testing.T) {
	// One source for the number, so config.example.yaml and the TUI's :set&
	// cannot come to name different defaults.
	require.Equal(t, applink.DefaultPaceMS, Default().ApplinkPaceMS)
}

func TestLoadWith_BeatsTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("applink_pace_ms: 250\nbackfill_days: 7\n"), 0o644))
	cfg, err := LoadWith(p, []string{"applink_pace_ms=1500"})
	require.NoError(t, err)
	require.Equal(t, 1500, cfg.ApplinkPaceMS)
	require.Equal(t, 7, cfg.BackfillDays, "a key the flag left alone keeps the file's value")
}

func TestLoadWith_AppliesToAMissingFile(t *testing.T) {
	// --set is how a run happens without a config file at all.
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"applink_pace_ms=1500"})
	require.NoError(t, err)
	require.Equal(t, 1500, cfg.ApplinkPaceMS)
}

func TestLoadWith_ReachesANestedKey(t *testing.T) {
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"resources.max_bytes=123", "ai.context=9"})
	require.NoError(t, err)
	require.Equal(t, int64(123), cfg.Resources.MaxBytes)
	require.Equal(t, 9, cfg.AI.Context)
	require.Equal(t, "claude-opus-5", cfg.AI.Model, "a sibling in the same section survives")
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
	cfg, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"poll_interval=10ms"})
	require.NoError(t, err)
	require.Equal(t, time.Second, cfg.PollInterval)
}

func TestLoadWith_NamesAKeyTheConfigHasNot(t *testing.T) {
	_, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"applnk_pace_ms=1500"})
	require.ErrorContains(t, err, "applnk_pace_ms")
}

func TestLoadWith_RefusesSomethingThatIsNotAPair(t *testing.T) {
	_, err := LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), []string{"applink_pace_ms"})
	require.ErrorContains(t, err, "key=value")
}

func TestKeys_NamesEveryFieldAndDotsTheNestedOnes(t *testing.T) {
	keys := Keys()
	require.Subset(t, keys, []string{"data_dir", "applink_pace_ms", "silence", "resources.max_bytes", "ai.model"})
	require.NotContains(t, keys, "resources", "a section is not a key a value can be set on")
	// Every key Keys names must be one --set can actually reach, or the
	// completion offers what the flag then refuses.
	for _, k := range keys {
		require.NoError(t, applySets(new(Config), []string{k + "="}), k)
	}
}
