package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDraft_MissingChatIsZeroNotError(t *testing.T) {
	s, ctx := openTest(t), t.Context()

	d, err := s.LoadDraft(ctx, "oc_quiet", "")
	require.NoError(t, err)
	assert.Equal(t, Draft{ChatID: "oc_quiet"}, d)
	assert.True(t, d.Empty())
}

func TestSaveDraft_RoundTripsEveryField(t *testing.T) {
	s, ctx := openTest(t), t.Context()

	want := Draft{ChatID: "oc_quiet", Text: "半句话", ReplyTo: "om_elsewhere", InThread: true}
	require.NoError(t, s.SaveDraft(ctx, want, 1700))

	got, err := s.LoadDraft(ctx, "oc_quiet", "")
	require.NoError(t, err)
	want.UpdatedAt = 1700
	assert.Equal(t, want, got)
}

// The composer is one widget shared by every chat, so the store is what keeps
// two chats' drafts apart.
func TestSaveDraft_KeepsChatsApart(t *testing.T) {
	s, ctx := openTest(t), t.Context()

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "群里的半句"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_peer", Text: "单聊的半句"}, 2))

	group, err := s.LoadDraft(ctx, "oc_group", "")
	require.NoError(t, err)
	peer, err := s.LoadDraft(ctx, "oc_peer", "")
	require.NoError(t, err)
	assert.Equal(t, "群里的半句", group.Text)
	assert.Equal(t, "单聊的半句", peer.Text)
}

func TestSaveDraft_LastWriteWins(t *testing.T) {
	s, ctx := openTest(t), t.Context()

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "first"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "second"}, 2))

	got, err := s.LoadDraft(ctx, "oc_quiet", "")
	require.NoError(t, err)
	assert.Equal(t, "second", got.Text)
	assert.EqualValues(t, 2, got.UpdatedAt)
}

// An empty draft leaves no row, so the chat list has nothing to draw a marker
// from once the composer is cleared.
func TestSaveDraft_EmptyTextDropsTheRow(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "半句"}, 1))

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", ReplyTo: "om_elsewhere"}, 2))

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestDeleteDraft_IsWhatASuccessfulSendDoes(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "半句"}, 1))

	require.NoError(t, s.DeleteDraft(ctx, "oc_quiet", ""))

	got, err := s.LoadDraft(ctx, "oc_quiet", "")
	require.NoError(t, err)
	assert.True(t, got.Empty())
	require.NoError(t, s.DeleteDraft(ctx, "oc_quiet", ""), "deleting a missing draft is not an error")
}

func TestDrafts_KeyedByChat(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "a"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_peer", Text: "b"}, 2))

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "a", all["oc_group"].Text)
	assert.Equal(t, "b", all["oc_peer"].Text)
}

// A draft is written by the same process that displays it, so counting it
// would make saving a draft tell that process to reload.
func TestSaveDraft_DoesNotAdvanceDataRev(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	before, err := s.DataRev(ctx)
	require.NoError(t, err)

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "半句"}, 1))
	require.NoError(t, s.DeleteDraft(ctx, "oc_quiet", ""))

	after, err := s.DataRev(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestSaveDraft_BlanksDeleteTheRow(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "半句"}, 1))

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: " \n\t "}, 2))

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

// Each box keeps its own row: the chat's, and one per frame the right column
// stood in. Without the frame in the key a thread's half-written answer and
// the chat's half-written message would overwrite each other.
func TestSaveDraft_KeepsOneRowPerFrame(t *testing.T) {
	s, ctx := openTest(t), t.Context()

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "写给会话的"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", FrameID: "omt_a", Text: "写给话题的"}, 2))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", FrameID: "om_root", Text: "写给回复串的"}, 3))

	chat, err := s.LoadDraft(ctx, "oc_group", "")
	require.NoError(t, err)
	assert.Equal(t, "写给会话的", chat.Text)
	thread, err := s.LoadDraft(ctx, "oc_group", "omt_a")
	require.NoError(t, err)
	assert.Equal(t, "写给话题的", thread.Text)
	tree, err := s.LoadDraft(ctx, "oc_group", "om_root")
	require.NoError(t, err)
	assert.Equal(t, "写给回复串的", tree.Text)
}

func TestLoadDraft_AnswersTheFrameItWasAskedFor(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", FrameID: "omt_a", Text: "半句"}, 1))

	got, err := s.LoadDraft(ctx, "oc_group", "")
	require.NoError(t, err)
	assert.True(t, got.Empty(), "the chat's own box is empty although the thread's is not")

	got, err = s.LoadDraft(ctx, "oc_group", "omt_a")
	require.NoError(t, err)
	assert.Equal(t, Draft{ChatID: "oc_group", FrameID: "omt_a", Text: "半句", UpdatedAt: 1}, got)
}

func TestDeleteDraft_LeavesTheOtherBoxAlone(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "会话"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", FrameID: "omt_a", Text: "话题"}, 2))

	require.NoError(t, s.DeleteDraft(ctx, "oc_group", "omt_a"))

	chat, err := s.LoadDraft(ctx, "oc_group", "")
	require.NoError(t, err)
	assert.Equal(t, "会话", chat.Text)
}

func TestDrafts_ListsOnlyTheChatsOwnComposer(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "会话"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", FrameID: "omt_a", Text: "话题"}, 2))

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "会话", all["oc_group"].Text)
}

func TestFrameDrafts_KeysByFrame(t *testing.T) {
	s, ctx := openTest(t), t.Context()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "会话"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", FrameID: "omt_a", Text: "话题"}, 2))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_peer", FrameID: "om_root", Text: "回复串"}, 3))

	all, err := s.FrameDrafts(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "话题", all["omt_a"].Text)
	assert.Equal(t, "oc_group", all["omt_a"].ChatID)
	assert.Equal(t, "回复串", all["om_root"].Text)
}

// The migration rebuilds the table around a wider key, so the rows it found
// have to come out as the chat's own box rather than as nobody's.
func TestMigration0037_KeepsTheChatDraftsItFound(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	ctx := t.Context()

	for _, stmt := range []string{
		`DROP TABLE drafts`,
		`CREATE TABLE drafts (
			chat_id    TEXT PRIMARY KEY,
			text       TEXT    NOT NULL DEFAULT '',
			reply_to   TEXT    NOT NULL DEFAULT '',
			in_thread  INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO drafts (chat_id, text, reply_to, in_thread, updated_at) VALUES
			('oc_group','群里的半句','om_elsewhere',1,700),
			('oc_peer','单聊的半句','',0,800)`,
		`DELETE FROM schema_migrations WHERE version = 37`,
	} {
		_, err = s.db.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	s.Close()

	s, err = Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	defer s.Close()

	got, err := s.LoadDraft(ctx, "oc_group", "")
	require.NoError(t, err)
	assert.Equal(t, Draft{ChatID: "oc_group", Text: "群里的半句",
		ReplyTo: "om_elsewhere", InThread: true, UpdatedAt: 700}, got)

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 2)
	frames, err := s.FrameDrafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, frames, "nothing was written in a frame before there were frames")
}
