package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const dayMs = 24 * 60 * 60 * 1000

func TestChatsNeedingMute_TakesTheLongestUnansweredAndSkipsQuietChats(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	now := int64(100 * dayMs)
	for _, id := range []string{"oc_asked", "oc_never", "oc_quiet"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_1", "oc_asked", now-dayMs, 1, "a"),
		msgAt("om_2", "oc_never", now-2*dayMs, 1, "b"),
		msgAt("om_3", "oc_quiet", now-60*dayMs, 1, "c"),
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.SetMuteStatus(ctx, map[string]bool{"oc_asked": true}, nil, now-dayMs))

	chats, err := s.ChatsNeedingMute(ctx, now-30*dayMs, now, 10)
	require.NoError(t, err)
	ids := make([]string, 0, len(chats))
	for _, c := range chats {
		ids = append(ids, c.ChatID)
	}
	require.Equal(t, []string{"oc_never", "oc_asked"}, ids,
		"never asked comes first, and a chat with nothing recent is not asked about at all")
}

func TestChatsNeedingMute_PrioritisesNeverCheckedChatsWithABadge(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	now := int64(100 * dayMs)
	for _, id := range []string{"oc_unread", "oc_loud"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_old", "oc_unread", now-2*dayMs, 1, "waiting"),
		msgAt("om_new", "oc_loud", now-dayMs, 1, "read already"),
	}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_old")
	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_new", &read, now, 0))

	chats, err := s.ChatsNeedingMute(ctx, now-30*dayMs, now, 10)
	require.NoError(t, err)
	require.Len(t, chats, 2)
	require.Equal(t, "oc_unread", chats[0].ChatID,
		"never checked with a badge beats a newer chat that has nothing waiting")
}

func TestSetMuteStatus_StampsTheChatsItCouldNotAnswerFor(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	require.NoError(t, s.EnsureChat(ctx, "oc_a", 1))
	require.NoError(t, s.EnsureChat(ctx, "oc_x", 1))

	require.NoError(t, s.SetMuteStatus(ctx, map[string]bool{"oc_a": true}, nil, 10))
	require.NoError(t, s.SetMuteStatus(ctx, nil, []string{"oc_a", "oc_x"}, 20))

	a, err := s.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.True(t, a.Muted, "an unanswered chat keeps what was last known")
	require.Equal(t, int64(20), a.MuteCheckedAt, "but is stamped, so it does not hold the rotation in place")

	x, err := s.GetChat(ctx, "oc_x")
	require.NoError(t, err)
	require.False(t, x.Muted)
	require.Equal(t, int64(20), x.MuteCheckedAt)
}
