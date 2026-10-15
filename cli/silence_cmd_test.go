package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
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
	st, err := storetest.Open(t, filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	_, err = st.UpsertMessages(t.Context(), []store.Message{
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

// silenceAddFixture is a data dir whose store knows a chat holding a human's
// and a bot's message, and two chats sharing one name.
func silenceAddFixture(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	st, err := storetest.Open(t, filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	ctx := t.Context()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{
		{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group", RawJSON: "{}"},
		{ChatID: "oc_twin_a", Name: "项目协作群", ChatMode: "group", RawJSON: "{}"},
		{ChatID: "oc_twin_b", Name: "项目协作群", ChatMode: "group", RawJSON: "{}"},
	}, 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_human", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_a", CreateMs: 100, MessagePosition: 1},
		{MessageID: "om_noise", ChatID: "oc_quiet", MsgType: "text", SenderID: "cli_c", ContentRaw: `{"text":"Nightly build passed"}`, CreateMs: 200, MessagePosition: 2},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.Close())
	return dir, filepath.Join(dir, "config.yaml")
}

func TestSilenceAdd_WritesTheRuleAndReportsWhatItMatches(t *testing.T) {
	dir, cfgPath := silenceAddFixture(t)
	out, err := runCfg(t, dir, "# keep me\npoll_interval_ms: 3000\n",
		"silence", "add", "--chat", "平台组", "--sender", "cli_c", "--contains", "nightly")
	require.NoError(t, err)
	require.Contains(t, out, `"added": true`)
	require.Contains(t, out, `"chat": "oc_quiet"`, "a chat name is written as the id it resolves to")
	require.Contains(t, out, `"matched": 1`)
	require.Contains(t, out, `"last_match_ms": 200`)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Equal(t, store.SilenceRules{{Chat: "oc_quiet", Sender: "cli_c", Contains: "nightly"}}, cfg.Silence)
	require.Equal(t, 3000, cfg.PollIntervalMS)
	text, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(text), "# keep me", "the rest of the file is left as the reader wrote it")
}

func TestSilenceAdd_KeepsTheRulesAlreadyThere(t *testing.T) {
	dir, cfgPath := silenceAddFixture(t)
	_, err := runCfg(t, dir, "silence:\n  - sender: ou_b\n", "silence", "add", "--chat", "oc_quiet")
	require.NoError(t, err)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Equal(t, store.SilenceRules{{Sender: "ou_b"}, {Chat: "oc_quiet"}}, cfg.Silence)
}

func TestSilenceAdd_ARuleAlreadyThereIsNotWrittenTwice(t *testing.T) {
	dir, cfgPath := silenceAddFixture(t)
	body := "silence:\n  - sender: cli_c\n"
	out, err := runCfg(t, dir, body, "silence", "add", "--sender", "cli_c")
	require.NoError(t, err)
	require.Contains(t, out, `"added": false`)
	require.Contains(t, out, `"matched": 1`)
	text, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Equal(t, "data_dir: "+dir+"\n"+body, string(text), "nothing changed, so nothing is rewritten")
}

func TestSilenceAdd_RefusesARuleThatMatchesEverything(t *testing.T) {
	dir, cfgPath := silenceAddFixture(t)
	_, err := runCfg(t, dir, "", "silence", "add")
	require.ErrorContains(t, err, "set at least one of")
	_, err = runCfg(t, dir, "", "silence", "add", "--contains", "  ")
	require.ErrorContains(t, err, "set at least one of", "blank text is no text")
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Empty(t, cfg.Silence)
}

func TestSilenceAdd_RefusesAChatNameThatNamesNoOneChat(t *testing.T) {
	dir, cfgPath := silenceAddFixture(t)
	_, err := runCfg(t, dir, "", "silence", "add", "--chat", "no such chat")
	require.ErrorContains(t, err, `no chat named "no such chat"`)
	_, err = runCfg(t, dir, "", "silence", "add", "--chat", "项目协作群")
	require.ErrorContains(t, err, "2 chats named")
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Empty(t, cfg.Silence)
}

func TestSilenceAdd_LeavesASetOverrideOutOfTheFile(t *testing.T) {
	dir, cfgPath := silenceAddFixture(t)
	_, err := runCfg(t, dir, "", "--set", "silence_sync=true", "silence", "add", "--sender", "cli_c")
	require.NoError(t, err)
	text, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NotContains(t, string(text), "silence_sync", "--set lasts one run; add writes only the rule")
}
