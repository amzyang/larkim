package tui

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/stretchr/testify/require"
)

func newChatPollModel(t *testing.T) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := New(Deps{Store: st, Syncer: &sync.Syncer{Store: st}})
	m.chatID = "oc_open"
	m.focused = true
	return m
}

func TestClaimChatPoll_NamesTheOpenChat(t *testing.T) {
	m := newChatPollModel(t)
	require.Equal(t, "oc_open", m.claimChatPoll(time.Unix(1000, 0)))
}

func TestClaimChatPoll_RefusesWhileTheTerminalIsBlurred(t *testing.T) {
	m := newChatPollModel(t)
	m.focused = false
	require.Empty(t, m.claimChatPoll(time.Unix(1000, 0)), "nobody is reading it")
}

func TestClaimChatPoll_RefusesWhileACallIsOut(t *testing.T) {
	m := newChatPollModel(t)
	m.chatPollInFlight = true
	require.Empty(t, m.claimChatPoll(time.Unix(1000, 0)), "a slow call costs a beat, not a queue")
}

func TestNotePollResult_StandsDownWhenTheGatewayRefuses(t *testing.T) {
	m := newChatPollModel(t)
	now := time.Unix(1000, 0)
	m.chatPollInFlight = true

	m.notePollResult(&larkcli.Error{Subtype: "rate_limit"}, now)
	require.False(t, m.chatPollInFlight)
	require.Empty(t, m.claimChatPoll(now), "the beat stands down")
	require.Empty(t, m.claimChatPoll(now.Add(chatPollBackoff-time.Second)))
	require.Equal(t, "oc_open", m.claimChatPoll(now.Add(chatPollBackoff)))
}

func TestNotePollResult_AnOrdinaryFailureOnlyFreesTheNextBeat(t *testing.T) {
	m := newChatPollModel(t)
	now := time.Unix(1000, 0)
	m.chatPollInFlight = true

	m.notePollResult(errors.New("lark-cli exploded"), now)
	require.Equal(t, "oc_open", m.claimChatPoll(now), "one bad call does not stop the chain")
}

func TestChatPollDue_ReArmsEvenWhenItPollsNothing(t *testing.T) {
	m := newChatPollModel(t)
	m.focused = false // nothing to poll this beat

	next, cmd := m.Update(chatPollDueMsg{})
	require.False(t, next.(Model).chatPollInFlight)
	require.Contains(t, collect(cmd), "tui.chatPollDueMsg",
		"the chain must survive a blurred beat or focus never resumes it")
}

func TestFocusMsg_PollsAtOnceRatherThanWaitingOutABeat(t *testing.T) {
	m := newChatPollModel(t)
	m.focused = false

	next, cmd := m.Update(tea.FocusMsg{})
	require.True(t, next.(Model).chatPollInFlight)
	require.NotNil(t, cmd)
}
