package sync

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func reactSyncer(t *testing.T) (*Syncer, *larkcli.Fake) {
	t.Helper()
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, f.Self.UserOpenID))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "text", CreateMs: clk.Now().UnixMilli(), RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	return s, f
}

func TestReact_AddsTheEmojiAndBringsTheSummaryBack(t *testing.T) {
	s, f := reactSyncer(t)
	ctx := context.Background()
	f.Reactions["om_a"] = json.RawMessage(`{"counts":[{"reaction_type":"OK","count":"1"}]}`)

	require.NoError(t, s.React(ctx, "om_a", "OK", true))
	require.Contains(t, f.Calls, "react:add:om_a:OK")

	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.JSONEq(t, `{"counts":[{"reaction_type":"OK","count":"1"}]}`, got.ReactionsJSON,
		"the pane shows what Feishu holds, not a guess made before the call")
}

func TestReact_LooksUpTheReactionIdBeforeTakingOneBack(t *testing.T) {
	// The stored summary carries no reaction id, and the delete needs one, so
	// taking a reaction back costs a lookup Feishu answers nowhere else.
	s, f := reactSyncer(t)
	ctx := context.Background()
	added, err := f.AddReaction(ctx, "om_a", "OK")
	require.NoError(t, err)
	f.Calls = nil

	require.NoError(t, s.React(ctx, "om_a", "OK", false))
	require.Equal(t, []string{"react:list:om_a:OK", "react:delete:om_a:" + added.ReactionID,
		"reaction-counts:om_a"}, f.Calls)
	require.Empty(t, f.Reacted["om_a"])
}

func TestReact_LeavesSomebodyElsesReactionAlone(t *testing.T) {
	s, f := reactSyncer(t)
	ctx := context.Background()
	f.Reacted["om_a"] = []larkcli.Reaction{{ReactionID: "rx_other", EmojiType: "OK", OperatorID: "ou_b"}}

	require.NoError(t, s.React(ctx, "om_a", "OK", false))
	require.NotContains(t, f.Calls, "react:delete:om_a:rx_other")
	require.Len(t, f.Reacted["om_a"], 1, "only what this identity added is ever deleted")
}
