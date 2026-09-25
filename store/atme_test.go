package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// atMeStore is a chat holding one unread message that names the reader,
// followed by n unread messages that do not.
func atMeStore(t *testing.T, mentions string, after int) *Store {
	t.Helper()
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_group", 1))

	msgs := []Message{{MessageID: "om_at", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a",
		ContentRaw: `{"text":"@林岚 看下"}`, MentionsJSON: mentions, CreateMs: 100, UpdateMs: 100}}
	for i := range after {
		msgs = append(msgs, Message{MessageID: "om_after" + string(rune('a'+i)), ChatID: "oc_group",
			MsgType: "text", SenderID: "ou_a", ContentRaw: `{"text":"别的"}`,
			CreateMs: int64(200 + i), UpdateMs: int64(200 + i)})
	}
	_, err := s.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)
	// mentions_json belongs to the rendering pass, not to ingest.
	require.NoError(t, s.UpdateRendered(ctx, "om_at", "@林岚 看下", mentions, "", 150))
	unread := false
	for _, m := range msgs {
		require.NoError(t, s.SetReadStatus(ctx, m.MessageID, &unread, 100, 0))
	}
	return s
}

func listOne(t *testing.T, s *Store, self string) Chat {
	t.Helper()
	chats, err := s.ListChats(context.Background(), ChatQuery{Self: self})
	require.NoError(t, err)
	require.Len(t, chats, 1)
	return chats[0]
}

// The marker belongs to the chat, not to its newest message: five replies
// later somebody is still waiting on the reader.
func TestListChats_AtMeSurvivesLaterMessages(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 5)

	assert.True(t, listOne(t, s, "ou_me").UnreadMention)
}

func TestListChats_AtMeIgnoresMentionsOfOtherPeople(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_a","key":"@_user_1","name":"张三"}]`, 0)

	assert.False(t, listOne(t, s, "ou_me").UnreadMention)
}

// One open id must not answer for another that merely starts with it.
func TestListChats_AtMeDoesNotMatchAnIdPrefix(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_meeting","key":"@_user_1","name":"会议助手"}]`, 0)

	assert.False(t, listOne(t, s, "ou_me").UnreadMention)
}

// Reading the chat is what puts the marker out.
func TestListChats_AtMeGoesOutWhenTheChatIsRead(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 2)
	require.True(t, listOne(t, s, "ou_me").UnreadMention)

	require.NoError(t, s.MarkChatRead(context.Background(), "oc_group", 500))

	assert.False(t, listOne(t, s, "ou_me").UnreadMention)
}

// An empty reader id must leave every chat unmarked: instr with an empty
// needle answers 1 on any string, which would light the whole list.
func TestListChats_NoSelfLeavesEveryChatUnmarked(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 0)

	assert.False(t, listOne(t, s, "").UnreadMention)
}

// The mention flag rides the same aggregate as the count, so a filtered
// listing must still bind its arguments in the right order.
func TestListChats_AtMeSurvivesAFilteredListing(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 0)
	ctx := context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_group", Name: "平台组", ChatMode: "group"}}, 1))

	chats, err := s.ListChats(ctx, ChatQuery{Self: "ou_me", Search: "平台", Mode: "group"})
	require.NoError(t, err)
	require.Len(t, chats, 1)
	assert.True(t, chats[0].UnreadMention)
	assert.EqualValues(t, 1, chats[0].UnreadCount)
}

func TestMentionsOf_ListsWhatNamesTheReaderNewestFirst(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_group", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_old", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a", CreateMs: 100, UpdateMs: 100},
		{MessageID: "om_new", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a", CreateMs: 300, UpdateMs: 300},
		{MessageID: "om_other", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a", CreateMs: 200, UpdateMs: 200},
	}, 1)
	require.NoError(t, err)
	me := `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`
	require.NoError(t, s.UpdateRendered(ctx, "om_old", "@林岚 早", me, "", 1))
	require.NoError(t, s.UpdateRendered(ctx, "om_new", "@林岚 晚", me, "", 1))
	require.NoError(t, s.UpdateRendered(ctx, "om_other", "@张三 你好",
		`[{"id":"ou_a","key":"@_user_1","name":"张三"}]`, "", 1))

	hits, err := s.MentionsOf(ctx, "ou_me", 0)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "om_new", hits[0].MessageID, "newest first")
	assert.Equal(t, "om_old", hits[1].MessageID)
}

// A mention already read is still the thing the reader was asked about, so it
// keeps its place in the list.
func TestMentionsOf_KeepsMentionsTheReaderHasSeen(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 0)
	ctx := context.Background()
	require.NoError(t, s.MarkChatRead(ctx, "oc_group", 500))

	hits, err := s.MentionsOf(ctx, "ou_me", 0)
	require.NoError(t, err)
	assert.Len(t, hits, 1)
}

func TestMentionsOf_WithoutASelfIdAnswersNothing(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 0)

	hits, err := s.MentionsOf(context.Background(), "", 0)
	require.NoError(t, err)
	assert.Empty(t, hits)
}

// A rule that says "do not pull me by this" holds for the list too.
func TestMentionsOf_LeavesOutSilencedMessages(t *testing.T) {
	s := atMeStore(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, 0)
	ctx := context.Background()
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET silenced = 1 WHERE message_id = 'om_at'`)
	require.NoError(t, err)

	hits, err := s.MentionsOf(ctx, "ou_me", 0)
	require.NoError(t, err)
	assert.Empty(t, hits)
}

// prettyMentions is how lark-cli actually prints a mention block: indented,
// with a space after every colon. Every other test here writes the compact
// spelling by hand, which is why the needle could stop matching real rows
// without a test noticing.
const prettyMentions = `[
  {
    "key": "@_user_1",
    "id": "ou_me",
    "name": "林岚"
  }
]`

func TestListChats_AtMeMatchesMentionsAsLarkCliPrintsThem(t *testing.T) {
	s := atMeStore(t, prettyMentions, 0)

	assert.True(t, listOne(t, s, "ou_me").UnreadMention,
		"the stored spelling of a mention is the store's own, not lark-cli's")
}

func TestMentionsOf_FindsMentionsAsLarkCliPrintsThem(t *testing.T) {
	s := atMeStore(t, prettyMentions, 0)

	found, err := s.MentionsOf(t.Context(), "ou_me", 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "om_at", found[0].MessageID)
}

// A body that does not parse is still the only copy of what arrived.
func TestUpdateRendered_KeepsUnparsableJSONAsItCame(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_quiet", 100, 1, "hi")}, 1)
	require.NoError(t, err)

	require.NoError(t, s.UpdateRendered(ctx, "om_a", "hi", "{not json", "", 150))

	m, err := s.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	assert.Equal(t, "{not json", m.MentionsJSON)
}
