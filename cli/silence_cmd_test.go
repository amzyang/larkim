package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// runCfg executes the CLI against a written config and returns stdout.
func runCfg(t *testing.T, dir, cfgBody string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := New("test", "")
	root.SetOut(&out)
	root.SetErr(&errOut)
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("data_dir: "+dir+"\n"+cfgBody), 0o600))
	root.SetArgs(append([]string{"--config", cfg, "--json"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestSilenceCmd_ReportsPerRuleMatches(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	_, err = st.UpsertMessages(context.Background(), []store.Message{
		{MessageID: "om_human", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_a", CreateMs: 100, MessagePosition: 1},
		{MessageID: "om_noise", ChatID: "oc_quiet", MsgType: "text", SenderID: "cli_c", CreateMs: 200, MessagePosition: 2},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.Close())

	out, err := runCfg(t, dir, "silence:\n  - sender: cli_c\n  - sender: cli_typo\n", "silence")
	require.NoError(t, err)
	require.Contains(t, out, `"matched": 1`)
	require.Contains(t, out, `"matched": 0`, "a rule that matches nothing is the point of the command")
	require.Contains(t, out, `"last_match_ms": 200`)
}

func TestSilenceCmd_RejectsARuleThatMatchesEverything(t *testing.T) {
	dir := t.TempDir()
	_, err := runCfg(t, dir, "silence:\n  - contains: \"\"\n", "silence")
	require.ErrorContains(t, err, "set at least one of")
}
