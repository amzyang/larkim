package cli

import (
	"io"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// sub finds a direct subcommand of root by name.
func sub(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	require.FailNowf(t, "missing command", "no %q under %q", name, root.Name())
	return nil
}

func TestNew_BareCommandRunsTheTUI(t *testing.T) {
	root := New("test", "")

	require.True(t, root.Runnable(), "typing larkim alone must open the TUI")
	require.Equal(t, reflect.ValueOf(sub(t, root, "tui").RunE).Pointer(), reflect.ValueOf(root.RunE).Pointer(),
		"the bare command and `larkim tui` must be the same entry point")
}

func TestNew_BareCommandRejectsAnUnknownSubcommand(t *testing.T) {
	root := New("test", "")
	root.SetArgs([]string{"nope"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err := root.Execute()

	require.ErrorContains(t, err, `unknown command "nope"`,
		"a typo must not be swallowed as an argument the TUI ignores")
}
