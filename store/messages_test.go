package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnrenderedLocalMessages_TakeTheirOwnQueue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	call := `{"topic":"站会的视频会议","meet_number":"100000000","start_time":"1000"}`
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_text", ChatID: "oc", MsgType: "text", CreateMs: 30, ContentRaw: `{"text":"hi"}`, RawJSON: "{}"},
		{MessageID: "om_sys", ChatID: "oc", MsgType: "system", CreateMs: 20, ContentRaw: `{"template":"{from_user} left"}`, RawJSON: "{}"},
		{MessageID: "om_call", ChatID: "oc_v", MsgType: "video_chat", CreateMs: 15, ContentRaw: call, RawJSON: "{}"},
		{MessageID: "om_gone", ChatID: "oc", MsgType: "system", CreateMs: 10, Deleted: true, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	ids, err := s.UnrenderedMessageIDs(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_text"}, ids, "the messages larkim renders itself never reach lark-cli")

	pending, err := s.UnrenderedLocalMessages(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []PendingLocalMessage{
		{MessageID: "om_sys", MsgType: "system", ContentRaw: `{"template":"{from_user} left"}`, CreateMs: 20},
		{MessageID: "om_call", MsgType: "video_chat", ContentRaw: call, CreateMs: 15, CallRaw: call},
	}, pending)

	require.NoError(t, s.UpdateRendered(ctx, "om_sys", "A left", "", "", 2))
	require.NoError(t, s.UpdateRendered(ctx, "om_call", "[Video call]", "", "", 2))
	pending, _ = s.UnrenderedLocalMessages(ctx, 10)
	require.Empty(t, pending)
}

func TestUnrenderedLocalMessages_CarryTheCallThatRanBeforeThem(t *testing.T) {
	// Feishu closes a call with a system message whose body says nothing; the
	// length is on the video_chat message the call left behind, so the queue
	// hands both to the renderer together.
	s := openTest(t)
	ctx := context.Background()
	call := `{"topic":"x","start_time":"1000","end_time":"33000"}`
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_call", ChatID: "oc_a", MsgType: "video_chat", CreateMs: 1000, ContentRaw: call, RawJSON: "{}"},
		{MessageID: "om_end", ChatID: "oc_a", MsgType: "system", CreateMs: 33900, ContentRaw: `{"template":" "}`, RawJSON: "{}"},
		{MessageID: "om_elsewhere", ChatID: "oc_b", MsgType: "system", CreateMs: 33900, ContentRaw: `{"template":" "}`, RawJSON: "{}"},
		{MessageID: "om_before", ChatID: "oc_a", MsgType: "system", CreateMs: 500, ContentRaw: `{"template":" "}`, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	pending, err := s.UnrenderedLocalMessages(ctx, 10)
	require.NoError(t, err)
	got := map[string]string{}
	for _, m := range pending {
		got[m.MessageID] = m.CallRaw
	}
	require.Equal(t, call, got["om_end"])
	require.Empty(t, got["om_elsewhere"], "a call in another chat is not this marker's")
	require.Empty(t, got["om_before"], "a call that had not started yet cannot have ended")
}

func TestMessagesByIDs_SkipsWhatTheStoreNeverSaw(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_a", ChatID: "oc", MsgType: "text", CreateMs: 10, RawJSON: "{}"},
		{MessageID: "om_gone", ChatID: "oc", MsgType: "text", CreateMs: 20, Deleted: true, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	got, err := s.MessagesByIDs(ctx, []string{"om_a", "om_gone", "om_never"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "oc", got["om_a"].ChatID)
	require.True(t, got["om_gone"].Deleted, "a recalled message still answers for the quote above its reply")
	require.NotContains(t, got, "om_never")

	empty, err := s.MessagesByIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestUpdateReactions_LeavesTheRenderingAlone(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_a", ChatID: "oc", MsgType: "text", CreateMs: 10, ContentRaw: `{"text":"hi"}`, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.UpdateRendered(ctx, "om_a", "hi", `[{"id":"ou_a"}]`, "", 2))

	const block = `{"counts":[{"reaction_type":"OK","count":"1"}]}`
	require.NoError(t, s.UpdateReactions(ctx, "om_a", block))

	got, err := s.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, block, got.ReactionsJSON)
	require.Equal(t, "hi", got.Content, "the body is not re-rendered")
	require.Equal(t, `[{"id":"ou_a"}]`, got.MentionsJSON)
	require.EqualValues(t, 2, got.RenderedAt, "the message does not go back into the render queue")
}

func TestUpdateReactions_DoesNotAdvanceTheRevisionForAnUnchangedSummary(t *testing.T) {
	// Every open pane reloads on a revision bump, and a chat is re-asked about
	// on every visit, so re-stating what the row already holds must be silent.
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_a", ChatID: "oc", MsgType: "text", CreateMs: 10, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	const block = `{"counts":[{"reaction_type":"OK","count":"1"}]}`
	require.NoError(t, s.UpdateReactions(ctx, "om_a", block))
	changed, err := s.DataRev(ctx)
	require.NoError(t, err)

	require.NoError(t, s.UpdateReactions(ctx, "om_a", block))
	again, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Equal(t, changed, again, "the same summary written twice moves nothing")

	require.NoError(t, s.UpdateReactions(ctx, "om_a", ""))
	cleared, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, cleared, again, "a reaction taken back is a change the panes must see")
}
