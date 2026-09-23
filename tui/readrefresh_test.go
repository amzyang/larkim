package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/stretchr/testify/require"
)

func newReadRefreshModel(t *testing.T) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := New(Deps{Store: st, Syncer: &sync.Syncer{Store: st}})
	m.chatID = "oc_open"
	return m
}

func TestClaimReadRefresh_OnlyForTheChatStillOpen(t *testing.T) {
	m := newReadRefreshModel(t)
	now := time.Unix(1000, 0)
	require.False(t, m.claimReadRefresh("oc_scrolled_past", now))
	require.True(t, m.claimReadRefresh("oc_open", now))
}

func TestClaimReadRefresh_HoldsOffUntilTheCooldownPasses(t *testing.T) {
	m := newReadRefreshModel(t)
	now := time.Unix(1000, 0)
	require.True(t, m.claimReadRefresh("oc_open", now))
	require.False(t, m.claimReadRefresh("oc_open", now.Add(readRefreshCooldown-time.Second)))
	require.True(t, m.claimReadRefresh("oc_open", now.Add(readRefreshCooldown)))
}

func TestClaimReadRefresh_NotWhenADaemonOwnsTheWrites(t *testing.T) {
	m := newReadRefreshModel(t)
	m.deps.Syncer = nil
	require.False(t, m.claimReadRefresh("oc_open", time.Unix(1000, 0)))
}

func TestOpenChat_ArmsTheReadRefresh(t *testing.T) {
	m := newReadRefreshModel(t)
	got := collect(m.openChat("oc_other"))
	require.Contains(t, got, "tui.messagesLoadedMsg")
	require.Contains(t, got, "tui.readRefreshDueMsg")
}
