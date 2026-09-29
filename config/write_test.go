package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// example copies the repository's reference file into a temp dir, which is the
// one config file with a comment above every key.
func example(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../config.example.yaml")
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, b, 0o644))
	return p
}

func TestSetFile_KeepsCommentsAndSiblings(t *testing.T) {
	p := example(t)
	before, err := Load(p)
	require.NoError(t, err)

	require.NoError(t, SetFile(p, "applink_pace_ms", "1500"))

	after, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, 1500, after.ApplinkPaceMS)
	after.ApplinkPaceMS = before.ApplinkPaceMS
	require.Equal(t, before, after, "no other key moved")

	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(text), "# Database, attachments and the daemon lock live here.")
	require.Contains(t, string(text), "# Skip attachments larger than this; 0 means unlimited.")
	require.Contains(t, string(text), "# Environment variable holding the Anthropic API key.")
}

func TestSetFile_KeepsTheCommentAboveTheKeyItRewrites(t *testing.T) {
	p := example(t)
	require.NoError(t, SetFile(p, "backfill_days", "7"))
	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(text), "# Bound on the initial history pull per chat.\nbackfill_days: 7")
}

func TestSetFile_WritesANestedKey(t *testing.T) {
	p := example(t)
	require.NoError(t, SetFile(p, "ai.model", "claude-sonnet-5"))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-5", cfg.AI.Model)
	require.Equal(t, "ANTHROPIC_API_KEY", cfg.AI.APIKeyEnv, "its siblings stay")
}

func TestSetFile_CreatesAMissingFileAndItsDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.yaml")
	require.NoError(t, SetFile(p, "resources.max_bytes", "1024"))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, int64(1024), cfg.Resources.MaxBytes)
	require.Equal(t, 30, cfg.BackfillDays, "a key the file omits is still the default")

	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "resources:\n  max_bytes: 1024\n", string(text), "only the key that was set")
}

func TestSetFile_AddsAKeyTheFileLacks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("# keep me\nbackfill_days: 7\n"), 0o644))
	require.NoError(t, SetFile(p, "overlap", "5m"))
	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(text), "# keep me")
	require.Contains(t, string(text), "overlap: 5m")
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, 7, cfg.BackfillDays)
}

func TestSetFile_RewritesAFileThatIsOnlyComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("# nothing set yet\n"), 0o644))
	require.NoError(t, SetFile(p, "active_top_k", "5"))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, 5, cfg.ActiveTopK)
}

func TestSetFile_KeepsTheFileMode(t *testing.T) {
	p := example(t)
	require.NoError(t, os.Chmod(p, 0o600))
	require.NoError(t, SetFile(p, "backfill_days", "7"))
	st, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestSetFile_LeavesNoTemporaryFileBehind(t *testing.T) {
	p := example(t)
	require.NoError(t, SetFile(p, "backfill_days", "7"))
	names, err := os.ReadDir(filepath.Dir(p))
	require.NoError(t, err)
	require.Len(t, names, 1)
	require.Equal(t, "config.yaml", names[0].Name())
}

func TestSetFile_EveryKeySurvivesARoundTrip(t *testing.T) {
	p := example(t)
	cfg, err := Load(p)
	require.NoError(t, err)
	before, err := os.ReadFile(p)
	require.NoError(t, err)

	for _, k := range Keys() {
		v, ok := cfg.Get(k)
		require.True(t, ok, k)
		require.NoError(t, SetFile(p, k, v), k)
	}

	after, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, cfg, after)
	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, commentLines(string(before)), commentLines(string(text)), "every comment survives")
}

func TestSetFileValue_WritesSilenceAsABlockList(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	rules := store.SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}, {Contains: "nightly build"}}
	require.NoError(t, SetFileValue(p, "silence", rules))

	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "silence:\n  - chat: oc_quiet\n    sender: cli_c\n  - contains: nightly build\n", string(text),
		"the form a reader writes by hand, with no empty field")
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, rules, cfg.Silence)
}

func TestSetFileValue_KeepsCommentsAndSiblings(t *testing.T) {
	p := example(t)
	before, err := Load(p)
	require.NoError(t, err)

	require.NoError(t, SetFileValue(p, "silence", store.SilenceRules{{Chat: "oc_quiet"}}))

	after, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, store.SilenceRules{{Chat: "oc_quiet"}}, after.Silence)
	after.Silence = before.Silence
	require.Equal(t, before, after, "no other key moved")
	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(text), "# `larkim silence` says how many messages each rule currently matches.")
}

func TestSetFileValue_WritesNoRulesAsAnEmptyList(t *testing.T) {
	p := example(t)
	require.NoError(t, SetFileValue(p, "silence", store.SilenceRules{{Chat: "oc_quiet"}}))
	require.NoError(t, SetFileValue(p, "silence", store.SilenceRules{}))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Empty(t, cfg.Silence)
	text, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(text), "silence: []")
}

func commentLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
			out = append(out, t)
		}
	}
	return out
}
