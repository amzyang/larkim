package presence

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// shortDir is a socket directory short enough for sun_path: t.TempDir under
// macOS's /var/folders runs past the 104 bytes a unix socket path may take.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lp")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "tui")
}

func TestPresence_StateReportsFocusAndOpenSwitchesChat(t *testing.T) {
	dir := shortDir(t)
	live := &Live{KittyWindowID: "7", KittyListenOn: "unix:/tmp/kitty-1"}
	live.Set(true, "oc_quiet")
	opened := make(chan string, 1)
	srv, err := Listen(dir)
	require.NoError(t, err)
	go func() { _ = srv.Serve(t.Context(), live, func(chat string) { opened <- chat }) }()

	peers, err := Find(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, os.Getpid(), peers[0].PID)
	require.True(t, peers[0].Focused)
	require.Equal(t, "oc_quiet", peers[0].Chat)
	require.Equal(t, "7", peers[0].KittyWindowID)
	require.Equal(t, "unix:/tmp/kitty-1", peers[0].KittyListenOn)

	live.Set(false, "oc_elsewhere")
	peers, err = Find(t.Context(), dir)
	require.NoError(t, err)
	require.False(t, peers[0].Focused, "the state is read per request, not at start")

	require.NoError(t, Open(t.Context(), peers[0], "oc_ask"))
	select {
	case chat := <-opened:
		require.Equal(t, "oc_ask", chat)
	case <-time.After(2 * time.Second):
		t.Fatal("open never reached the TUI")
	}
}

func TestPresence_RemovesStaleSockets(t *testing.T) {
	dir := shortDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// A socket nobody listens on any more: what a TUI that crashed leaves.
	stale := filepath.Join(dir, "99999.sock")
	l, err := net.Listen("unix", stale)
	require.NoError(t, err)
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, l.Close())
	require.FileExists(t, stale)

	peers, err := Find(t.Context(), dir)
	require.NoError(t, err)
	require.Empty(t, peers)
	require.NoFileExists(t, stale)
}

func TestPresence_ServeRemovesItsSocketOnExit(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv, err := Listen(dir)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, &Live{}, func(string) {}) }()
	matches, _ := filepath.Glob(filepath.Join(dir, "*.sock"))
	require.Len(t, matches, 1)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "only this user reaches the sockets")

	cancel()
	require.NoError(t, <-done)
	matches, _ = filepath.Glob(filepath.Join(dir, "*.sock"))
	require.Empty(t, matches, "closing the listener unlinks its file")
}

func TestPresence_FindOnAMissingDirectoryIsNobody(t *testing.T) {
	peers, err := Find(t.Context(), filepath.Join(shortDir(t), "none"))
	require.NoError(t, err)
	require.Empty(t, peers)
}
