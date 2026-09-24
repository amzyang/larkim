package sync

import (
	"context"
	"encoding/json"
	"testing"

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
