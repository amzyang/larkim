package sync

import (
	"context"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTick_RebuildsSilenceWhenTheRulesChange(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_quiet", Name: "Platform", ChatMode: "group"}}
	noise := msg("om_noise", "oc_quiet", now.Add(-30*time.Second), "nightly build #418 passed")
	noise.Sender = larkcli.RawSender{ID: "cli_c", SenderType: "app", SenderName: "Build bot"}
	f.AddMessage(noise)

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	m, err := s.Store.GetMessage(ctx, "om_noise")
	require.NoError(t, err)
	require.False(t, m.Silenced)
	require.Equal(t, m.CreateMs, chatOf(t, s, "oc_quiet").LastUnsilencedMs)

	s.Store.Silence = store.SilenceRules{{Sender: "cli_c"}}
	clk.t = now.Add(10 * time.Second)
	_, err = s.Tick(ctx)
	require.NoError(t, err)

	m, err = s.Store.GetMessage(ctx, "om_noise")
	require.NoError(t, err)
	require.True(t, m.Silenced, "an edited config reaches the messages already stored")
	c := chatOf(t, s, "oc_quiet")
	require.Equal(t, "om_noise", c.LastMessageID, "the chat row still says what arrived")
	require.Zero(t, c.LastUnsilencedMs, "with nothing left unsilenced the chat sinks")
}

func TestTick_LeavesSilenceAloneWhenTheRulesHold(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Store.Silence = store.SilenceRules{{Sender: "cli_c"}}
	f.AddMessage(msg("om_a", "oc_quiet", clk.t.Add(-30*time.Second), "morning"))
	_, err := s.Tick(ctx)
	require.NoError(t, err)

	before, err := s.Store.DataRev(ctx)
	require.NoError(t, err)
	clk.t = clk.t.Add(10 * time.Second)
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	after, err := s.Store.DataRev(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "an unchanged rule set writes nothing")
}

func chatOf(t *testing.T, s *Syncer, chatID string) store.Chat {
	t.Helper()
	c, err := s.Store.GetChat(context.Background(), chatID)
	require.NoError(t, err)
	return c
}
