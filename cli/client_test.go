package cli

import (
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/stretchr/testify/require"
)

// The lanes live inside the client, so two clients would be two pairs of
// lanes and the TUI's keystrokes would stop knowing about its own sweeps.
func TestApp_ClientIsSharedAcrossCallers(t *testing.T) {
	a := &App{cfg: config.Config{DataDir: t.TempDir()}}
	require.Same(t, a.client(), a.client())
	require.Same(t, a.client(), a.syncer(nil).Client)
}

func TestApp_ClientTakesTheConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark-cli")
	a := &App{cfg: config.Config{DataDir: t.TempDir(), LarkCLIPath: path}}
	require.Equal(t, path, a.client().Path)
}
