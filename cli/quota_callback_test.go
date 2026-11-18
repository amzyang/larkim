package cli

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuotaCallback_RejectsInvalidEventWithoutGUI(t *testing.T) {
	t.Parallel()
	var out, logs bytes.Buffer
	a := &App{log: slog.New(slog.NewJSONHandler(&logs, nil)), Out: &out, Err: &bytes.Buffer{}, In: bytes.NewBufferString(`{"version":1,"job_id":1}`)}
	cmd := a.quotaCallbackCmd()
	cmd.SetArgs([]string{"--chat-id", "oc_quiet", "--sender-id", "ou_bot", "--check"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), `"status":"failed"`)
	require.Contains(t, logs.String(), `"msg":"quota callback done"`)
	require.Contains(t, logs.String(), `"job_id":1`)
	require.Contains(t, logs.String(), `"dur_ms":`)
	require.Contains(t, logs.String(), `"status":"failed"`)
}

func TestQuotaCallback_RequiresExplicitScope(t *testing.T) {
	t.Parallel()
	a := &App{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, In: bytes.NewBufferString(`{}`)}
	cmd := a.quotaCallbackCmd()
	cmd.SetArgs(nil)
	require.Error(t, cmd.Execute())
}
