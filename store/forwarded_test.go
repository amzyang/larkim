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
