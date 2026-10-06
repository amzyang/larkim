package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestUpsertRaw_RefreshesMuteOnFreshMessage(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.Muted = map[string]bool{"oc_a": true}
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))

	_, fresh, err := s.upsertRaw(ctx, []larkcli.RawMessage{msg("om_new", "oc_a", now, "hi")}, now)
	require.NoError(t, err)
	require.Equal(t, 1, fresh)
	require.Equal(t, 1, callsTo(f, "mute-status"))

	chat, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.True(t, chat.Muted)
	require.Equal(t, now.UnixMilli(), chat.MuteCheckedAt)
}

func TestUpsertRaw_SkipsMuteWhenOverlapOnly(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.Muted = map[string]bool{"oc_a": true}
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	m := msg("om_new", "oc_a", now, "hi")

	_, _, err := s.upsertRaw(ctx, []larkcli.RawMessage{m}, now)
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "mute-status"))

	f.Calls = nil
	_, fresh, err := s.upsertRaw(ctx, []larkcli.RawMessage{m}, now)
	require.NoError(t, err)
	require.Zero(t, fresh)
	require.Zero(t, callsTo(f, "mute-status"))
}

func TestMuteOnMessageArrival_WhenBadgeRisesWithoutFreshMessage(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.Muted = map[string]bool{"oc_a": true}
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", now.UnixMilli()))
	row := ToRow(msg("om_old", "oc_a", now.Add(-time.Hour), "old"))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{row}, now.UnixMilli())
	require.NoError(t, err)

	unread := false
	require.NoError(t, s.Store.SetReadStatus(ctx, "om_old", &unread, now.UnixMilli(), 0))

	s.muteOnMessageArrival(ctx, nil, nil, map[string]struct{}{"oc_a": {}},
		map[string]int64{"oc_a": 0}, now)
	require.Equal(t, 1, callsTo(f, "mute-status"))
	chat, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.True(t, chat.Muted)
}
