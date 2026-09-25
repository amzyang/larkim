package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestRefreshReactions_AsksAboutTheNewestMessagesAndStoresTheAnswer(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	var msgs []store.Message
	for i := range reactionWindow + 5 {
		msgs = append(msgs, store.Message{MessageID: "om_" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			ChatID: "oc_team", MsgType: "text", CreateMs: clk.Now().UnixMilli() + int64(i), RawJSON: "{}"})
	}
	_, err := s.Store.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)

	newest := msgs[len(msgs)-1].MessageID
	f.Reactions[newest] = json.RawMessage(`{"counts":[{"reaction_type":"OK","count":"2"}]}`)

	n, err := s.RefreshReactions(ctx, "oc_team")
	require.NoError(t, err)
	require.Equal(t, reactionWindow, n, "one batch_query covers the window")

	got, err := s.Store.GetMessage(ctx, newest)
	require.NoError(t, err)
	require.JSONEq(t, `{"counts":[{"reaction_type":"OK","count":"2"}]}`, got.ReactionsJSON)

	oldest, err := s.Store.GetMessage(ctx, msgs[0].MessageID)
	require.NoError(t, err)
	require.Empty(t, oldest.ReactionsJSON, "a message outside the window is not asked about")
}

func TestRefreshReactions_ClearsASummaryFeishuNoLongerHolds(t *testing.T) {
	s, _, clk := newSyncer(t)
	ctx := context.Background()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "text", CreateMs: clk.Now().UnixMilli(), RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.Store.UpdateReactions(ctx, "om_a", `{"counts":[{"reaction_type":"OK","count":"1"}]}`))

	// Nothing in the fake answers for om_a: the only reaction was taken back.
	_, err = s.RefreshReactions(ctx, "oc_team")
	require.NoError(t, err)

	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Empty(t, got.ReactionsJSON, "a summary Feishu dropped is dropped here too")
}

func TestRefreshReactions_SaysNothingAboutAChatWithNoMessages(t *testing.T) {
	s, f, _ := newSyncer(t)
	n, err := s.RefreshReactions(context.Background(), "oc_quiet")
	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, f.Calls, "an empty chat costs no call")
}

// p2pChats registers n p2p chats, each holding one message, newest first.
func p2pChats(t *testing.T, s *Syncer, f *larkcli.Fake, clk *fakeClock, n int) []string {
	t.Helper()
	ctx := context.Background()
	var msgs []store.Message
	var ids []string
	for i := range n {
		chat := fmt.Sprintf("oc_peer%02d", i)
		id := fmt.Sprintf("om_p%02d", i)
		f.Chats = append(f.Chats, larkcli.RawChat{ChatID: chat, Name: "同事", ChatMode: "p2p",
			P2PTargetID: fmt.Sprintf("ou_%02d", i), P2PTargetType: "user"})
		msgs = append(msgs, store.Message{MessageID: id, ChatID: chat, MsgType: "text",
			CreateMs: clk.Now().UnixMilli() - int64(i), UpdateMs: clk.Now().UnixMilli() - int64(i), RawJSON: "{}"})
		ids = append(ids, id)
	}
	_, err := s.refreshChats(ctx, clk.Now())
	require.NoError(t, err)
	_, err = s.Store.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)
	f.Calls = nil
	return ids
}

func TestReactionsSlice_AsksAboutTheNewestMessageOfTheP2PChatsAlone(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_team", Name: "平台组", ChatMode: "group"}}
	p2pChats(t, s, f, clk, 1)
	now := clk.Now().UnixMilli()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_older", ChatID: "oc_peer00", MsgType: "text", CreateMs: now - 5000, UpdateMs: now - 5000, RawJSON: "{}"},
		{MessageID: "om_grp", ChatID: "oc_team", MsgType: "text", CreateMs: now, UpdateMs: now, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	f.Reactions["om_p00"] = json.RawMessage(`{"counts":[{"reaction_type":"OK","count":"1"}]}`)
	f.Calls = nil

	n, err := s.reactionsSlice(ctx, clk.Now())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"reaction-counts:om_p00"}, f.Calls,
		"one batched call; a group and an older message are no business of the chat list")

	c, err := s.Store.GetChat(ctx, "oc_peer00")
	require.NoError(t, err)
	require.JSONEq(t, `{"counts":[{"reaction_type":"OK","count":"1"}]}`, c.LastReactionsJSON)
}

func TestReactionsSlice_SkipsAChatWhoseNewestMessageWasRecalled(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	p2pChats(t, s, f, clk, 1)
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_p00", ChatID: "oc_peer00", MsgType: "text", Deleted: true,
			CreateMs: clk.Now().UnixMilli(), UpdateMs: clk.Now().UnixMilli(), RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	f.Calls = nil

	n, err := s.reactionsSlice(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, f.Calls, "a recall takes the reactions with the body")
}

func TestReactionsSlice_AsksNoMoreThanOneBatchPerTick(t *testing.T) {
	s, f, clk := newSyncer(t)
	ids := p2pChats(t, s, f, clk, reactionWindow+5)

	n, err := s.reactionsSlice(context.Background(), clk.Now())
	require.NoError(t, err)
	require.Equal(t, reactionWindow, n)
	require.Equal(t, []string{"reaction-counts:" + strings.Join(ids[:reactionWindow], ",")}, f.Calls,
		"the liveliest chats fill the batch; the rest wait for a tick where they are")
}

func TestReactionsSlice_AsksNoMoreOftenThanItsInterval(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.BackfillPerTick = 0
	s.Opt.RepairEvery = 0
	p2pChats(t, s, f, clk, 1)

	reactionCalls := func() int {
		n := 0
		for _, c := range f.Calls {
			if strings.HasPrefix(c, "reaction-counts:") {
				n++
			}
		}
		return n
	}

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, reactionCalls(), "the first tick asks")

	clk.t = clk.t.Add(reactionsEvery - time.Second)
	f.Calls = nil
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, reactionCalls(), "a tick inside the interval does not ask again")

	clk.t = clk.t.Add(reactionsEvery)
	f.Calls = nil
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, reactionCalls(), "past the interval it asks again")
}
