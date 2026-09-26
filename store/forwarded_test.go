package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// bundle stores one merge_forward message so a queue row can point at it.
func bundle(t *testing.T, s *Store, id, chatID string, createMs int64) {
	t.Helper()
	_, err := s.UpsertMessages(t.Context(), []Message{
		{MessageID: id, ChatID: chatID, MsgType: "merge_forward", CreateMs: createMs,
			ContentRaw: `{"text":"Merged and Forwarded Message"}`, RawJSON: "{}"},
	}, createMs)
	require.NoError(t, err)
	require.NoError(t, s.AddForwardRoots(t.Context(), []string{id}))
}

func TestSaveForwarded_KeepsAChildOutOfTheMessagesTable(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	// The child is a real message of its own chat, already synced with its
	// own position. The bundle references it; it does not copy it.
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_orig", ChatID: "oc_src", MsgType: "text", CreateMs: 100,
			MessagePosition: 10, ContentRaw: `{"text":"预算定了"}`, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	bundle(t, s, "om_fwd", "oc_a", 200)

	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", []Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_orig", ChatID: "oc_src", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", CreateMs: 100, ContentRaw: `{"text":"预算定了"}`},
	}, 300))

	orig, err := s.GetMessage(ctx, "om_orig")
	require.NoError(t, err)
	require.EqualValues(t, 10, orig.MessagePosition,
		"a child written into messages would take the bundle's context and lose the message its own chat holds")
	require.Equal(t, "oc_src", orig.ChatID)

	n, err := scanOne[int64](s.db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE chat_id = 'oc_src'`))
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "expanding a bundle must not conjure rows into a chat larkim never synced")
}

func TestSaveForwarded_TheSameMessageAtTwoDepthsKeepsBothRows(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	// Somebody forwarded one message on its own and a stretch of history
	// containing it, then merged both into this bundle.
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", []Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_orig", ChatID: "oc_src", MsgType: "text", CreateMs: 100},
		{UpperMessageID: "om_fwd", MessageID: "om_inner", ChatID: "oc_a", MsgType: "merge_forward", CreateMs: 110},
		{UpperMessageID: "om_inner", MessageID: "om_orig", ChatID: "oc_src", MsgType: "text", CreateMs: 100},
	}, 300))

	top, err := s.ForwardChildren(ctx, "om_fwd", "om_fwd")
	require.NoError(t, err)
	require.Len(t, top, 2)
	inner, err := s.ForwardChildren(ctx, "om_fwd", "om_inner")
	require.NoError(t, err)
	require.Len(t, inner, 1, "keyed without the parent, the nested copy would replace the top-level one")
	require.Equal(t, "om_orig", inner[0].MessageID)
}

func TestSaveForwarded_SettlesTheQueueRowOnTheTopLevelCount(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", []Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_a", ChatID: "oc_src", MsgType: "text", CreateMs: 100},
		{UpperMessageID: "om_fwd", MessageID: "om_inner", ChatID: "oc_a", MsgType: "merge_forward", CreateMs: 110},
		{UpperMessageID: "om_inner", MessageID: "om_b", ChatID: "oc_src", MsgType: "text", CreateMs: 90},
		{UpperMessageID: "om_inner", MessageID: "om_c", ChatID: "oc_src", MsgType: "text", CreateMs: 95},
	}, 300))

	root, err := s.GetForwardRoot(ctx, "om_fwd")
	require.NoError(t, err)
	require.Equal(t, 2, root.ChildCount, "the count says what one frame lists, not what the whole tree holds")
	require.EqualValues(t, 300, root.FetchedAt)

	due, err := s.ForwardRootsDue(ctx, 1_000, 10)
	require.NoError(t, err)
	require.Empty(t, due, "an expanded bundle is off the queue")
}

func TestSaveForwarded_ReplacesWhatAnEarlierAnswerLeft(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	kids := []Forwarded{{UpperMessageID: "om_fwd", MessageID: "om_a", ChatID: "oc_src", MsgType: "text", CreateMs: 100}}
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", kids, 300))
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", kids, 400))

	top, err := s.ForwardChildren(ctx, "om_fwd", "om_fwd")
	require.NoError(t, err)
	require.Len(t, top, 1, "a bundle is frozen, so a second answer is the same answer written once")
}

func TestForwardRootsDue_TakesTheNewestBundlesFirst(t *testing.T) {
	s := openTest(t)
	bundle(t, s, "om_old", "oc_a", 100)
	bundle(t, s, "om_new", "oc_a", 300)
	bundle(t, s, "om_mid", "oc_a", 200)

	due, err := s.ForwardRootsDue(t.Context(), 1_000, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"om_new", "om_mid"}, []string{due[0].RootMessageID, due[1].RootMessageID},
		"the newest forwards are the ones a reader is about to open")
}

func TestForwardRootsDue_HoldsBackABundleWaitingOnItsBackoff(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.MarkForwardFailed(ctx, "om_fwd", "timeout", 5_000))

	due, err := s.ForwardRootsDue(ctx, 4_999, 10)
	require.NoError(t, err)
	require.Empty(t, due)

	due, err = s.ForwardRootsDue(ctx, 5_000, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, 1, due[0].Attempts)
}

func TestForwardRootsDue_DropsARefusedBundleForGood(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.MarkForwardRefused(ctx, "om_fwd", "230002 permission denied", 300))

	// next_attempt_at is back to zero on a refused row, so fetched_at is what
	// has to keep it out; reading the clock alone would ask again at once.
	due, err := s.ForwardRootsDue(ctx, 1_000_000, 10)
	require.NoError(t, err)
	require.Empty(t, due)

	root, err := s.GetForwardRoot(ctx, "om_fwd")
	require.NoError(t, err)
	require.Equal(t, "230002 permission denied", root.LastError, "the summary line reads this to say it cannot be opened")
}

func TestForwardRootsDue_LeavesARecalledBundleAlone(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_fwd", ChatID: "oc_a", MsgType: "merge_forward", CreateMs: 200, Deleted: true, RawJSON: "{}"},
	}, 400)
	require.NoError(t, err)

	due, err := s.ForwardRootsDue(ctx, 1_000, 10)
	require.NoError(t, err)
	require.Empty(t, due)
}

func TestAddForwardRoots_LeavesAnAlreadySettledBundleAlone(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", []Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_a", ChatID: "oc_src", MsgType: "text", CreateMs: 100},
	}, 300))

	// Every listing re-reads its overlap, so the same id arrives again.
	require.NoError(t, s.AddForwardRoots(ctx, []string{"om_fwd"}))

	root, err := s.GetForwardRoot(ctx, "om_fwd")
	require.NoError(t, err)
	require.EqualValues(t, 300, root.FetchedAt, "re-queuing a bundle would expand it again on every tick")
	require.Equal(t, 1, root.ChildCount)
}

func TestMigrate_SeedsEveryStoredBundleIntoTheQueue(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	// Messages stored before the table existed carry no queue row, so the
	// migration seeds them; here the rows are written and the seeding SQL is
	// replayed against them.
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_fwd", ChatID: "oc_a", MsgType: "merge_forward", CreateMs: 100, RawJSON: "{}"},
		{MessageID: "om_gone", ChatID: "oc_a", MsgType: "merge_forward", CreateMs: 110, Deleted: true, RawJSON: "{}"},
		{MessageID: "om_text", ChatID: "oc_a", MsgType: "text", CreateMs: 120, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO forwarded_roots (root_message_id)
 SELECT message_id FROM messages WHERE msg_type = 'merge_forward' AND deleted = 0`)
	require.NoError(t, err)

	due, err := s.ForwardRootsDue(ctx, 1_000, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "om_fwd", due[0].RootMessageID)
}

// chats10 is ten children of one chat, so a preview has more to choose from
// than it shows.
func chats10(chatID string) []Forwarded {
	out := make([]Forwarded, 0, 10)
	for i := range 10 {
		out = append(out, Forwarded{UpperMessageID: "om_fwd", MessageID: "om_c" + string(rune('a'+i)),
			ChatID: chatID, MsgType: "text", Seq: i, SenderID: "ou_a", SenderName: "张三",
			CreateMs: int64(100 + i), ContentRaw: `{"text":"第` + string(rune('0'+i)) + `句"}`})
	}
	return out
}

func TestForwardGists_PreviewsTheFirstChildrenInOrder(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", chats10("oc_src"), 300))

	gists, err := s.ForwardGists(ctx, []string{"om_fwd"})
	require.NoError(t, err)
	g := gists["om_fwd"]

	require.Equal(t, 10, g.ChildCount, "the count is the frame's, not the card's")
	require.Len(t, g.Preview, ForwardPreview)
	require.Equal(t, `{"text":"第0句"}`, g.Preview[0].ContentRaw, "a forward opens with what it was forwarded for")
	require.Equal(t, `{"text":"第3句"}`, g.Preview[3].ContentRaw)
	require.Equal(t, "张三", g.Preview[0].SenderName)
}

func TestForwardGists_NamesTheChatTheBundleCameFrom(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	require.NoError(t, s.UpsertChats(ctx, []Chat{
		{ChatID: "oc_src", Name: "平台组", ChatMode: "group"},
	}, 100))
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", chats10("oc_src"), 300))

	gists, err := s.ForwardGists(ctx, []string{"om_fwd"})
	require.NoError(t, err)
	g := gists["om_fwd"]

	require.Equal(t, 1, g.Sources)
	require.Equal(t, "oc_src", g.SourceChatID)
	require.Equal(t, "group", g.SourceChatMode)
	require.Equal(t, "平台组", g.SourceChatName)
}

func TestForwardGists_LeavesAMixedBundleUnnamed(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	kids := chats10("oc_src")
	kids[4].ChatID = "oc_other"
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", kids, 300))

	gists, err := s.ForwardGists(ctx, []string{"om_fwd"})
	require.NoError(t, err)

	require.Equal(t, 2, gists["om_fwd"].Sources)
	require.Empty(t, gists["om_fwd"].SourceChatID, "two conversations have no one name between them")
}

func TestForwardGists_LeavesAChatLarkimNeverSyncedUnnamed(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", chats10("oc_elsewhere"), 300))

	gists, err := s.ForwardGists(ctx, []string{"om_fwd"})
	require.NoError(t, err)
	g := gists["om_fwd"]

	require.Equal(t, 1, g.Sources)
	require.Equal(t, "oc_elsewhere", g.SourceChatID)
	require.Empty(t, g.SourceChatMode, "nothing here or at Feishu can say what sort of chat that was")
}

func TestForwardLevels_PreviewsAndNamesANestedBundle(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	require.NoError(t, s.UpsertChats(ctx, []Chat{
		{ChatID: "oc_dm", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a"},
	}, 100))
	bundle(t, s, "om_fwd", "oc_a", 200)
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", []Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_inner", ChatID: "oc_src",
			MsgType: "merge_forward", Seq: 0, CreateMs: 100},
		{UpperMessageID: "om_inner", MessageID: "om_x", ChatID: "oc_dm", MsgType: "text", Seq: 0,
			SenderName: "张三", CreateMs: 90, ContentRaw: `{"text":"预算定了"}`},
		{UpperMessageID: "om_inner", MessageID: "om_y", ChatID: "oc_dm", MsgType: "text", Seq: 1,
			SenderName: "林岚", CreateMs: 91, ContentRaw: `{"text":"收到"}`},
	}, 300))

	levels, err := s.ForwardLevels(ctx, "om_fwd", []string{"om_inner"})
	require.NoError(t, err)
	g := levels["om_inner"]

	require.True(t, g.Expanded, "its rows are here, so it is expanded by construction")
	require.Equal(t, 2, g.ChildCount)
	require.Len(t, g.Preview, 2)
	require.Equal(t, "张三", g.Preview[0].SenderName)
	require.Equal(t, "p2p", g.SourceChatMode, "a nested card is named after its own children's chat")
	require.Equal(t, "ou_a", g.SourcePeerID)
}

func TestSaveForwarded_StoresAChildsReactionsMinimised(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	bundle(t, s, "om_fwd", "oc_a", 200)

	// lark-cli prints its JSON indented. The @-me marker and the :mentions
	// panel match these columns by text, so the indentation cannot survive.
	require.NoError(t, s.SaveForwarded(ctx, "om_fwd", []Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_a", ChatID: "oc_src", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", CreateMs: 100, ContentRaw: `{"text":"预算定了"}`,
			MentionsJSON:  "[\n  {\n    \"key\": \"@_user_1\",\n    \"id\": \"ou_me\"\n  }\n]",
			ReactionsJSON: "{\n  \"counts\": [\n    {\n      \"reaction_type\": \"THUMBSUP\"\n    }\n  ]\n}"},
	}, 300))

	kids, err := s.ForwardChildren(ctx, "om_fwd", "om_fwd")
	require.NoError(t, err)
	require.Len(t, kids, 1)
	require.Equal(t, `{"counts":[{"reaction_type":"THUMBSUP"}]}`, kids[0].ReactionsJSON)
	require.Equal(t, `[{"key":"@_user_1","id":"ou_me"}]`, kids[0].MentionsJSON)
}
