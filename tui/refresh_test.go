package tui

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/larkcli"
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

// A refresh that rides along with the beat must not reach the notice bar: the
// reader did not ask for it, so a red banner is the wrong place for a gateway
// that refused, and there is nothing to back off from it.
func TestRideAlongRefresh_LogsAFailureInsteadOfRaisingIt(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	f.Err = errors.New("429 too many requests")
	d := New(Deps{Store: st, Client: f, Syncer: &sync.Syncer{Store: st, Client: f}}).deps
	// Both refreshes stop at an empty chat, so the gateway is only reached
	// once there is something in it to ask about.
	_, err = st.UpsertMessages(t.Context(), []store.Message{{
		MessageID: "om_a", ChatID: "oc_open", MsgType: "text", SenderID: "ou_a", SenderType: "user",
		ContentRaw: `{"text":"hi"}`, CreateMs: 100, UpdateMs: 100, MessagePosition: 1}}, 1)
	require.NoError(t, err)

	require.Nil(t, refreshReadStatus(d, "oc_open")())
	require.Nil(t, refreshReactions(d, "oc_open")())
}
