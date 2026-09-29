package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/larkmd"
	"github.com/stretchr/testify/require"
)

func TestLintCmd_ReadsAFileAndNamesEveryFinding(t *testing.T) {
	a, _ := bodySourceApp(t, "")
	path := filepath.Join(t.TempDir(), "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("## 周报\n\n@张三 看下 :DONE:\n"), 0o644))

	runBodyCmd(t, a, a.lintCmd(), path)

	out := a.Out.(*bytes.Buffer).String()
	require.Contains(t, out, "3:1 [mention_unresolved]")
	require.Contains(t, out, "[emoji_not_rendered]")
	require.Contains(t, out, "  hint: ")
}

func TestLintCmd_ReadsStdinWhenGivenNoFile(t *testing.T) {
	a, _ := bodySourceApp(t, "@张三 看下")
	runBodyCmd(t, a, a.lintCmd())
	require.Contains(t, a.Out.(*bytes.Buffer).String(), "mention_unresolved")
}

func TestLintCmd_SaysSoWhenThereIsNothingToReport(t *testing.T) {
	a, _ := bodySourceApp(t, "## 周报\n\n- 修复了 A\n- 修复了 B")
	runBodyCmd(t, a, a.lintCmd(), "-")
	require.Equal(t, "nothing to report\n", a.Out.(*bytes.Buffer).String())
}

func TestLintCmd_JSONAnswersWithAnArrayEvenWhenClean(t *testing.T) {
	a, _ := bodySourceApp(t, "就这样")
	a.jsonOut = true
	runBodyCmd(t, a, a.lintCmd(), "-")

	var got []larkmd.Finding
	require.NoError(t, json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &got))
	require.Empty(t, got)
	require.Contains(t, a.Out.(*bytes.Buffer).String(), "[]")
}

func TestLintCmd_JSONCarriesThePositionAndTheRule(t *testing.T) {
	a, _ := bodySourceApp(t, "@张三 看下")
	a.jsonOut = true
	runBodyCmd(t, a, a.lintCmd(), "-")

	var got []larkmd.Finding
	require.NoError(t, json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "mention_unresolved", got[0].Rule)
	require.Equal(t, 1, got[0].Line)
	require.Equal(t, 1, got[0].Column)
}

func TestLintCmd_AMissingFileIsAUserError(t *testing.T) {
	a, _ := bodySourceApp(t, "")
	cmd := a.lintCmd()
	cmd.SetOut(a.Out)
	cmd.SetErr(a.Err)
	cmd.SetArgs([]string{filepath.Join(t.TempDir(), "nope.md")})

	err := cmd.Execute()
	require.ErrorContains(t, err, "nope.md")
	require.False(t, reportable(err), "a mistyped path is the user's, not a defect")
}

func TestSendCmd_MarkdownWarningsGoToStderrAndTheSendStillHappens(t *testing.T) {
	a, f := bodySourceApp(t, "")
	a.jsonOut = true
	st, err := a.openStore()
	require.NoError(t, err)
	require.NoError(t, st.Close())

	msg, err := outgoingFlags{markdown: "@张三 看下"}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	a.warnMarkdown(msg)

	require.Contains(t, a.Err.(*bytes.Buffer).String(), "larkim: 1:1 [mention_unresolved]")
	require.Empty(t, a.Out.(*bytes.Buffer).String(), "stdout stays whatever the command prints there")
}

func TestSendCmd_WarnMarkdownIsSilentForATextBody(t *testing.T) {
	a, _ := bodySourceApp(t, "")
	a.warnMarkdown(larkcli.Text("@张三 看下"))
	require.Empty(t, a.Err.(*bytes.Buffer).String(), "text is sent verbatim, so nothing in it is markdown")
}

func TestReadSource_NamesWhatTheReaderTyped(t *testing.T) {
	_, err := readSource("--markdown @~/nope.md", "~/nope.md", strings.NewReader(""))
	require.ErrorContains(t, err, "--markdown @~/nope.md:")

	_, err = readSource("-", "-", strings.NewReader("   \n"))
	require.ErrorContains(t, err, "- holds no message")
}
