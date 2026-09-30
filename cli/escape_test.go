package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// runPlain is run without --json: the human-facing output, which is what the
// terminal interprets.
func runPlain(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := New("test", "")
	root.SetOut(&out)
	root.SetErr(&errOut)
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("data_dir: "+dir+"\n"), 0o600))
	root.SetArgs(append([]string{"--config", cfg}, args...))
	err := root.Execute()
	return out.String(), err
}

// escapeFixture is one chat and one message whose every sender-filled field
// carries a sequence a terminal would act on: kitty's remote control, which
// runs commands where allow_remote_control is on, a clipboard write, a window
// title and a screen clear.
func escapeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	ctx := t.Context()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{
		{ChatID: "oc_a", Name: "平台组\x1b]0;pwned\x07", ChatMode: "group"},
	}, 1))
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_a", Name: "张三\x1b[2J", Email: "zhangsan@example.com"},
	}, 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{
		MessageID: "om_a", ChatID: "oc_a", CreateMs: 10, MessagePosition: 1,
		MsgType: "text", SenderID: "ou_a", SenderType: "user",
		SenderName: "张三\x1b]52;c;cGF5bG9hZA==\x07",
		RawJSON:    "{}", ContentRaw: `{"text":"x"}`,
	}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_a",
		"hi\x1bP@kitty-cmd{\"cmd\":\"launch\"}\x1b\\there", "", 1))
	require.NoError(t, st.Close())
	return dir
}

// Nothing larkim prints for a person to read may carry an escape sequence: the
// text is a sender's, and the terminal obeys what it is written.
func TestCommands_PrintNoSendersEscapeSequences(t *testing.T) {
	dir := escapeFixture(t)
	for _, args := range [][]string{
		{"messages", "list"},
		{"messages", "show", "om_a"},
		{"chats", "list"},
		{"contacts", "list"},
	} {
		t.Run(args[0]+" "+args[1], func(t *testing.T) {
			out, err := runPlain(t, dir, args...)
			require.NoError(t, err)
			require.NotContains(t, out, "\x1b")
			require.NotContains(t, out, "\x07")
		})
	}
}

// The text is still shown; it is the sequences around it that are dropped.
func TestMessagesShow_KeepsTheTextItScrubbed(t *testing.T) {
	out, err := runPlain(t, escapeFixture(t), "messages", "show", "om_a")
	require.NoError(t, err)
	// The sequence goes whole, payload with it; the prose around it stays.
	require.Contains(t, out, "hithere")
	require.NotContains(t, out, "kitty-cmd")
	require.Contains(t, out, "张三")
}
