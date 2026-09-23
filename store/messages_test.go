package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnrenderedSystemMessages_TakeTheirOwnQueue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_text", ChatID: "oc", MsgType: "text", CreateMs: 30, ContentRaw: `{"text":"hi"}`, RawJSON: "{}"},
		{MessageID: "om_sys", ChatID: "oc", MsgType: "system", CreateMs: 20, ContentRaw: `{"template":"{from_user} left"}`, RawJSON: "{}"},
		{MessageID: "om_gone", ChatID: "oc", MsgType: "system", CreateMs: 10, Deleted: true, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	ids, err := s.UnrenderedMessageIDs(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"om_text"}, ids, "system messages never reach the renderer")

	pending, err := s.UnrenderedSystemMessages(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []PendingSystemMessage{{MessageID: "om_sys", ContentRaw: `{"template":"{from_user} left"}`}}, pending)

	require.NoError(t, s.UpdateRendered(ctx, "om_sys", "A left", "", "", 2))
	pending, _ = s.UnrenderedSystemMessages(ctx, 10)
	require.Empty(t, pending)
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
