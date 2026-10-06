package sync

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func newSyncer(t *testing.T) (*Syncer, *larkcli.Fake, *fakeClock) {
	t.Helper()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	clk := &fakeClock{t: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	s := &Syncer{Client: f, Store: st, Clock: clk}
	s.SetOptions(Options{
		PollInterval: time.Second, Overlap: 2 * time.Minute, ChatsRefreshEvery: 10 * time.Minute,
		SlowPathEvery: 10 * time.Minute, BackfillDays: 30, ActiveTopK: 30, BackfillPerTick: 5, RenderPerTick: 4, DownloadPerTick: 1, ForwardsPerTick: 3, ReadStatusPerTick: 4, RepairEvery: 6 * time.Hour, RepairPerTick: 3, MembersPerTick: 2, AvatarsPerTick: 5, ContactDetailsPerTick: larkcli.MaxUserIDsPerSearch, DocLinksPerTick: 1, ImageTextPerTick: 4,
	})
	return s, f, clk
}

func msg(id, chat string, at time.Time, text string) larkcli.RawMessage {
	return larkcli.RawMessage{MessageID: id, ChatID: chat, MsgType: "text",
		CreateTime: msAt(at), UpdateTime: msAt(at), MessagePosition: 1,
		Sender: larkcli.RawSender{ID: "ou_a", SenderType: "user", SenderName: "A"},
		Body:   larkcli.RawBody{Content: `{"text":"` + text + `"}`}}
}

func msAt(t time.Time) larkcli.Millis { return larkcli.Millis(t.UnixMilli()) }

// callsTo counts how often the fake was asked for one thing, for the tests
// that care how many times a tick spends a call rather than in what order.
func callsTo(f *larkcli.Fake, name string) int {
	n := 0
	for _, c := range f.Calls {
		if c == name {
			n++
		}
	}
	return n
}

func TestTick_FirstRunDiscoversRendersAndBackfills(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group", Avatar: "https://x/a.jpg"}}
	f.AddMessage(msg("om_new", "oc_a", now.Add(-30*time.Second), "fresh"))
	f.AddMessage(msg("om_old", "oc_a", now.Add(-48*time.Hour), "history"))
	f.AddMessage(msg("om_ancient", "oc_a", now.Add(-60*24*time.Hour), "too old"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.True(t, rep.Complete)
	require.Equal(t, 1, rep.Hits)
	require.Equal(t, 1, rep.New)
	require.Equal(t, 1, rep.Chats)
	require.Zero(t, rep.History, "first slice is 30 days back and empty")
	require.Equal(t, 2, rep.Backfilled, "om_new re-listed and om_old within 30 days; om_ancient excluded")
	require.Equal(t, 2, rep.Rendered)

	got, err := s.Store.GetMessage(ctx, "om_new")
	require.NoError(t, err)
	require.Equal(t, "fresh", got.Content, "a text body is its own words; no call was spent on it")
	_, err = s.Store.GetMessage(ctx, "om_ancient")
	require.ErrorIs(t, err, store.ErrNotFound)

	wm, ok, _ := s.Store.GetState(ctx, KeyWatermark)
	require.True(t, ok)
	require.Equal(t, now.UnixMilli(), mustInt(wm))
	chat, _ := s.Store.GetChat(ctx, "oc_a")
	require.Equal(t, "https://x/a.jpg", chat.AvatarURL)
	require.NotZero(t, chat.BackfillDoneAt)
	require.Equal(t, now.Add(-30*time.Second).UnixMilli(), chat.CursorMs, "cursor = newest message listed by backfill")

	// Second tick, past the search interval: nothing new; only the probe's
	// listing of the head, the live search and one history slice.
	f.Calls = nil
	clk.t = now.Add(searchEvery)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.New)
	require.Equal(t, []string{"active:30", "list:chat:oc_a", "search", "search"}, f.Calls,
		"the probe lists the head whether or not the ordering moved")
}

func TestHistorySlice_WalksDayByDayUntilLive(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	s.Opt().BackfillDays = 2
	s.Opt().BackfillPerTick = 0 // isolate the search-based history
	s.Opt().RepairEvery = 0
	f.AddMessage(msg("om_d1", "oc_a", now.Add(-40*time.Hour), "day1"))
	f.AddMessage(msg("om_d2", "oc_a", now.Add(-20*time.Hour), "day2"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.History, "slice [-48h,-24h] finds om_d1")
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.History, "slice [-24h, live start] finds om_d2")
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.History, "caught up")
	cur, _, _ := s.Store.GetState(ctx, KeyHistoryCursor)
	require.Equal(t, now.UnixMilli(), mustInt(cur), "cursor rests inside the live window, not on its moving edge")
}

func TestTick_BisectsTruncatedWindowAndPullsThreads(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Truncate = 3
	// Five hits in a 2-minute window exceed the cap; each 1-minute half fits.
	for i, sec := range []int{110, 90, 70, 30, 10} {
		f.AddMessage(msg("om_"+string(rune('a'+i)), "oc_a", now.Add(-time.Duration(sec)*time.Second), "x"))
	}
	root := msg("om_root", "oc_a", now.Add(-time.Hour), "root")
	root.ThreadID = "omt_1"
	f.AddMessage(root)
	reply := msg("om_reply", "oc_a", now.Add(-50*time.Minute), "reply")
	reply.ThreadID = "omt_1"
	reply.MessagePosition = -1
	f.AddMessage(reply)
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.True(t, rep.Complete, "bisection down to <=3 hits per sub-window covers everything")
	require.Equal(t, 5, rep.New)
	require.Equal(t, 4, countCalls(f.Calls, "search"), "whole window, two halves, one history slice")
	require.Contains(t, f.Calls, "list:thread:omt_1")
	got, err := s.Store.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.Equal(t, "omt_1", got.ThreadID)
}

func TestTick_AuthErrorSetsNeedsLoginAndProbesBeforeRetry(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	ctx := t.Context()
	f.Err = &larkcli.Error{ExitCode: larkcli.ExitAuth, Type: "auth", Subtype: "token_missing"}
	_, err := s.Tick(ctx)
	require.Error(t, err)
	s.SetStatus(ctx, err)
	st, _, _ := s.Store.GetState(ctx, KeyStatus)
	require.Equal(t, StatusNeedsLogin, st)
	require.Equal(t, 10*time.Minute, s.delayFor(err, 20))

	f.Calls = nil
	_, err = s.Tick(ctx)
	require.Error(t, err)
	require.Equal(t, []string{"whoami"}, f.Calls, "probe only, no API traffic while logged out")

	f.Err = nil
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	s.SetStatus(ctx, nil)
	st, _, _ = s.Store.GetState(ctx, KeyStatus)
	require.Equal(t, StatusRunning, st)
	self, _, _ := s.Store.GetState(ctx, KeySelfOpenID)
	require.Equal(t, "ou_self", self)
}

// handshakeTimeout is the shape every timeout in the daemon's log has taken.
func handshakeTimeout() *larkcli.Error {
	return &larkcli.Error{
		ExitCode: larkcli.ExitNetwork,
		Type:     "network",
		Subtype:  "timeout",
		Message:  `API call failed: Get "https://open.feishu.cn/...": net/http: TLS handshake timeout`,
	}
}

func TestTimeoutRun_ExcusesTimeoutsOnlyWithinTheGrace(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	timeout := fmt.Errorf("search: %w", handshakeTimeout())
	var run timeoutRun

	first := run.judge(timeout, at)
	require.True(t, excused(first), "the first timeout of a run is an empty cycle")
	require.ErrorIs(t, first, timeout, "excusing keeps the cause")
	require.True(t, excused(run.judge(timeout, at.Add(timeoutGrace-time.Millisecond))))
	require.False(t, excused(run.judge(timeout, at.Add(timeoutGrace))),
		"a run of nothing but timeouts this long is the path, not a blip")
	require.False(t, excused(run.judge(timeout, at.Add(2*timeoutGrace))), "and stays a failure until the run ends")

	require.NoError(t, run.judge(nil, at.Add(2*timeoutGrace)))
	require.True(t, excused(run.judge(timeout, at.Add(3*timeoutGrace))), "a success ends the run")

	dns := &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network", Subtype: "dns"}
	require.False(t, excused(run.judge(dns, at.Add(4*timeoutGrace))), "only a timeout is ever excused")
	require.True(t, excused(run.judge(timeout, at.Add(4*timeoutGrace))),
		"any other failure ends the run too: the loop has already reported it")
}

func TestSetStatus_AnExcusedTimeoutLeavesSyncState(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	ctx := t.Context()
	require.NoError(t, s.Store.SetState(ctx, KeyStatus, StatusError))
	require.NoError(t, s.Store.SetState(ctx, KeyLastError, "slow path: boom"))
	var run timeoutRun
	s.SetStatus(ctx, run.judge(fmt.Errorf("search: %w", handshakeTimeout()), s.now()))
	st, _, _ := s.Store.GetState(ctx, KeyStatus)
	require.Equal(t, StatusError, st, "an excused timeout says nothing of the login or the API")
	last, _, _ := s.Store.GetState(ctx, KeyLastError)
	require.Equal(t, "slow path: boom", last)
}

func TestSetStatus_ATimeoutPastTheGraceIsAnError(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	ctx := t.Context()
	err := fmt.Errorf("search: %w", handshakeTimeout())
	s.SetStatus(ctx, err)
	st, _, _ := s.Store.GetState(ctx, KeyStatus)
	require.Equal(t, StatusError, st, "a sync that has stopped says so")
	last, _, _ := s.Store.GetState(ctx, KeyLastError)
	require.Equal(t, err.Error(), last)
}

func TestSetStatus_OtherNetworkFailureIsAnError(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	ctx := t.Context()
	err := fmt.Errorf("search: %w", &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network", Subtype: "dns"})
	s.SetStatus(ctx, err)
	st, _, _ := s.Store.GetState(ctx, KeyStatus)
	require.Equal(t, StatusError, st)
	last, _, _ := s.Store.GetState(ctx, KeyLastError)
	require.Equal(t, err.Error(), last)
}

func TestPass_ATimeoutWithinTheGraceRecordsOK(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	ctx := t.Context()
	f.Err = handshakeTimeout()
	var run timeoutRun
	_, err := s.pass(ctx, false, &run)
	require.True(t, excused(err))
	runs, err := s.Store.LastRuns(ctx, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.True(t, runs[0].OK, "a timeout is an empty tick, not a failed run")
	require.Empty(t, runs[0].Error)
}

func TestPass_TimeoutsPastTheGraceRecordAFailure(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Err = handshakeTimeout()
	var run timeoutRun
	_, err := s.pass(ctx, false, &run)
	require.True(t, excused(err))

	clk.t = clk.t.Add(timeoutGrace)
	_, err = s.pass(ctx, false, &run)
	require.True(t, transientFailure(err))
	require.False(t, excused(err), "nothing but timeouts for this long is a sync that has stopped")
	runs, err := s.Store.LastRuns(ctx, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.False(t, runs[0].OK)
	require.Contains(t, runs[0].Error, "TLS handshake timeout")
}

func TestPass_UpstreamServerErrorRecordsAFailure(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	ctx := t.Context()
	f.Err = &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network", Subtype: "server_error", Code: 503}
	_, err := s.pass(ctx, false, &timeoutRun{})
	require.Error(t, err)
	runs, err := s.Store.LastRuns(ctx, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.False(t, runs[0].OK)
	require.NotEmpty(t, runs[0].Error)
}

func TestDelayFor_RateLimitHonoursRetryAfter(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	err := &larkcli.Error{ExitCode: 1, Subtype: "rate_limit", RetryAfter: 45 * time.Second}
	require.Equal(t, 45*time.Second, s.delayFor(err, 1))
	err.RetryAfter = 0
	require.Equal(t, 30*time.Second, s.delayFor(err, 1))
}

func TestDelayFor_KeepsASubSecondPollIntervalOutOfTheBackoff(t *testing.T) {
	t.Parallel()
	s, _, _ := newSyncer(t)
	s.Opt().PollInterval = 100 * time.Millisecond
	err := &larkcli.Error{ExitCode: 1, Subtype: "internal"}
	require.Equal(t, time.Second, s.delayFor(err, 1), "a tick pace is not a retry pace")
	require.Equal(t, 4*time.Second, s.delayFor(err, 3))
}

func TestOptionsFrom_ReadsThePollIntervalAsMilliseconds(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.PollIntervalMS = 250
	require.Equal(t, 250*time.Millisecond, OptionsFrom(cfg).PollInterval)
}

func TestSlowPath_ReconcilesActiveChatsFromCursor(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	// The head window of the active-time ordering is what the probe lists on
	// every tick; oc_a sits below it, so only the slow path reaches it.
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_head", Name: "Head", ChatMode: "group"},
		{ChatID: "oc_second", Name: "Second", ChatMode: "group"},
		{ChatID: "oc_third", Name: "Third", ChatMode: "group"},
		{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"},
	}
	f.AddMessage(msg("om_head", "oc_head", now.Add(-time.Hour), "head"))
	f.AddMessage(msg("om_1", "oc_a", now.Add(-time.Hour), "one"))
	_, err := s.Tick(ctx)
	require.NoError(t, err)

	// A message the search index never surfaces lands outside the fast window.
	f.AddMessage(msg("om_hidden", "oc_a", now.Add(-30*time.Minute), "hidden"))

	f.Calls = nil
	s.Opt().ActiveTopK = 20
	clk.t = now.Add(11 * time.Minute) // slow path due; fast window [wm-2m, now] misses -30m
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, rep.SlowPath, "both chats re-listed from cursor-overlap, and om_hidden with them")
	_, err = s.Store.GetMessage(ctx, "om_hidden")
	require.NoError(t, err)

	require.Equal(t, 1, callsTo(f, "active:20"),
		"the slow path reads the ordering at its own length, not discovery's")
}

func mustInt(s string) int64 {
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}

func countCalls(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

func TestBackfill_PermanentChatErrorIsRecordedAndSkipped(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_restricted", Name: "R", ChatMode: "group"}, {ChatID: "oc_ok", Name: "OK", ChatMode: "group"}}
	f.AddMessage(msg("om_ok", "oc_ok", clk.t.Add(-time.Hour), "fine"))
	f.ListErr = map[string]error{"oc_restricted": &larkcli.Error{ExitCode: larkcli.ExitAPI, Type: "api", Subtype: "unknown", Code: 231203, Message: "restricted"}}

	rep, err := s.Tick(ctx)
	require.NoError(t, err, "one bad chat must not abort the tick")
	require.Equal(t, 1, rep.Backfilled)
	c, _ := s.Store.GetChat(ctx, "oc_restricted")
	require.Equal(t, "231203: restricted", c.SyncError)
	require.NotZero(t, c.BackfillDoneAt)
	pending, _ := s.Store.ChatsNeedingBackfill(ctx, 10)
	require.Empty(t, pending)

	// Rate limits still abort so the loop backs off.
	f.ListErr = map[string]error{"oc_ok": &larkcli.Error{ExitCode: larkcli.ExitAPI, Subtype: "rate_limit"}}
	clk.t = clk.t.Add(11 * time.Minute)
	_, err = s.Tick(ctx)
	require.Error(t, err)
}

func TestTick_MuteRidesTheChatRefresh(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"},
		{ChatID: "oc_b", Name: "Beta", ChatMode: "group"},
	}
	f.Muted = map[string]bool{"oc_a": true}
	// Already stored, so discovery re-lists them without an arrival and the
	// refresh is the only path left to ask.
	for _, m := range []larkcli.RawMessage{
		msg("om_a", "oc_a", now.Add(-time.Minute), "hi"),
		msg("om_b", "oc_b", now.Add(-time.Minute), "hi"),
	} {
		f.AddMessage(m)
		require.NoError(t, s.Store.EnsureChat(ctx, m.ChatID, now.UnixMilli()))
		_, err := s.Store.UpsertMessages(ctx, []store.Message{ToRow(m)}, now.UnixMilli())
		require.NoError(t, err)
	}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Muted)
	require.Equal(t, 1, callsTo(f, "mute-status"), "both chats ride the one refresh call")

	chats, err := s.Store.ListChats(ctx, store.ChatQuery{})
	require.NoError(t, err)
	byID := map[string]store.Chat{}
	for _, c := range chats {
		byID[c.ChatID] = c
	}
	require.True(t, byID["oc_a"].Muted)
	require.False(t, byID["oc_b"].Muted)
	require.Equal(t, now.UnixMilli(), byID["oc_a"].MuteCheckedAt)

	clk.t = clk.t.Add(time.Minute)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Muted, "the lookup is paced by the chat refresh it rides")
}

func TestTick_FiresOnChangeIncludingOnFailure(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	ctx := t.Context()
	fired := 0
	s.OnChange = func() { fired++ }

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, fired)

	f.Err = errors.New("boom")
	_, err = s.Tick(ctx)
	require.Error(t, err)
	require.Equal(t, 2, fired, "a failed tick may still have written, so the consumer is woken either way")
}

func TestRefreshReadStatus_OvertakesTheBackoff(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	f.AddMessage(msg("om_here", "oc_here", now.Add(-time.Hour), "hi"))
	f.AddMessage(msg("om_elsewhere", "oc_other", now.Add(-time.Hour), "hi"))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_here", ChatID: "oc_here", SenderID: "ou_a", CreateMs: now.Add(-time.Hour).UnixMilli(), RawJSON: "{}"},
		{MessageID: "om_elsewhere", ChatID: "oc_other", SenderID: "ou_a", CreateMs: now.Add(-time.Hour).UnixMilli(), RawJSON: "{}"},
	}, now.UnixMilli())
	require.NoError(t, err)
	unread := false
	require.NoError(t, s.Store.SetReadStatus(ctx, "om_here", &unread, now.UnixMilli(), now.Add(6*time.Hour).UnixMilli()))
	require.NoError(t, s.Store.SetReadStatus(ctx, "om_elsewhere", &unread, now.UnixMilli(), now.Add(6*time.Hour).UnixMilli()))

	n, err := s.pollReadStatus(ctx, now)
	require.NoError(t, err)
	require.Zero(t, n, "both wait out the backoff")

	f.Read["om_here"] = true
	f.Read["om_elsewhere"] = true
	n, err = s.RefreshReadStatus(ctx, "oc_here")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	m, _ := s.Store.GetMessage(ctx, "om_here")
	require.True(t, *m.IsReadRemote)
	m, _ = s.Store.GetMessage(ctx, "om_elsewhere")
	require.False(t, *m.IsReadRemote, "another chat's backoff is untouched")
}

func TestRefreshReadStatus_SpendsNoCallWhenNothingIsUnread(t *testing.T) {
	t.Parallel()
	s, f, _ := newSyncer(t)
	ctx := t.Context()
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))

	n, err := s.RefreshReadStatus(ctx, "oc_empty")
	require.NoError(t, err)
	require.Zero(t, n)
	require.NotContains(t, f.Calls, "read-status")
}

func TestPollReadStatus_DropsUnreadPastTheHorizon(t *testing.T) {
	t.Parallel()
	s, _, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	old := now.Add(-readStatusHorizon - time.Hour)
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_stale", ChatID: "oc", SenderID: "ou_a", CreateMs: old.UnixMilli(), RawJSON: "{}"},
	}, now.UnixMilli())
	require.NoError(t, err)
	unread := false
	require.NoError(t, s.Store.SetReadStatus(ctx, "om_stale", &unread, old.UnixMilli(), old.UnixMilli()))
	count, _ := s.Store.UnreadCount(ctx)
	require.Equal(t, int64(1), count)

	_, err = s.pollReadStatus(ctx, now)
	require.NoError(t, err)

	count, _ = s.Store.UnreadCount(ctx)
	require.Zero(t, count, "beyond the horizon nothing can refresh it, so it stops claiming unread")
}

func TestTick_SystemMessagesRenderInProcess(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.AddMessage(larkcli.RawMessage{MessageID: "om_sys", ChatID: "oc_a", MsgType: "system",
		CreateTime: msAt(now.Add(-time.Minute)), UpdateTime: msAt(now.Add(-time.Minute)), MessagePosition: 1,
		Body: larkcli.RawBody{Content: `{"template":"{from_user} renamed the group to \"{group_name}\".","from_user":["A"],"to_chatters":[]}`}})

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Rendered)
	require.NotContains(t, f.Calls, "render", "a system message needs no render call")

	m, err := s.Store.GetMessage(ctx, "om_sys")
	require.NoError(t, err)
	require.Equal(t, `A renamed the group to "…".`, m.Content)
}

func TestRenderLocal_NamesTheCallInBothPanes(t *testing.T) {
	t.Parallel()
	// A call still running is the chat's last message until the marker that
	// closes it arrives, so the list has to say which meeting it was.
	s, _, clk := newSyncer(t)
	ctx := t.Context()
	start := clk.t.UnixMilli()
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", start))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_call", ChatID: "oc_a", MsgType: "video_chat", CreateMs: start, UpdateMs: start,
			MessagePosition: 1, ContentRaw: `{"topic":"站会的视频会议","meet_number":"100000000","start_time":"` +
				strconv.FormatInt(start, 10) + `"}`, RawJSON: "{}"},
	}, start)
	require.NoError(t, err)

	n, err := s.renderLocal(ctx, nil, 50, clk.t)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := s.Store.MessagesByIDs(ctx, []string{"om_call"})
	require.NoError(t, err)
	require.Equal(t, "[Video call] 站会的视频会议 · 100000000", got["om_call"].Content,
		"a call still running has no length yet")

	c, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, "[Video call] 站会的视频会议 · 100000000", c.LastContent)
}

func TestRenderLocal_TimesTheCallItsMarkerClosesForBothPanes(t *testing.T) {
	t.Parallel()
	s, _, clk := newSyncer(t)
	ctx := t.Context()
	start := clk.t.UnixMilli()
	end := start + 32_000
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", start))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_call", ChatID: "oc_a", MsgType: "video_chat", CreateMs: start, UpdateMs: end,
			MessagePosition: 1, ContentRaw: videoChatBody(start, end), RawJSON: "{}"},
		{MessageID: "om_end", ChatID: "oc_a", MsgType: "system", CreateMs: end + 909, UpdateMs: end + 909,
			MessagePosition: 2, ContentRaw: blankTemplate, RawJSON: "{}"},
	}, start)
	require.NoError(t, err)

	n, err := s.renderLocal(ctx, nil, 50, clk.t)
	require.NoError(t, err)
	require.Equal(t, 2, n, "the call and the marker closing it are both rendered in process")

	got, err := s.Store.MessagesByIDs(ctx, []string{"om_call", "om_end"})
	require.NoError(t, err)
	require.Equal(t, "Meeting ended: 32s", got["om_end"].Content)
	require.Equal(t, "[Video call] 站会的视频会议 · 100000000 · 32s", got["om_call"].Content)

	c, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, "Meeting ended: 32s", c.LastContent, "the chat list reads the same rendering")
}

func TestTick_RendersNewMessagesBeforeSweeps(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	// A msg_type larkim has no renderer for, which is what still reaches
	// lark-cli; this test is about where that call sits among the sweeps.
	shared := msg("om_new", "oc_a", clk.t.Add(-30*time.Second), "")
	shared.MsgType, shared.Body.Content = "brand_new", `{}`
	f.AddMessage(shared)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.New)

	render := slices.Index(f.Calls, "render")
	require.GreaterOrEqual(t, render, 0, "the new message was rendered")
	sweep := slices.IndexFunc(f.Calls, func(c string) bool {
		// The activity probe ("active:30") is discovery, not a sweep: it runs
		// before the fast path so the fast path has something to render.
		return strings.HasPrefix(c, "list:") || c == "chats"
	})
	require.GreaterOrEqual(t, sweep, 0, "a sweep ran in the same tick")
	require.Less(t, render, sweep, "a reader waits on the rendering; nobody waits on the sweeps")
}

func TestTick_NudgesAsEachStageLands(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	var nudges int
	s.OnChange = func() { nudges++ }
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_new", "oc_a", clk.t.Add(-30*time.Second), "fresh"))

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Greater(t, nudges, 1, "a body and its rendering land at different moments")
}

func TestHistorySlice_StopsSearchingOnceCaughtUp(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().BackfillDays = 1
	s.Opt().BackfillPerTick = 0 // isolate the search-based history
	s.Opt().RepairEvery = 0

	// Warm up until history reaches the live window, advancing the clock the
	// way Run does: a frozen clock freezes liveStart with it and hides the bug.
	for range 3 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(searchEvery)
	}

	f.Calls = nil
	for range 3 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(searchEvery)
	}
	require.Equal(t, []string{"active:30", "search", "active:30", "search", "active:30", "search"}, f.Calls,
		"one live search per interval; history is caught up and must not re-search behind it")
}

func TestHistorySlice_LiveWindowCoversAnOutageWithoutHistory(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().BackfillDays = 1
	s.Opt().BackfillPerTick = 0
	s.Opt().RepairEvery = 0

	for range 3 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(5 * time.Second)
	}

	// The loop stops for ten minutes. The watermark stalls with it, so the
	// next live window reaches back over the whole gap on its own.
	gap := clk.t
	clk.t = clk.t.Add(10 * time.Minute)
	f.AddMessage(msg("om_gap", "oc_a", gap.Add(3*time.Minute), "sent while stopped"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.New, "the live window spans the outage")
	require.Zero(t, rep.History, "history stays out of it")
	got, err := s.Store.GetMessage(ctx, "om_gap")
	require.NoError(t, err)
	require.Equal(t, "oc_a", got.ChatID)
}

func TestActiveProbe_NamesChatsThatMovedUp(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().BackfillPerTick = 0
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
		{ChatID: "oc_c", Name: "张三", ChatMode: "p2p"},
		{ChatID: "oc_d", Name: "李四", ChatMode: "p2p"},
	}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Moved, "a first run has no ordering to compare against")

	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, activeHead, rep.Moved, "an unchanged ordering still names the head window")

	// A message in oc_d puts it at position 1, which is the whole signal.
	f.Chats = []larkcli.RawChat{f.Chats[3], f.Chats[0], f.Chats[1], f.Chats[2]}
	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, activeHead, rep.Moved, "the promoted chat heads the window, named once")
	require.Zero(t, rep.Probed, "listing the head window every tick must not report its old messages as news")

	order, ok, err := s.Store.GetState(ctx, KeyActiveOrder)
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `["oc_d","oc_a","oc_b","oc_c"]`, order, "the probe records what it saw for the next tick")
}

func TestActiveProbe_UnreadableOrderCountsAsAFirstRun(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().BackfillPerTick = 0
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}, {ChatID: "oc_b", ChatMode: "group"}}
	require.NoError(t, s.Store.SetState(ctx, KeyActiveOrder, "not json"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err, "a corrupt value must not fail every tick")
	require.Zero(t, rep.Moved, "with no ordering to compare against, not even the head is named")

	f.Chats = []larkcli.RawChat{f.Chats[1], f.Chats[0]}
	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Moved, "the tick that rewrote it compares against it again")
}

func TestActiveProbe_ReachesAMessageBeforeTheSearchDoes(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
	}
	f.AddMessage(msg("om_seed", "oc_b", clk.t.Add(-time.Hour), "seed"))

	// Warm up: backfill both chats so the probe has cursors to pull from.
	for range 2 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(5 * time.Second)
	}

	// A message in oc_b puts it at position 1. The search index has not caught
	// up, which the fake stands in for by holding the hit back.
	fresh := msg("om_fresh", "oc_b", clk.t.Add(-time.Second), "")
	fresh.MsgType, fresh.Body.Content = "brand_new", `{}`
	f.AddMessage(fresh)
	f.SearchHidden = []string{"om_fresh"}
	f.Chats = []larkcli.RawChat{f.Chats[1], f.Chats[0]}
	clk.t = clk.t.Add(searchEvery) // let the safety net run, so the order is visible
	f.Calls = nil

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Moved, "both chats sit in the head window")
	require.Equal(t, 1, rep.Probed, "only the new message counts; the cursor overlap re-lists the chat's older one")
	require.Zero(t, rep.New, "the search had nothing left to find")
	require.Equal(t, "active:30", f.Calls[0])
	require.ElementsMatch(t, []string{"list:chat:oc_a", "list:chat:oc_b"}, f.Calls[1:3], "the window is listed side by side")
	require.Equal(t, []string{"render", "search"},
		f.Calls[3:5], "what the probe found is rendered before the search asks")

	m, err := s.Store.GetMessage(ctx, "om_fresh")
	require.NoError(t, err)
	require.Equal(t, "oc_b", m.ChatID)
}

func TestActiveProbe_ReachesASecondMessageInTheChatAlreadyAtTheHead(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
	}
	f.AddMessage(msg("om_seed", "oc_a", clk.t.Add(-time.Hour), "seed"))
	for range 2 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(5 * time.Second)
	}

	// A reply into the chat already at position 1 leaves the ordering exactly
	// as it was, so nothing about it moved; the head rule is what reaches it.
	f.AddMessage(msg("om_reply", "oc_a", clk.t.Add(-time.Second), "reply"))
	f.SearchHidden = []string{"om_reply"}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.New, "the search index has not caught up")
	require.Equal(t, 1, rep.Probed)

	m, err := s.Store.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.Equal(t, "oc_a", m.ChatID)
}

func TestActiveProbe_ReachesAMessageInAChatThatKeptItsPlace(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
		{ChatID: "oc_c", Name: "构建机器人", ChatMode: "p2p"},
		{ChatID: "oc_d", Name: "张三", ChatMode: "p2p"},
	}
	f.AddMessage(msg("om_seed", "oc_a", clk.t.Add(-time.Hour), "seed"))
	for range 2 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(5 * time.Second)
	}

	// oc_b is written in and then oc_a after it, so oc_b holds second place:
	// the same ordering a stale page shows while it has yet to take in oc_b.
	f.AddMessage(msg("om_second", "oc_b", clk.t.Add(-2*time.Second), "second"))
	f.AddMessage(msg("om_head", "oc_a", clk.t.Add(-time.Second), "head"))
	f.SearchHidden = []string{"om_second", "om_head"}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.New, "the search index has not caught up")
	require.Equal(t, 2, rep.Probed)
	m, err := s.Store.GetMessage(ctx, "om_second")
	require.NoError(t, err)
	require.Equal(t, "oc_b", m.ChatID)
}

func TestTick_SearchIsASafetyNetOnItsOwnInterval(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().BackfillPerTick = 0
	s.Opt().RepairEvery = 0
	s.Opt().BackfillDays = 0 // history caught up from the start, so only the live search counts
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_seed", "oc_a", clk.t.Add(-time.Hour), "seed"))

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	clk.t = clk.t.Add(searchEvery - time.Second)
	f.Calls = nil
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, callsTo(f, "search"), "inside the interval the probe carries discovery alone")
	require.Contains(t, f.Calls, "active:30", "the probe still runs every tick")
	require.False(t, rep.Searched)
	require.False(t, rep.Complete, "a tick that searched nothing covered nothing")

	clk.t = clk.t.Add(time.Second)
	f.Calls = nil
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "search"), "past the interval the safety net runs once")
	require.True(t, rep.Searched)
	require.True(t, rep.Complete)
}

func TestActiveProbe_AFailedPullLeavesTheMovedChatsNamedNextTick(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().BackfillPerTick = 0
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
		{ChatID: "oc_c", Name: "张三", ChatMode: "p2p"},
	}
	backfilled(t, s, clk.t, "oc_a", "oc_b", "oc_c")

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	// oc_c moved up without reaching the head, which is the one position the
	// next ordering cannot recover: at the head it would be named every tick
	// regardless.
	f.AddMessage(msg("om_late", "oc_c", clk.t, "late"))
	f.SearchHidden = []string{"om_late"} // only the probe can reach it
	f.Chats = []larkcli.RawChat{f.Chats[1], f.Chats[2], f.Chats[0]}
	f.ListErr = map[string]error{"oc_c": errors.New("gateway said no")}
	clk.t = clk.t.Add(5 * time.Second)

	_, err = s.Tick(ctx)
	require.Error(t, err)
	order, ok, err := s.Store.GetState(ctx, KeyActiveOrder)
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `["oc_a","oc_b","oc_c"]`, order, "a failed pull must not consume the delta it failed on")

	f.ListErr = nil
	clk.t = clk.t.Add(5 * time.Second)
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Probed, "the retained ordering names oc_c again")
}

func TestProbeReadStatus_TakesAChatOutOfTheSweepOnceTheClientAnswers(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	f.AddMessage(msg("om_walked", "oc_walked", now.Add(-time.Hour), "hi"))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{{MessageID: "om_walked", ChatID: "oc_walked",
		SenderID: "ou_a", CreateMs: now.Add(-time.Hour).UnixMilli(), RawJSON: "{}"}}, now.UnixMilli())
	require.NoError(t, err)
	require.NoError(t, s.Store.SetReadStatus(ctx, "om_walked", new(false), now.UnixMilli(), now.Add(6*time.Hour).UnixMilli()))
	_, err = s.Store.AcceptRemoteRead(ctx, "oc_walked", 0)
	require.NoError(t, err)
	chats, err := s.Store.ChatsWithUnread(ctx)
	require.NoError(t, err)
	require.Empty(t, chats, "accept is the receipt the badge follows")

	f.Read["om_walked"] = true
	_, err = s.probeReadStatus(ctx, now)
	require.NoError(t, err)

	m, _ := s.Store.GetMessage(ctx, "om_walked")
	require.True(t, *m.IsReadRemote)
	probes, err := s.Store.ReadStatusProbes(ctx, store.ReadCheckQuery{Self: "ou_me", SinceMs: 100, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, probes, "confirm closes the unconfirmed accept slot")
	chats, err = s.Store.ChatsWithUnread(ctx)
	require.NoError(t, err)
	require.Empty(t, chats)
}

func TestProbeReadStatus_RevertsWhenFeishuStillReportsUnread(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	now := clk.t
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))
	f.AddMessage(msg("om_lie", "oc_lie", now.Add(-time.Hour), "hi"))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{{MessageID: "om_lie", ChatID: "oc_lie",
		SenderID: "ou_a", CreateMs: now.Add(-time.Hour).UnixMilli(), RawJSON: "{}"}}, now.UnixMilli())
	require.NoError(t, err)
	require.NoError(t, s.Store.SetReadStatus(ctx, "om_lie", new(false), now.UnixMilli(), now.Add(6*time.Hour).UnixMilli()))
	_, err = s.Store.AcceptRemoteRead(ctx, "oc_lie", 0)
	require.NoError(t, err)

	f.Read["om_lie"] = false
	_, err = s.probeReadStatus(ctx, now)
	require.NoError(t, err)

	m, _ := s.Store.GetMessage(ctx, "om_lie")
	require.False(t, *m.IsReadRemote)
	chats, err := s.Store.ChatsWithUnread(ctx)
	require.NoError(t, err)
	require.Len(t, chats, 1)
}
