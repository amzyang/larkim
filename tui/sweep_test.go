package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The data-dir lock names the sweep's owner and nothing else. Everything a
// reader reaches for — a reaction, a forward, older history, a cold search
// hit — names ids Feishu just answered for and upserts the same rows whoever
// asks, so the rest of this package's tests run in the configuration a TUI
// beside a daemon has: a puller, no sweep. These two are what the sweep still
// owns alone.

func TestNew_AlwaysHasAPullerToReachFeishuWith(t *testing.T) {
	require.NotNil(t, New(Deps{}).deps.Syncer,
		"a reader that does not own the sweep still pulls what it opens")
}

func TestRunCommand_SyncBelongsToTheSweepOwner(t *testing.T) {
	m := New(Deps{})
	next, cmd := m.runCommand("sync")
	require.Nil(t, cmd, "a tick moves the global cursors, which the daemon owns")
	require.Contains(t, next.(Model).notice, "daemon")

	m.deps.Embedded = true
	next, cmd = m.runCommand("sync")
	require.NotNil(t, cmd)
	require.Contains(t, next.(Model).notice, "syncing")
}
