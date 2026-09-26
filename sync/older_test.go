package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// atFloor gives oc_a a history floor at floorMs and fills Feishu with n
// messages a day apart ending just before it, none of them stored.
func atFloor(t *testing.T, s *Syncer, f *larkcli.Fake, floor time.Time, n int) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", floor.UnixMilli()))
	require.NoError(t, s.Store.SetChatBackfillDone(ctx, "oc_a", floor.UnixMilli(), floor.UnixMilli()))
	require.NoError(t, s.Store.SetChatCursor(ctx, "oc_a", floor.UnixMilli()))
	for i := range n {
		at := floor.Add(-time.Duration(i+1) * 24 * time.Hour)
		f.AddMessage(msg("om_old_"+string(rune('a'+i)), "oc_a", at, "older"))
	}
}

func TestPullOlder_MovesTheFloorBackOnePage(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	floor := clk.Now().AddDate(0, 0, -30)
	f.OlderPage = 2
	atFloor(t, s, f, floor, 5)

	n, err := s.PullOlder(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, 2, n, "one page, not the archive")
	require.Contains(t, f.Calls, "older:oc_a")
	require.NotContains(t, f.Calls, "list:chat:oc_a", "the whole point is not to re-list the window already stored")

	chat, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, floor.Add(-48*time.Hour).UnixMilli(), chat.HistoryFloorMs,
		"the floor lands on the oldest message of the page")
	require.Equal(t, floor.UnixMilli(), chat.CursorMs, "walking backwards says nothing about the newest message")
}

func TestPullOlder_WalksBackAPageAtATime(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.OlderPage = 2
	atFloor(t, s, f, clk.Now().AddDate(0, 0, -30), 5)

	// Pages overlap by their boundary message, so a five-message history
	// takes one page more than dividing it would suggest.
	for range 4 {
		_, err := s.PullOlder(ctx, "oc_a")
		require.NoError(t, err)
	}
	stored, err := s.Store.ListMessages(ctx, store.MessageQuery{ChatID: "oc_a", Limit: 100})
	require.NoError(t, err)
	require.Len(t, stored, 5, "paging back reaches the whole of the history")

	chat, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Zero(t, chat.HistoryFloorMs, "a page the server says is the last marks the chat complete")
}

func TestPullOlder_CompleteHistoryAsksNothing(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	atFloor(t, s, f, clk.Now().AddDate(0, 0, -30), 1)
	require.NoError(t, s.Store.SetChatHistoryFloor(ctx, "oc_a", 0))
	f.Calls = nil

	n, err := s.PullOlder(ctx, "oc_a")
	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, f.Calls, "a chat stored whole costs no call")
}

func TestPullOlder_BringsTheRepliesOfTheThreadsItFinds(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	floor := clk.Now().AddDate(0, 0, -30)
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", floor.UnixMilli()))
	require.NoError(t, s.Store.SetChatBackfillDone(ctx, "oc_a", floor.UnixMilli(), floor.UnixMilli()))

	root := msg("om_root", "oc_a", floor.Add(-24*time.Hour), "root")
	root.ThreadID = "omt_1"
	f.AddMessage(root)
	reply := msg("om_reply", "oc_a", floor.Add(-23*time.Hour), "reply")
	reply.ThreadID, reply.MessagePosition = "omt_1", -1
	f.AddMessage(reply)

	n, err := s.PullOlder(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, 2, n, "a root older than the floor brings its replies with it")
	require.Contains(t, f.Calls, "list:thread:omt_1")
}

func TestPullOlder_AFailedCallLeavesTheFloorWhereItWas(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	floor := clk.Now().AddDate(0, 0, -30)
	atFloor(t, s, f, floor, 3)
	f.ListErr = map[string]error{"oc_a": &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network"}}

	_, err := s.PullOlder(ctx, "oc_a")
	require.Error(t, err)

	chat, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, floor.UnixMilli(), chat.HistoryFloorMs, "a refused page is not the start of the chat")
}
