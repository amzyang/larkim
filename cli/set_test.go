package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runRoot drives the real root command, which is where --set is folded into
// the configuration every command then reads.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	root := New("test", "")
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	// The startup log line shares this buffer; what a command printed is
	// the last of it.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	return lines[len(lines)-1], err
}

func TestSet_ReachesTheConfigEveryCommandReads(t *testing.T) {
	dir := t.TempDir()
	out, err := runRoot(t, "--set", "data_dir="+dir, "db", "path")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "larkim.db"), out)
}

func TestSet_NamesAKeyTheConfigHasNot(t *testing.T) {
	_, err := runRoot(t, "--set", "aplink_pace_ms=1500", "db", "path")
	require.ErrorContains(t, err, "aplink_pace_ms")
}

func TestSet_IsRepeatableAndAppliesInOrder(t *testing.T) {
	first, last := t.TempDir(), t.TempDir()
	out, err := runRoot(t, "--set", "data_dir="+first, "--set", "poll_interval_ms=1500", "--set", "data_dir="+last, "db", "path")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(last, "larkim.db"), out)
}
