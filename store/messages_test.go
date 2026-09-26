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

	ids, err := s.UnrenderedMessageIDs(ctx, "", 10)
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

func TestThreadGists_CountsTheRepliesAndTakesTheNewest(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_root", ChatID: "oc_a", MsgType: "text", CreateMs: 100, MessagePosition: 5,
			ThreadID: "omt_1", ContentRaw: `{"text":"hello"}`, RawJSON: "{}"},
		{MessageID: "om_r1", ChatID: "oc_a", MsgType: "text", CreateMs: 110, MessagePosition: -1,
			ThreadID: "omt_1", ContentRaw: `{"text":"先"}`, SenderName: "李四", RawJSON: "{}"},
		{MessageID: "om_r2", ChatID: "oc_a", MsgType: "text", CreateMs: 120, MessagePosition: -3,
			ThreadID: "omt_1", ContentRaw: `{"text":"1234"}`, SenderName: "王五", RawJSON: "{}"},
		{MessageID: "om_gone", ChatID: "oc_a", MsgType: "text", CreateMs: 130, MessagePosition: -4,
			ThreadID: "omt_1", ContentRaw: `{"text":"撤了"}`, Deleted: true, RawJSON: "{}"},
		{MessageID: "om_quiet", ChatID: "oc_a", MsgType: "text", CreateMs: 140, MessagePosition: 6,
			ThreadID: "omt_2", ContentRaw: `{"text":"没人回"}`, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	got, err := s.ThreadGists(ctx, []string{"omt_1", "omt_2"}, "ou_me")
	require.NoError(t, err)
	require.Equal(t, 2, got["omt_1"].Replies, "the root is not a reply, and a recalled one is gone")
	require.Equal(t, "王五", got["omt_1"].SenderName, "a thread is alive, so the newest word is its state")
	require.Equal(t, `{"text":"1234"}`, got["omt_1"].ContentRaw)
	require.NotContains(t, got, "omt_2", "a thread with nothing in it has no line to draw from here")
}

func TestThreadGists_WaitingOnlyForAThreadIAmIn(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	// omt_1: I spoke in it. omt_2: somebody @'d me. omt_3: neither.
	rows := []Message{
		{MessageID: "om_r1", ChatID: "oc_a", MsgType: "text", CreateMs: 110, MessagePosition: -1,
			ThreadID: "omt_1", SenderID: "ou_me", ContentRaw: `{"text":"我回过"}`, RawJSON: "{}"},
		{MessageID: "om_r2", ChatID: "oc_a", MsgType: "text", CreateMs: 120, MessagePosition: -2,
			ThreadID: "omt_1", SenderID: "ou_x", ContentRaw: `{"text":"新的"}`, RawJSON: "{}"},
		{MessageID: "om_r3", ChatID: "oc_a", MsgType: "text", CreateMs: 130, MessagePosition: -1,
			ThreadID: "omt_2", SenderID: "ou_x", ContentRaw: `{"text":"@我"}`, RawJSON: "{}"},
		{MessageID: "om_r4", ChatID: "oc_a", MsgType: "text", CreateMs: 140, MessagePosition: -1,
			ThreadID: "omt_3", SenderID: "ou_x", ContentRaw: `{"text":"与我无关"}`, RawJSON: "{}"},
	}
	_, err := s.UpsertMessages(ctx, rows, 1)
	require.NoError(t, err)
	require.NoError(t, s.UpdateRendered(ctx, "om_r3", "@林岚 看下", `[{"key":"@_user_1","id":"ou_me","name":"林岚"}]`, "", 2))
	for _, r := range rows {
		markUnreadMsg(t, s, r.MessageID)
	}

	got, err := s.ThreadGists(ctx, []string{"omt_1", "omt_2", "omt_3"}, "ou_me")
	require.NoError(t, err)
	require.True(t, got["omt_1"].Waiting, "I took a turn in it")
	require.True(t, got["omt_2"].Waiting, "it called my name")
	require.False(t, got["omt_3"].Waiting,
		"a thread nobody asked me about is somebody else's conversation")
}

func TestThreadGists_ASilencedOrReadReplyIsNotWaiting(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	quiet := Message{MessageID: "om_quiet", ChatID: "oc_a", MsgType: "text", CreateMs: 110,
		MessagePosition: -1, ThreadID: "omt_1", SenderID: "ou_me", ContentRaw: `{"text":"我回过"}`, RawJSON: "{}"}
	_, err := s.UpsertMessages(ctx, []Message{quiet}, 1)
	require.NoError(t, err)
	markUnreadMsg(t, s, "om_quiet")
	got, err := s.ThreadGists(ctx, []string{"omt_1"}, "ou_me")
	require.NoError(t, err)
	require.True(t, got["omt_1"].Waiting)

	_, err = s.db.ExecContext(ctx, `UPDATE messages SET silenced = 1 WHERE message_id = 'om_quiet'`)
	require.NoError(t, err)
	got, _ = s.ThreadGists(ctx, []string{"omt_1"}, "ou_me")
	require.False(t, got["omt_1"].Waiting, "silence is what unreadCounted already says")

	require.NoError(t, s.MarkThreadRead(ctx, "omt_1", 5000))
	_, err = s.db.ExecContext(ctx, `UPDATE messages SET silenced = 0 WHERE message_id = 'om_quiet'`)
	require.NoError(t, err)
	got, _ = s.ThreadGists(ctx, []string{"omt_1"}, "ou_me")
	require.False(t, got["omt_1"].Waiting, "and a settled reply is settled whether it was silenced or not")
}

// markUnreadMsg gives a message a read_state row Feishu still reports unread.
func markUnreadMsg(t *testing.T, s *Store, id string) {
	t.Helper()
	unread := false
	require.NoError(t, s.SetReadStatus(t.Context(), id, &unread, 1, 0))
}
