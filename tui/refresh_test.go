package tui

import (
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/stretchr/testify/require"
)

func newRefreshModel(t *testing.T) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := New(Deps{Store: st, Syncer: &sync.Syncer{Store: st}})
	m.chatID = "oc_open"
	return m
}

func TestClaimChatRefresh_OnlyForTheChatStillOpen(t *testing.T) {
	m := newRefreshModel(t)
	require.False(t, m.claimChatRefresh("oc_scrolled_past"))
	require.True(t, m.claimChatRefresh("oc_open"))
	require.True(t, m.claimChatRefresh("oc_open"), "repeats are the beat's to pace, not this one's")
}

func TestOpenChat_ArmsTheChatRefresh(t *testing.T) {
	m := newRefreshModel(t)
	got := collect(m.openChat("oc_other"))
	require.Contains(t, got, "tui.messagesLoadedMsg")
	require.Contains(t, got, "tui.chatRefreshDueMsg")
}
