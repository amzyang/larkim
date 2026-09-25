package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestOpenLog_RollsOverWhenTheFileIsFull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "larkim.log")
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), maxLogBytes+1), 0o600))

	f, err := openLog(path)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Zero(t, st.Size(), "the full file was rolled aside, not appended to")
	rolled, err := os.Stat(path + ".1")
	require.NoError(t, err)
	require.Equal(t, int64(maxLogBytes+1), rolled.Size())
}

func TestOpenLog_AppendsToAFileUnderTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "larkim.log")
	require.NoError(t, os.WriteFile(path, []byte("kept\n"), 0o600))

	f, err := openLog(path)
	require.NoError(t, err)
	_, err = f.WriteString("added\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "kept\nadded\n", string(b))
	require.NoFileExists(t, path+".1")
}

// logApp builds an App logging into dir, for a command with the given
// annotations.
func logApp(t *testing.T, dir string, annotations map[string]string) (*App, *bytes.Buffer) {
	t.Helper()
	var errOut bytes.Buffer
	a := &App{Version: "test", Err: &errOut, cfg: config.Config{DataDir: dir}}
	a.initLog(&cobra.Command{Use: "x", Annotations: annotations})
	return a, &errOut
}

func TestInitLog_AltScreenCommandKeepsStderrClean(t *testing.T) {
	dir := t.TempDir()
	a, errOut := logApp(t, dir, map[string]string{altScreen: "true"})

	a.log.Warn("something went wrong")

	require.Empty(t, errOut.String(), "stderr is the alternate screen; nothing may be written over it")
	b, err := os.ReadFile(filepath.Join(dir, "larkim.log"))
	require.NoError(t, err)
	require.Contains(t, string(b), "something went wrong")
}

func TestNew_TUIIsMarkedAsOwningTheScreen(t *testing.T) {
	var tui *cobra.Command
	for _, c := range New("test", "").Commands() {
		if c.Name() == "tui" {
			tui = c
		}
	}
	require.NotNil(t, tui)
	require.Equal(t, "true", tui.Annotations[altScreen],
		"without the mark its logs would be written over the alternate screen")
}

func TestInitLog_OrdinaryCommandWritesBothPlaces(t *testing.T) {
	dir := t.TempDir()
	a, errOut := logApp(t, dir, nil)

	a.log.Warn("something went wrong")

	require.Contains(t, errOut.String(), "something went wrong")
	b, err := os.ReadFile(filepath.Join(dir, "larkim.log"))
	require.NoError(t, err)
	require.Contains(t, string(b), "something went wrong")
}

func TestInitLog_DebugGatesTheCallDetail(t *testing.T) {
	dir := t.TempDir()
	a, _ := logApp(t, dir, nil)
	a.log.Debug("lark-cli request")

	b, err := os.ReadFile(filepath.Join(dir, "larkim.log"))
	require.NoError(t, err)
	require.NotContains(t, string(b), "lark-cli request")

	a.debug = true
	a.initLog(&cobra.Command{Use: "x"})
	a.log.Debug("lark-cli request")

	b, err = os.ReadFile(filepath.Join(dir, "larkim.log"))
	require.NoError(t, err)
	require.Contains(t, string(b), "lark-cli request")
}

func TestInitLog_UnwritableLogFallsBackToStderr(t *testing.T) {
	// A data dir that is a file, not a directory, is as close as a test gets
	// to the read-only home this branch exists for.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	a, errOut := logApp(t, blocked, nil)

	a.log.Warn("still reported")

	require.Contains(t, errOut.String(), "log file unavailable")
	require.Contains(t, errOut.String(), "still reported", "the tool keeps working without its log")
}
