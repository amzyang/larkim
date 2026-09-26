package sync

import (
	"context"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// stakedThreadTick lays down a thread whose root is older than any window the
// tick lists, with its one reply held in the thread container alone. rootFrom
// is who wrote the root, which is what decides whether the reader has a stake.
func stakedThreadTick(t *testing.T, rootFrom string) (*Syncer, *larkcli.Fake, context.Context) {
	t.Helper()
	s, f, clk := newSyncer(t)
	ctx, now := context.Background(), clk.t
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", now.UnixMilli()))

	root := msg("om_root", "oc_a", now.Add(-60*24*time.Hour), "old topic")
	root.ThreadID, root.Sender.ID = "omt_x", rootFrom
	_, err := s.Store.UpsertMessages(ctx, []store.Message{ToRow(root)}, now.UnixMilli())
	require.NoError(t, err)

	reply := msg("om_reply", "oc_a", now.Add(-45*24*time.Hour), "late answer")
	reply.MessageID, reply.ThreadID, reply.MessagePosition = "om_reply", "omt_x", -3
	f.AddMessage(reply)
	return s, f, ctx
}

// A chat's listing carries a thread's root and none of its replies, and the
// sweep follows only the threads it saw a root for. Answering an old topic is
// what a thread is for, so the reader's own threads are asked after by name.
func TestTick_AsksAfterAStakedThreadWhoseRootIsOutOfEveryWindow(t *testing.T) {
	s, f, ctx := stakedThreadTick(t, "ou_me")

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Positive(t, callsTo(f, "list:thread:omt_x"))
	require.Positive(t, rep.Threads)

	got, err := s.Store.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.Equal(t, "omt_x", got.ThreadID)
}

// A thread nobody asked the reader about is somebody else's conversation.
func TestTick_LeavesAThreadTheReaderHasNoStakeInAlone(t *testing.T) {
	s, f, ctx := stakedThreadTick(t, "ou_a")

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, callsTo(f, "list:thread:omt_x"))
	require.Zero(t, rep.Threads)
}
