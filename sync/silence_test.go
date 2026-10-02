package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTick_RebuildsSilenceWhenTheRulesChange(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
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

	s.Store.SetSilence(store.SilenceRules{{Sender: "cli_c"}})
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
	ctx := t.Context()
	s.Store.SetSilence(store.SilenceRules{{Sender: "cli_c"}})
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
	c, err := s.Store.GetChat(t.Context(), chatID)
	require.NoError(t, err)
	return c
}

// settleRecorder stands in for the markread lever, remembering the watermarks
// it was asked to settle.
type settleRecorder struct {
	calls []store.ChatUnread
	err   error
}

func (r *settleRecorder) Clear(_ context.Context, chat store.ChatUnread) error {
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, chat)
	return nil
}

func TestTick_SettlesSilencedUnreadOnTheServer(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Store.SetSilence(store.SilenceRules{{Sender: "cli_c"}})
	rec := &settleRecorder{}
	s.SetSettleSilenced(rec.Clear)
	require.NoError(t, func() error { _, err := s.EnsureIdentity(ctx); return err }())
	f.Chats = []larkcli.RawChat{{ChatID: "oc_quiet", Name: "Platform", ChatMode: "group"}}
	at := clk.t.Add(-30 * time.Second)
	noise := msg("om_a", "oc_quiet", at, "nightly build #418")
	noise.MessagePosition = 1
	noise.Sender = larkcli.RawSender{ID: "cli_c", SenderType: "app", SenderName: "Build bot"}
	human := msg("om_human", "oc_quiet", at.Add(time.Second), "did anyone look at it")
	human.MessagePosition = 2
	human.Sender = larkcli.RawSender{ID: "ou_b", SenderType: "user", SenderName: "Bob"}
	more := msg("om_c", "oc_quiet", at.Add(2*time.Second), "nightly build #419")
	more.MessagePosition = 3
	more.Sender = noise.Sender
	f.AddMessage(noise)
	f.AddMessage(human)
	f.AddMessage(more)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Settled)
	require.Equal(t, []store.ChatUnread{{ChatID: "oc_quiet", Position: 1}}, rec.calls,
		"the watermark stops below the unsilenced message, which keeps its dot")

	// The settle moves the feed count alone — Feishu's per-message read state
	// never flips — so the sweeps' later touches re-queue the chat. The next
	// tick must see the floor and settle nothing.
	clk.t = clk.t.Add(10 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Settled)
	require.Len(t, rec.calls, 1, "a covered message never settles twice")
	pending, err := s.Store.PendingSilenceSettle(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, pending, "the no-action row is dropped without a write")
}

func TestTick_KeepsAQueuedChatWhenTheSettleFails(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Store.SetSilence(store.SilenceRules{{Sender: "cli_c"}})
	rec := &settleRecorder{err: errors.New("gateway refused")}
	s.SetSettleSilenced(rec.Clear)
	_, err0 := s.EnsureIdentity(ctx)
	require.NoError(t, err0)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_quiet", Name: "Platform", ChatMode: "group"}}
	noise := msg("om_noise", "oc_quiet", clk.t.Add(-30*time.Second), "nightly build #418 passed")
	noise.MessagePosition = 1
	noise.Sender = larkcli.RawSender{ID: "cli_c", SenderType: "app", SenderName: "Build bot"}
	f.AddMessage(noise)

	_, err := s.Tick(ctx)
	require.NoError(t, err, "a refused settle costs its chat, not the tick")
	pending, err := s.Store.PendingSilenceSettle(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the row waits for the next tick")
}

func TestTick_SkipsTheSilenceSettleWhenNoLever(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Store.SetSilence(store.SilenceRules{{Sender: "cli_c"}})
	_, err0 := s.EnsureIdentity(ctx)
	require.NoError(t, err0)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_quiet", Name: "Platform", ChatMode: "group"}}
	noise := msg("om_noise", "oc_quiet", clk.t.Add(-30*time.Second), "nightly build #418 passed")
	noise.MessagePosition = 1
	noise.Sender = larkcli.RawSender{ID: "cli_c", SenderType: "app", SenderName: "Build bot"}
	f.AddMessage(noise)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Settled)
	pending, err := s.Store.PendingSilenceSettle(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "with no lever the queue only fills")
}

func TestTick_StopsSettlingWhenTheLeverIsTakenAwayMidRun(t *testing.T) {
	// silence_sync turned off under a running sweep: the queue goes back to
	// only filling, without the loop being restarted.
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Store.SetSilence(store.SilenceRules{{Sender: "cli_c"}})
	rec := &settleRecorder{}
	s.SetSettleSilenced(rec.Clear)
	_, err0 := s.EnsureIdentity(ctx)
	require.NoError(t, err0)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_quiet", Name: "Platform", ChatMode: "group"}}
	noise := msg("om_noise", "oc_quiet", clk.t.Add(-30*time.Second), "nightly build #418 passed")
	noise.MessagePosition = 1
	noise.Sender = larkcli.RawSender{ID: "cli_c", SenderType: "app", SenderName: "Build bot"}
	f.AddMessage(noise)
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Settled)

	s.SetSettleSilenced(nil)
	clk.t = clk.t.Add(10 * time.Second)
	later := msg("om_later", "oc_quiet", clk.t.Add(-time.Second), "nightly build #419 passed")
	later.MessagePosition = 2
	later.Sender = noise.Sender
	f.AddMessage(later)

	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Settled)
	require.Len(t, rec.calls, 1, "the lever taken away is not asked again")
	pending, err := s.Store.PendingSilenceSettle(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "and the queue goes back to only filling")
}
