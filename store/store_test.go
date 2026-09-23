package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrate_IsIdempotentAndSchemaLists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	s.Close()
	s, err = Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	defer s.Close()
	require.Contains(t, Schema(), "CREATE TABLE messages")
}

func TestMigrate_RewindsResourceScanForCardImages(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	require.NoError(t, s.SetState(ctx, "resource_scan_id", "9720"))
	// Pretend the database predates the card-image migration.
	_, err = s.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 8`)
	require.NoError(t, err)
	s.Close()

	s, err = Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	defer s.Close()
	v, ok, err := s.GetState(ctx, "resource_scan_id")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0", v)
}

func TestUpsertMessages_PreservesRenderingAndRecalledContent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	m := Message{MessageID: "om_1", ChatID: "oc_a", MsgType: "text", ContentRaw: `{"text":"hi"}`, CreateMs: 100, UpdateMs: 100, MessagePosition: 3, RawJSON: "{}"}
	n, err := s.UpsertMessages(ctx, []Message{m}, 1000)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, s.UpdateRendered(ctx, "om_1", "hi", "[]", "", 1001))

	// Re-sync unchanged: rendering stays.
	_, err = s.UpsertMessages(ctx, []Message{m}, 2000)
	require.NoError(t, err)
	got, err := s.GetMessage(ctx, "om_1")
	require.NoError(t, err)
	require.Equal(t, "hi", got.Content)
	require.Equal(t, int64(1001), got.RenderedAt)
	require.Equal(t, int64(1000), got.FirstSeenAt)
	require.Equal(t, int64(2000), got.LastSeenAt)

	// Edited: update_ms changes → needs re-render.
	edited := m
	edited.UpdateMs = 150
	edited.Updated = true
	_, err = s.UpsertMessages(ctx, []Message{edited}, 3000)
	require.NoError(t, err)
	got, _ = s.GetMessage(ctx, "om_1")
	require.Zero(t, got.RenderedAt)
	require.True(t, got.Updated)

	// Recalled with empty body: keep last content_raw, stamp deleted_seen_at once.
	recalled := edited
	recalled.Deleted = true
	recalled.ContentRaw = ""
	_, err = s.UpsertMessages(ctx, []Message{recalled}, 4000)
	require.NoError(t, err)
	_, err = s.UpsertMessages(ctx, []Message{recalled}, 5000)
	require.NoError(t, err)
	got, _ = s.GetMessage(ctx, "om_1")
	require.True(t, got.Deleted)
	require.Equal(t, `{"text":"hi"}`, got.ContentRaw)
	require.Equal(t, int64(4000), got.DeletedSeenAt)
}

func TestUnknownMessageIDs_PreservesOrder(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{{MessageID: "om_b", ChatID: "oc", CreateMs: 1, RawJSON: "{}"}}, 1)
	require.NoError(t, err)
	unknown, err := s.UnknownMessageIDs(ctx, []string{"om_a", "om_b", "om_c"})
	require.NoError(t, err)
	require.Equal(t, []string{"om_a", "om_c"}, unknown)
}

func TestListMessages_FiltersAndOrders(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	rows := []Message{
		{MessageID: "om_1", ChatID: "oc_a", SenderID: "ou_x", MsgType: "text", CreateMs: 100, MessagePosition: 1, RawJSON: "{}"},
		{MessageID: "om_2", ChatID: "oc_a", SenderID: "ou_y", MsgType: "image", CreateMs: 200, MessagePosition: 2, RawJSON: "{}"},
		{MessageID: "om_3", ChatID: "oc_b", SenderID: "ou_x", MsgType: "text", CreateMs: 300, MessagePosition: 1, RawJSON: "{}"},
		{MessageID: "om_4", ChatID: "oc_a", SenderID: "ou_x", MsgType: "text", CreateMs: 400, MessagePosition: 3, Deleted: true, RawJSON: "{}"},
	}
	_, err := s.UpsertMessages(ctx, rows, 1)
	require.NoError(t, err)

	ids := func(ms []Message) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.MessageID)
		}
		return out
	}
	got, err := s.ListMessages(ctx, MessageQuery{ChatID: "oc_a"})
	require.NoError(t, err)
	require.Equal(t, []string{"om_1", "om_2"}, ids(got), "deleted hidden by default")

	got, _ = s.ListMessages(ctx, MessageQuery{ChatID: "oc_a", IncludeDeleted: true, Desc: true, Limit: 2})
	require.Equal(t, []string{"om_4", "om_2"}, ids(got))

	got, _ = s.ListMessages(ctx, MessageQuery{SenderID: "ou_x", SinceMs: 150})
	require.Equal(t, []string{"om_3"}, ids(got))

	got, _ = s.ListMessages(ctx, MessageQuery{MsgType: "image"})
	require.Equal(t, []string{"om_2"}, ids(got))

	require.NoError(t, s.MarkConsumed(ctx, []string{"om_1"}, 999))
	got, _ = s.ListMessages(ctx, MessageQuery{ChatID: "oc_a", Unconsumed: true})
	require.Equal(t, []string{"om_2"}, ids(got))
	got, _ = s.ListMessages(ctx, MessageQuery{ChatID: "oc_a"})
	require.Equal(t, int64(999), got[0].ConsumedAt)
	require.Nil(t, got[0].IsReadRemote)
}

func TestChats_UpsertLeaveReviveAndBackfill(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	chats := []Chat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}, {ChatID: "oc_b", Name: "Bob", ChatMode: "p2p", P2PTargetID: "ou_b"}}
	require.NoError(t, s.UpsertChats(ctx, chats, 100))
	require.NoError(t, s.SetChatCursor(ctx, "oc_a", 5000))
	require.NoError(t, s.SetChatCursor(ctx, "oc_a", 4000))

	need, err := s.ChatsNeedingBackfill(ctx, 10)
	require.NoError(t, err)
	require.Len(t, need, 2)
	require.NoError(t, s.SetChatBackfillDone(ctx, "oc_a", 150))
	need, _ = s.ChatsNeedingBackfill(ctx, 10)
	require.Len(t, need, 1)

	// Second full listing without oc_b → left; cursor survives the refresh.
	require.NoError(t, s.UpsertChats(ctx, chats[:1], 200))
	n, err := s.MarkChatsLeft(ctx, 200)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	a, _ := s.GetChat(ctx, "oc_a")
	require.Equal(t, int64(5000), a.CursorMs)
	require.Equal(t, int64(150), a.BackfillDoneAt)

	list, _ := s.ListChats(ctx, ChatQuery{})
	require.Len(t, list, 1)
	list, _ = s.ListChats(ctx, ChatQuery{IncludeLeft: true, Search: "bo"})
	require.Len(t, list, 1)
	require.Equal(t, "oc_b", list[0].ChatID)

	// Revived on next listing.
	require.NoError(t, s.UpsertChats(ctx, chats, 300))
	b, _ := s.GetChat(ctx, "oc_b")
	require.Zero(t, b.LeftAt)

	require.NoError(t, s.EnsureChat(ctx, "oc_new", 400))
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 400))
	a, _ = s.GetChat(ctx, "oc_a")
	require.Equal(t, "Alpha", a.Name)
}

func TestStateAndRuns(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, ok, err := s.GetState(ctx, "watermark_ms")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.SetState(ctx, "watermark_ms", "42"))
	require.NoError(t, s.SetState(ctx, "watermark_ms", "43"))
	v, ok, _ := s.GetState(ctx, "watermark_ms")
	require.True(t, ok)
	require.Equal(t, "43", v)

	require.NoError(t, s.RecordRun(ctx, Run{Kind: "tick", StartedAt: 1, FinishedAt: 2, OK: true, Fetched: 3}))
	runs, err := s.LastRuns(ctx, 5)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, 3, runs[0].Fetched)
	c, err := s.Counts(ctx)
	require.NoError(t, err)
	require.Zero(t, c.Messages)
}

// cursorFixture stores five messages whose canonical order is only decidable
// by (create_ms, message_position, id): three of them share a millisecond.
func cursorFixture(t *testing.T) (*Store, []string) {
	t.Helper()
	s := openTest(t)
	msgs := []Message{
		{MessageID: "om_a", ChatID: "oc_a", CreateMs: 50, MessagePosition: 1, RawJSON: "{}"},
		{MessageID: "om_b", ChatID: "oc_a", CreateMs: 100, MessagePosition: 1, RawJSON: "{}"},
		{MessageID: "om_c", ChatID: "oc_a", CreateMs: 100, MessagePosition: 2, RawJSON: "{}"},
		{MessageID: "om_d", ChatID: "oc_a", CreateMs: 100, MessagePosition: 3, RawJSON: "{}"},
		{MessageID: "om_e", ChatID: "oc_a", CreateMs: 200, MessagePosition: 4, RawJSON: "{}"},
	}
	_, err := s.UpsertMessages(context.Background(), msgs, 1)
	require.NoError(t, err)
	return s, []string{"om_a", "om_b", "om_c", "om_d", "om_e"}
}

func ids(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.MessageID
	}
	return out
}

func TestListMessages_CursorsSplitAtTheAnchorWithinOneMillisecond(t *testing.T) {
	s, all := cursorFixture(t)
	ctx := context.Background()

	before, err := s.ListMessages(ctx, MessageQuery{ChatID: "oc_a", BeforeID: "om_c", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"om_a", "om_b"}, ids(before), "before is exclusive and splits inside the shared millisecond")

	after, err := s.ListMessages(ctx, MessageQuery{ChatID: "oc_a", AfterID: "om_c", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"om_d", "om_e"}, ids(after))

	require.Equal(t, all, append(append(ids(before), "om_c"), ids(after)...), "the two pages plus the anchor cover everything once")
}

func TestListMessages_CursorPagesDoNotRepeat(t *testing.T) {
	s, all := cursorFixture(t)
	ctx := context.Background()
	var walked []string
	cursor := "om_e"
	for range 4 {
		page, err := s.ListMessages(ctx, MessageQuery{ChatID: "oc_a", BeforeID: cursor, Desc: true, Limit: 2})
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		walked = append(walked, ids(page)...)
		cursor = page[len(page)-1].MessageID
	}
	require.Equal(t, []string{"om_d", "om_c", "om_b", "om_a"}, walked, "paging backwards in twos visits every older message once")
	require.Len(t, all, len(walked)+1)
}

func TestListMessages_UnknownCursorIsNotFound(t *testing.T) {
	s, _ := cursorFixture(t)
	_, err := s.ListMessages(context.Background(), MessageQuery{BeforeID: "om_nope"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestThreadReplyCounts_CountsLiveRepliesOnly(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	msgs := []Message{
		{MessageID: "om_root", ChatID: "oc_a", CreateMs: 10, MessagePosition: 1, ThreadID: "omt_1", RawJSON: "{}"},
		{MessageID: "om_r1", ChatID: "oc_a", CreateMs: 20, MessagePosition: -3, ThreadID: "omt_1", RawJSON: "{}"},
		{MessageID: "om_r2", ChatID: "oc_a", CreateMs: 30, MessagePosition: -1, ThreadID: "omt_1", RawJSON: "{}"},
		{MessageID: "om_r3", ChatID: "oc_a", CreateMs: 40, MessagePosition: -3, ThreadID: "omt_1", Deleted: true, RawJSON: "{}"},
		{MessageID: "om_other", ChatID: "oc_b", CreateMs: 50, MessagePosition: -3, ThreadID: "omt_1", RawJSON: "{}"},
	}
	_, err := s.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)

	got, err := s.ThreadReplyCounts(ctx, "oc_a", []string{"omt_1", "omt_absent"})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"omt_1": 2}, got, "every negative position counts; recalled replies, the root and other chats are left out")
}

func TestListMessages_ExcludeThreadRepliesAppliesBeforeTheLimit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var msgs []Message
	for i := range 20 {
		pos := int64(-3) // a thread reply, whatever sentinel the API chose
		if i%5 == 0 {
			pos = int64(i + 1)
		}
		msgs = append(msgs, Message{MessageID: fmt.Sprintf("om_%02d", i), ChatID: "oc_a",
			CreateMs: int64(i) * 100, MessagePosition: pos, ThreadID: "omt_1", RawJSON: "{}"})
	}
	_, err := s.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)

	got, err := s.ListMessages(ctx, MessageQuery{ChatID: "oc_a", ExcludeThreadReplies: true, Desc: true, Limit: 3})
	require.NoError(t, err)
	require.Equal(t, []string{"om_15", "om_10", "om_05"}, ids(got), "the limit counts the messages that survive the filter")
}

func TestUpsertMessages_EditedAtOnlyTracksObservedContentChange(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	m := Message{MessageID: "om_1", ChatID: "oc_a", MsgType: "text", ContentRaw: `{"text":"3"}`, CreateMs: 100, UpdateMs: 100, RawJSON: "{}"}
	_, err := s.UpsertMessages(ctx, []Message{m}, 1000)
	require.NoError(t, err)
	got, err := s.GetMessage(ctx, "om_1")
	require.NoError(t, err)
	require.Zero(t, got.EditedAt, "a message we have seen only once was never observed changing")

	// Feishu's own post-send patch (@ resolution, link and time-phrase
	// enrichment) bumps updated/update_ms but leaves the body alone.
	patched := m
	patched.UpdateMs = 2089
	patched.Updated = true
	_, err = s.UpsertMessages(ctx, []Message{patched}, 2000)
	require.NoError(t, err)
	got, _ = s.GetMessage(ctx, "om_1")
	require.Zero(t, got.EditedAt, "a patch that does not touch the body is not an edit")

	// The sender rewrites the message in the client.
	edited := patched
	edited.ContentRaw = `{"text":"<p>4</p>"}`
	edited.UpdateMs = 40000
	_, err = s.UpsertMessages(ctx, []Message{edited}, 3000)
	require.NoError(t, err)
	got, _ = s.GetMessage(ctx, "om_1")
	require.Equal(t, int64(3000), got.EditedAt)

	// A recall arrives with an empty body and must not read as another edit.
	recalled := edited
	recalled.Deleted = true
	recalled.ContentRaw = ""
	_, err = s.UpsertMessages(ctx, []Message{recalled}, 4000)
	require.NoError(t, err)
	got, _ = s.GetMessage(ctx, "om_1")
	require.Equal(t, int64(3000), got.EditedAt)
}

func TestUpsertMessages_EditedAtIgnoresTypesFeishuCannotEdit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	card := Message{MessageID: "om_c", ChatID: "oc_a", MsgType: "interactive", ContentRaw: `{"elements":["v1"]}`, CreateMs: 100, UpdateMs: 100, RawJSON: "{}"}
	_, err := s.UpsertMessages(ctx, []Message{card}, 1000)
	require.NoError(t, err)

	// A bot redrawing its card is not the sender editing a message.
	card.ContentRaw = `{"elements":["v2"]}`
	card.UpdateMs = 500000
	card.Updated = true
	_, err = s.UpsertMessages(ctx, []Message{card}, 2000)
	require.NoError(t, err)
	got, err := s.GetMessage(ctx, "om_c")
	require.NoError(t, err)
	require.Zero(t, got.EditedAt)
}
