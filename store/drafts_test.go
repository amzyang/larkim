package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDraft_MissingChatIsZeroNotError(t *testing.T) {
	s, ctx := openTest(t), context.Background()

	d, err := s.LoadDraft(ctx, "oc_quiet")
	require.NoError(t, err)
	assert.Equal(t, Draft{ChatID: "oc_quiet"}, d)
	assert.True(t, d.Empty())
}

func TestSaveDraft_RoundTripsEveryField(t *testing.T) {
	s, ctx := openTest(t), context.Background()

	want := Draft{ChatID: "oc_quiet", Text: "半句话", ReplyTo: "om_elsewhere", InThread: true}
	require.NoError(t, s.SaveDraft(ctx, want, 1700))

	got, err := s.LoadDraft(ctx, "oc_quiet")
	require.NoError(t, err)
	want.UpdatedAt = 1700
	assert.Equal(t, want, got)
}

// The composer is one widget shared by every chat, so the store is what keeps
// two chats' drafts apart.
func TestSaveDraft_KeepsChatsApart(t *testing.T) {
	s, ctx := openTest(t), context.Background()

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "群里的半句"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_peer", Text: "单聊的半句"}, 2))

	group, err := s.LoadDraft(ctx, "oc_group")
	require.NoError(t, err)
	peer, err := s.LoadDraft(ctx, "oc_peer")
	require.NoError(t, err)
	assert.Equal(t, "群里的半句", group.Text)
	assert.Equal(t, "单聊的半句", peer.Text)
}

func TestSaveDraft_LastWriteWins(t *testing.T) {
	s, ctx := openTest(t), context.Background()

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "first"}, 1))
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "second"}, 2))

	got, err := s.LoadDraft(ctx, "oc_quiet")
	require.NoError(t, err)
	assert.Equal(t, "second", got.Text)
	assert.EqualValues(t, 2, got.UpdatedAt)
}

// An empty draft leaves no row, so the chat list has nothing to draw a marker
// from once the composer is cleared.
func TestSaveDraft_EmptyTextDropsTheRow(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "半句"}, 1))

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", ReplyTo: "om_elsewhere"}, 2))

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestDeleteDraft_IsWhatASuccessfulSendDoes(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "半句"}, 1))

	require.NoError(t, s.DeleteDraft(ctx, "oc_quiet"))

	got, err := s.LoadDraft(ctx, "oc_quiet")
	require.NoError(t, err)
	assert.True(t, got.Empty())
	require.NoError(t, s.DeleteDraft(ctx, "oc_quiet"), "deleting a missing draft is not an error")
}

func TestDrafts_KeyedByChat(t *testing.T) {
	s, ctx := openTest(t), context.Background()
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
	s, ctx := openTest(t), context.Background()
	before, err := s.DataRev(ctx)
	require.NoError(t, err)

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_quiet", Text: "半句"}, 1))
	require.NoError(t, s.DeleteDraft(ctx, "oc_quiet"))

	after, err := s.DataRev(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestSaveDraft_BlanksDeleteTheRow(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: "半句"}, 1))

	require.NoError(t, s.SaveDraft(ctx, Draft{ChatID: "oc_group", Text: " \n\t "}, 2))

	all, err := s.Drafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}
