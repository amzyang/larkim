package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// msgAt builds a main-flow message; position is what keeps it out of, or in,
// the chat's newest-message summary.
func msgAt(id, chatID string, createMs int64, position int64, text string) Message {
	return Message{
		MessageID: id, ChatID: chatID, MsgType: "text", SenderID: "ou_a", SenderType: "user",
		SenderName: "Alice", ContentRaw: `{"text":"` + text + `"}`,
		CreateMs: createMs, UpdateMs: createMs, MessagePosition: position,
	}
}

func summaryOf(t *testing.T, s *Store, chatID string) Chat {
	t.Helper()
	c, err := s.GetChat(context.Background(), chatID)
	require.NoError(t, err)
	return c
}

func TestUpsertMessages_ColdStoresTheNewestMessage(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))

	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_1", "oc_a", 100, 1, "older"),
		msgAt("om_2", "oc_a", 200, 2, "newest"),
	}, 1)
	require.NoError(t, err)

	c := summaryOf(t, s, "oc_a")
	require.Equal(t, "om_2", c.LastMessageID)
	require.Equal(t, int64(200), c.LastMessageMs)
	require.Equal(t, "Alice", c.LastSenderName)
	require.Equal(t, "text", c.LastMsgType)
	require.Equal(t, `{"text":"newest"}`, c.LastContentRaw)
	require.Zero(t, c.LastRenderedAt, "rendering has not run yet")
}

func TestUpsertMessages_SkipsThreadReplies(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))

	root := msgAt("om_root", "oc_a", 100, 1, "root")
	root.ThreadID = "omt_1"
	reply := msgAt("om_reply", "oc_a", 300, -3, "reply in thread")
	reply.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{root, reply}, 1)
	require.NoError(t, err)

	c := summaryOf(t, s, "oc_a")
	require.Equal(t, "om_root", c.LastMessageID, "a thread reply is newer but off the main flow")
	require.Equal(t, int64(100), c.LastMessageMs)
}

func TestUpdateRendered_ReachesTheChatSummary(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)

	require.NoError(t, s.UpdateRendered(ctx, "om_1", "hello there", "", "", 42))

	c := summaryOf(t, s, "oc_a")
	require.Equal(t, "hello there", c.LastContent)
	require.Equal(t, int64(42), c.LastRenderedAt)
}

func TestUpdateRendered_LeavesOlderMessagesOutOfTheSummary(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_1", "oc_a", 100, 1, "older"),
		msgAt("om_2", "oc_a", 200, 2, "newest"),
	}, 1)
	require.NoError(t, err)

	require.NoError(t, s.UpdateRendered(ctx, "om_1", "rendering of the older one", "", "", 42))

	c := summaryOf(t, s, "oc_a")
	require.Equal(t, "om_2", c.LastMessageID)
	require.Empty(t, c.LastContent, "the older message's rendering must not leak into the summary")
}

func TestUpsertMessages_CarriesEditsAndRecallsIntoTheSummary(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)
	require.NoError(t, s.UpdateRendered(ctx, "om_1", "hi", "", "", 10))

	edited := msgAt("om_1", "oc_a", 100, 1, "hi, edited")
	edited.UpdateMs = 150
	edited.Updated = true
	_, err = s.UpsertMessages(ctx, []Message{edited}, 2)
	require.NoError(t, err)

	c := summaryOf(t, s, "oc_a")
	require.Equal(t, `{"text":"hi, edited"}`, c.LastContentRaw)
	require.Zero(t, c.LastRenderedAt, "an edit resets rendering, and the summary follows")

	recalled := msgAt("om_1", "oc_a", 100, 1, "hi, edited")
	recalled.UpdateMs = 150
	recalled.Deleted = true
	_, err = s.UpsertMessages(ctx, []Message{recalled}, 3)
	require.NoError(t, err)

	require.True(t, summaryOf(t, s, "oc_a").LastDeleted)
}

func TestUpsertMessages_ClearsAChatLeftWithNoMainFlowMessage(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)
	require.NotEmpty(t, summaryOf(t, s, "oc_a").LastMessageID)

	// The chat's only main-flow message turns out to be a thread reply.
	reply := msgAt("om_1", "oc_a", 100, -3, "hi")
	reply.ThreadID = "omt_1"
	_, err = s.UpsertMessages(ctx, []Message{reply}, 2)
	require.NoError(t, err)

	c := summaryOf(t, s, "oc_a")
	require.Empty(t, c.LastMessageID, "the summary is cleared, not left stale")
	require.Zero(t, c.LastMessageMs)
}

func TestListChats_OrdersByTheColdStoredTime(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_quiet", "oc_busy", "oc_empty"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_1", "oc_quiet", 100, 1, "old"),
		msgAt("om_2", "oc_busy", 900, 1, "recent"),
	}, 1)
	require.NoError(t, err)

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Len(t, chats, 3)
	require.Equal(t, "oc_busy", chats[0].ChatID)
	require.Equal(t, "oc_quiet", chats[1].ChatID)
	require.Equal(t, "oc_empty", chats[2].ChatID, "a chat with no messages sorts last")
}

func TestUpdateRendered_CarriesMentionsIntoTheSummary(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)

	mentions := `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`
	require.NoError(t, s.UpdateRendered(ctx, "om_1", "@林岚 hi", mentions, "", 42))
	require.Equal(t, mentions, summaryOf(t, s, "oc_a").LastMentionsJSON)

	_, err = s.UpsertMessages(ctx, []Message{msgAt("om_2", "oc_a", 200, 2, "newer")}, 2)
	require.NoError(t, err)
	require.Empty(t, summaryOf(t, s, "oc_a").LastMentionsJSON, "a newer message brings its own mentions, or none")
}
