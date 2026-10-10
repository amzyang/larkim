package sync

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// discovering is a store the probe can pull from: every chat backfilled, and
// the ordering f.Chats has on record, so the next cycle compares against it.
func discovering(t *testing.T, s *Syncer, f *larkcli.Fake, now time.Time) {
	t.Helper()
	ids := make([]string, len(f.Chats))
	for i, c := range f.Chats {
		ids[i] = c.ChatID
	}
	backfilled(t, s, now, ids...)
	require.NoError(t, s.setActiveOrder(t.Context(), ids))
}

func TestDiscover_AnUnmovedOrderingWritesNothing(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}, {ChatID: "oc_b", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	// The same ordering spelled differently: rewriting it would respell it.
	require.NoError(t, s.Store.SetState(ctx, KeyActiveOrder, `[ "oc_a", "oc_b" ]`))
	nudges := 0
	s.OnChange = func() { nudges++ }

	moved, probed, err := s.discover(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, 2, moved, "the head is listed whether or not it moved")
	require.Zero(t, probed)
	order, _, err := s.Store.GetState(ctx, KeyActiveOrder)
	require.NoError(t, err)
	require.Equal(t, `[ "oc_a", "oc_b" ]`, order, "a cycle that saw nothing move writes nothing")
	require.Zero(t, nudges, "and wakes nobody")
	require.Empty(t, s.wake)
}

func TestDiscover_ShowsAChatsMessageWithoutWaitingForASlowerChat(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_slow", ChatMode: "group"}, {ChatID: "oc_quick", ChatMode: "p2p"}}
	discovering(t, s, f, clk.t)
	f.AddMessage(msg("om_quick", "oc_quick", clk.t.Add(-time.Second), "on its way"))
	release := make(chan struct{})
	f.Enter = func(call string) {
		if call == "list:chat:oc_slow" {
			<-release
		}
	}
	shown := make(chan store.Message, 1)
	s.OnChange = func() {
		if m, err := s.Store.GetMessage(ctx, "om_quick"); err == nil {
			select {
			case shown <- m:
			default:
			}
		}
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := s.discover(ctx, clk.t)
		done <- err
	}()
	select {
	case m := <-shown:
		require.Equal(t, "on its way", m.Content, "the body is shown rendered, not by its type")
	case <-time.After(5 * time.Second):
		t.Fatal("the quick chat's message waited for the slow chat's listing")
	}
	close(release)
	require.NoError(t, <-done)
}

func TestDiscover_AsksForARenderingAtOnceAndWakesTheSweep(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	// A msg_type larkim has no renderer for, which is what still reaches
	// lark-cli.
	card := msg("om_card", "oc_a", clk.t.Add(-time.Second), "")
	card.MsgType, card.Body.Content = "brand_new", `{}`
	f.AddMessage(card)
	f.Rendered = map[string]larkcli.RenderedMessage{"om_card": {MessageID: "om_card", Content: "Deploy finished"}}

	_, probed, err := s.discover(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, 1, probed)
	require.Contains(t, f.Calls, "render", "a type larkim cannot render itself is asked for at once")
	m, err := s.Store.GetMessage(ctx, "om_card")
	require.NoError(t, err)
	require.Equal(t, "Deploy finished", m.Content)
	require.Len(t, s.wake, 1, "the sweep's pause ends so the follow-ups come now")
}

func TestDiscoveryPause_RestsOnlyWhileNobodyIsLooking(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	s.Opt().PollInterval = 2 * time.Second
	s.SetAttended(true)
	require.Equal(t, attendedDiscoveryPause, s.discoveryPause(false, nil, 0), "a reader is waiting, but the lane is not filled")
	require.Equal(t, 2*time.Second, s.discoveryPause(true, nil, 0), "without a login there is nothing to call with")

	s.SetAttended(false)
	require.Equal(t, 2*time.Second, s.discoveryPause(false, nil, 0))

	s.SetAttended(true)
	err := &larkcli.Error{ExitCode: 1, Subtype: "rate_limit", RetryAfter: 45 * time.Second}
	require.Equal(t, s.delayFor(err, 3), s.discoveryPause(false, err, 3), "a failure backs off however closely it is watched")
}

func TestSetAttended_ComingBackEndsThePause(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	s.SetAttended(true)
	<-s.attend

	s.SetAttended(false)
	require.Empty(t, s.attend, "going away is picked up at the next pause")
	s.SetAttended(true)
	require.Len(t, s.attend, 1)
	<-s.attend
	s.SetAttended(true)
	require.Empty(t, s.attend, "only a return is news")
}

func TestRun_DiscoveryLandsAMessageWhileTheSweepRests(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().PollInterval = time.Hour
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	s.SetAttended(true)

	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		require.ErrorIs(t, <-done, context.Canceled)
	})

	f.AddMessage(msg("om_late", "oc_a", clk.t.Add(-time.Second), "sent after the sweep"))
	require.Eventually(t, func() bool {
		_, err := s.Store.GetMessage(ctx, "om_late")
		return err == nil
	}, 5*time.Second, 5*time.Millisecond, "discovery waited out the sweep's hour")
	require.Eventually(t, func() bool {
		runs, err := s.Store.LastRuns(ctx, 10)
		return err == nil && len(runs) >= 2
	}, 5*time.Second, 5*time.Millisecond, "the find ended the sweep's pause")
}

func TestRun_APanicInTheSweepLeavesRunWhileDiscoveryIsStillGoing(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	s.SetAttended(true)
	// Discovery never searches, so only the sweep panics.
	f.Enter = func(call string) {
		if call == "search" {
			panic("sweep broke")
		}
	}

	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_ = s.Run(t.Context())
	}()
	select {
	case p := <-panicked:
		require.Equal(t, "sweep broke", p, "the panic reaches Run's caller, who re-panics it")
	case <-time.After(5 * time.Second):
		t.Fatal("Run waited on a discovery loop nothing told to stop")
	}
}

func TestRunDiscovery_MakesNoCallWhileLoggedOut(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	require.NoError(t, s.Store.SetState(t.Context(), KeyStatus, StatusNeedsLogin))
	s.Opt().PollInterval = 5 * time.Millisecond
	s.SetAttended(true)
	var calls atomic.Int64
	f.Enter = func(string) { calls.Add(1) }

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	s.runDiscovery(ctx)
	require.Zero(t, calls.Load(), "the sweep is what asks whether a login is back")
}

func TestScoutOnce_ASlowChatHoldsUpNobodyElse(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_slow", ChatMode: "p2p"}, {ChatID: "oc_quick", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	release := make(chan struct{})
	var slowCalls atomic.Int64
	f.Enter = func(call string) {
		if call == "list:chat:oc_slow" {
			slowCalls.Add(1)
			<-release
		}
	}
	sc := newScout(s.now, s.delayFor)
	t.Cleanup(func() {
		close(release)
		sc.wg.Wait()
	})

	_, err := s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err)
	f.AddMessage(msg("om_quick", "oc_quick", clk.t.Add(-time.Second), "while the slow chat is still listing"))
	require.Eventually(t, func() bool {
		if _, err := s.scoutOnce(ctx, sc, clk.t); err != nil {
			return false
		}
		_, err := s.Store.GetMessage(ctx, "om_quick")
		return err == nil
	}, 5*time.Second, 5*time.Millisecond, "the quick chat waited for the slow chat's listing")
	require.EqualValues(t, 1, slowCalls.Load(), "a chat being listed is not listed again beside itself")
}

func TestScoutOnce_AFailedListingBacksOffAndIsOwed(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a"}, {ChatID: "oc_b"}, {ChatID: "oc_c"}, {ChatID: "oc_d"}, {ChatID: "oc_e"}}
	discovering(t, s, f, clk.t)
	// oc_e moves up without reaching the head: only this cycle names it.
	f.Chats = []larkcli.RawChat{f.Chats[0], f.Chats[1], f.Chats[2], f.Chats[4], f.Chats[3]}
	f.AddMessage(msg("om_e", "oc_e", clk.t.Add(-time.Second), "late"))
	f.ListErr = map[string]error{"oc_e": &larkcli.Error{ExitCode: larkcli.ExitAPI, Subtype: "rate_limit", Code: 99991400}}
	sc := newScout(s.now, s.delayFor)

	_, err := s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err)
	sc.wg.Wait()
	_, err = s.scoutOnce(ctx, sc, clk.t)
	le, ok := errors.AsType[*larkcli.Error](err)
	require.True(t, ok, "the loop backs off for a listing that failed after the cycle returned")
	require.True(t, le.IsRateLimit())

	f.ListErr = nil
	_, err = s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err)
	sc.wg.Wait()
	_, err = s.Store.GetMessage(ctx, "om_e")
	require.ErrorIs(t, err, store.ErrNotFound, "a rate-limited chat rests before it is asked again")

	clk.t = clk.t.Add(s.delayFor(le, 1))
	_, err = s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err)
	sc.wg.Wait()
	_, err = s.Store.GetMessage(ctx, "om_e")
	require.NoError(t, err, "the ordering no longer names oc_e, so the failure has to")
}

func TestScoutOnce_ATimedOutListingIsOwedWithoutBackingOff(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	f.ListErr = map[string]error{"oc_a": handshakeTimeout()}
	buf := atLevel(s, slog.LevelInfo)
	sc := newScout(s.now, s.delayFor)

	_, err := s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err)
	sc.wg.Wait()
	_, err = s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err, "a timeout is an empty cycle, not a failure the loop backs off for")
	sc.wg.Wait()
	require.Equal(t, 1, callsTo(f, "list:chat:oc_a"), "the chat rests until the usual pace")
	line := logLine(buf, "discovery timed out")
	require.Contains(t, line, "level=INFO")
	require.Contains(t, line, "chat_id=oc_a")

	for i := range 3 {
		clk.t = clk.t.Add(s.Opt().PollInterval)
		_, err = s.scoutOnce(ctx, sc, clk.t)
		require.NoError(t, err)
		sc.wg.Wait()
		require.Equal(t, i+2, callsTo(f, "list:chat:oc_a"), "a timeout leaves the chat's wait where it was")
	}
}

func TestScoutOnce_AnotherNetworkFailureBacksOff(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	f.ListErr = map[string]error{"oc_a": &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network", Subtype: "dns"}}
	sc := newScout(s.now, s.delayFor)

	_, err := s.scoutOnce(ctx, sc, clk.t)
	require.NoError(t, err)
	sc.wg.Wait()
	_, err = s.scoutOnce(ctx, sc, clk.t)
	le, ok := errors.AsType[*larkcli.Error](err)
	require.True(t, ok, "the loop backs off for a dns failure")
	require.Equal(t, "dns", le.Subtype)
}

func TestScoutOnce_AChatThatKeepsFailingIsAskedAgainLessOften(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}}
	discovering(t, s, f, clk.t)
	// Not a *larkcli.Error, so nothing pins the failure on the chat itself:
	// the ordering names oc_a every cycle, and only its own wait holds it back.
	f.ListErr = map[string]error{"oc_a": errors.New("decode messages: unexpected end of JSON input")}
	sc := newScout(s.now, s.delayFor)
	cycle := func() {
		t.Helper()
		// Taking the failure first is what the loop does before its own pause,
		// so what holds the chat back here is its wait and nothing else.
		sc.failure()
		_, _ = s.scoutOnce(ctx, sc, clk.t)
		sc.wg.Wait()
	}

	base := s.Opt().PollInterval
	cycle()
	require.Equal(t, 1, callsTo(f, "list:chat:oc_a"))
	for i, gap := range []time.Duration{base, 2 * base, 4 * base} {
		clk.t = clk.t.Add(gap - time.Millisecond)
		cycle()
		require.Equal(t, i+1, callsTo(f, "list:chat:oc_a"), "the wait after failure %d is not up yet", i+1)
		clk.t = clk.t.Add(time.Millisecond)
		cycle()
		require.Equal(t, i+2, callsTo(f, "list:chat:oc_a"), "the wait after failure %d is up", i+1)
	}
}

func TestDiscover_NamesANewChatWithoutWaitingForTheFullListing(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_new", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a", P2PTargetType: "user"}}
	// The search found the chat's first message before any listing named it.
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_new", clk.t.UnixMilli()))

	_, _, err := s.discover(ctx, clk.t)
	require.NoError(t, err)
	got, err := s.Store.GetChat(ctx, "oc_new")
	require.NoError(t, err)
	require.Equal(t, "张三", got.Name)
	require.Equal(t, "ou_a", got.P2PTargetID)
	require.Zero(t, callsTo(f, "chats"), "the probe's own page carries the chat")
}
