package sync

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func newSyncer(t *testing.T) (*Syncer, *larkcli.Fake, *fakeClock) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	clk := &fakeClock{t: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	s := &Syncer{Client: f, Store: st, Clock: clk, Opt: Options{
		PollInterval: time.Second, Overlap: 2 * time.Minute, ChatsRefreshEvery: 10 * time.Minute,
		SlowPathEvery: 10 * time.Minute, BackfillDays: 30, ActiveTopK: 30, BackfillPerTick: 5, RenderPerTick: 4, DownloadPerTick: 1, ReadStatusPerTick: 4, RepairEvery: 6 * time.Hour, RepairPerTick: 3, MembersPerTick: 2, AvatarsPerTick: 5, ContactDetailsPerTick: larkcli.MaxUserIDsPerSearch,
	}}
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
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
	require.Equal(t, `rendered:{"text":"fresh"}`, got.Content)
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
	require.Equal(t, []string{"chats:true", "list:chat:oc_a", "search", "search"}, f.Calls,
		"the probe lists the head whether or not the ordering moved")
}

func TestHistorySlice_WalksDayByDayUntilLive(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	s.Opt.BackfillDays = 2
	s.Opt.BackfillPerTick = 0 // isolate the search-based history
	s.Opt.RepairEvery = 0
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
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
	s, f, _ := newSyncer(t)
	ctx := context.Background()
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

func TestDelayFor_RateLimitHonoursRetryAfter(t *testing.T) {
	s, _, _ := newSyncer(t)
	err := &larkcli.Error{ExitCode: 1, Subtype: "rate_limit", RetryAfter: 45 * time.Second}
	require.Equal(t, 45*time.Second, s.delayFor(err, 1))
	err.RetryAfter = 0
	require.Equal(t, 30*time.Second, s.delayFor(err, 1))
}

func TestSlowPath_ReconcilesActiveChatsFromCursor(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	// oc_head takes the head of the active-time ordering, which the probe
	// lists on every tick; oc_a sits below it, so only the slow path reaches it.
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_head", Name: "Head", ChatMode: "group"},
		{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"},
	}
	f.AddMessage(msg("om_head", "oc_head", now.Add(-time.Hour), "head"))
	f.AddMessage(msg("om_1", "oc_a", now.Add(-time.Hour), "one"))
	_, err := s.Tick(ctx)
	require.NoError(t, err)

	// A message the search index never surfaces lands outside the fast window.
	f.AddMessage(msg("om_hidden", "oc_a", now.Add(-30*time.Minute), "hidden"))

	f.Calls = nil
	clk.t = now.Add(11 * time.Minute) // slow path due; fast window [wm-2m, now] misses -30m
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, rep.SlowPath, "both chats re-listed from cursor-overlap, and om_hidden with them")
	_, err = s.Store.GetMessage(ctx, "om_hidden")
	require.NoError(t, err)

	require.Equal(t, 1, callsTo(f, "chats:true"),
		"the slow path reconciles the ordering the probe already listed")
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"},
		{ChatID: "oc_b", Name: "Beta", ChatMode: "group"},
	}
	f.Muted = map[string]bool{"oc_a": true}
	f.AddMessage(msg("om_a", "oc_a", now.Add(-time.Minute), "hi"))
	f.AddMessage(msg("om_b", "oc_b", now.Add(-time.Minute), "hi"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Muted)

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
	s, f, _ := newSyncer(t)
	ctx := context.Background()
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
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
	s, f, _ := newSyncer(t)
	ctx := context.Background()
	require.NoError(t, s.Store.SetState(ctx, KeySelfOpenID, "ou_me"))

	n, err := s.RefreshReadStatus(ctx, "oc_empty")
	require.NoError(t, err)
	require.Zero(t, n)
	require.NotContains(t, f.Calls, "read-status")
}

func TestPollReadStatus_DropsUnreadPastTheHorizon(t *testing.T) {
	s, _, clk := newSyncer(t)
	ctx := context.Background()
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.AddMessage(larkcli.RawMessage{MessageID: "om_sys", ChatID: "oc_a", MsgType: "system",
		CreateTime: msAt(now.Add(-time.Minute)), UpdateTime: msAt(now.Add(-time.Minute)), MessagePosition: 1,
		Body: larkcli.RawBody{Content: `{"template":"{from_user} renamed the group to \"{group_name}\".","from_user":["A"],"to_chatters":[]}`}})

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Rendered)
	require.NotContains(t, f.Calls, "render:false", "a system message needs no render call")

	m, err := s.Store.GetMessage(ctx, "om_sys")
	require.NoError(t, err)
	require.Equal(t, `A renamed the group to "…".`, m.Content)
}

func TestRenderLocal_NamesTheCallInBothPanes(t *testing.T) {
	// A call still running is the chat's last message until the marker that
	// closes it arrives, so the list has to say which meeting it was.
	s, _, clk := newSyncer(t)
	ctx := context.Background()
	start := clk.t.UnixMilli()
	require.NoError(t, s.Store.EnsureChat(ctx, "oc_a", start))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_call", ChatID: "oc_a", MsgType: "video_chat", CreateMs: start, UpdateMs: start,
			MessagePosition: 1, ContentRaw: `{"topic":"站会的视频会议","meet_number":"100000000","start_time":"` +
				strconv.FormatInt(start, 10) + `"}`, RawJSON: "{}"},
	}, start)
	require.NoError(t, err)

	n, err := s.renderLocal(ctx, clk.t)
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
	s, _, clk := newSyncer(t)
	ctx := context.Background()
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

	n, err := s.renderLocal(ctx, clk.t)
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
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_new", "oc_a", clk.t.Add(-30*time.Second), "fresh"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.New)

	render := slices.Index(f.Calls, "render:false")
	require.GreaterOrEqual(t, render, 0, "the new message was rendered")
	sweep := slices.IndexFunc(f.Calls, func(c string) bool {
		// The activity probe ("chats:true") is discovery, not a sweep: it runs
		// before the fast path so the fast path has something to render.
		return strings.HasPrefix(c, "list:") || c == "chats:false"
	})
	require.GreaterOrEqual(t, sweep, 0, "a sweep ran in the same tick")
	require.Less(t, render, sweep, "a reader waits on the rendering; nobody waits on the sweeps")
}

func TestTick_NudgesAsEachStageLands(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	var nudges int
	s.OnChange = func() { nudges++ }
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_new", "oc_a", clk.t.Add(-30*time.Second), "fresh"))

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Greater(t, nudges, 1, "a body and its rendering land at different moments")
}

func TestHistorySlice_StopsSearchingOnceCaughtUp(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.BackfillDays = 1
	s.Opt.BackfillPerTick = 0 // isolate the search-based history
	s.Opt.RepairEvery = 0

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
	require.Equal(t, []string{"chats:true", "search", "chats:true", "search", "chats:true", "search"}, f.Calls,
		"one live search per interval; history is caught up and must not re-search behind it")
}

func TestHistorySlice_LiveWindowCoversAnOutageWithoutHistory(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.BackfillDays = 1
	s.Opt.BackfillPerTick = 0
	s.Opt.RepairEvery = 0

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
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.BackfillPerTick = 0
	s.Opt.RepairEvery = 0
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
		{ChatID: "oc_c", Name: "张三", ChatMode: "p2p"},
	}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Moved, "a first run has no ordering to compare against")

	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Moved, "an unchanged ordering still names the head")

	// A message in oc_c puts it at position 1, which is the whole signal.
	f.Chats = []larkcli.RawChat{f.Chats[2], f.Chats[0], f.Chats[1]}
	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Moved, "the promoted chat is the head, named once")
	require.Zero(t, rep.Probed, "listing the head every tick must not report its old messages as news")

	order, ok, err := s.Store.GetState(ctx, KeyActiveOrder)
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `["oc_c","oc_a","oc_b"]`, order, "the probe records what it saw for the next tick")
}

func TestActiveProbe_UnreadableOrderCountsAsAFirstRun(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.BackfillPerTick = 0
	s.Opt.RepairEvery = 0
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", ChatMode: "group"}, {ChatID: "oc_b", ChatMode: "group"}}
	require.NoError(t, s.Store.SetState(ctx, KeyActiveOrder, "not json"))

	rep, err := s.Tick(ctx)
	require.NoError(t, err, "a corrupt value must not fail every tick")
	require.Zero(t, rep.Moved, "with no ordering to compare against, not even the head is named")

	f.Chats = []larkcli.RawChat{f.Chats[1], f.Chats[0]}
	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Moved, "the tick that rewrote it sees the next move")
}

func TestActiveProbe_ReachesAMessageBeforeTheSearchDoes(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.RepairEvery = 0
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
	f.AddMessage(msg("om_fresh", "oc_b", clk.t.Add(-time.Second), "fresh"))
	f.SearchHidden = []string{"om_fresh"}
	f.Chats = []larkcli.RawChat{f.Chats[1], f.Chats[0]}
	clk.t = clk.t.Add(searchEvery) // let the safety net run, so the order is visible
	f.Calls = nil

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Moved)
	require.Equal(t, 1, rep.Probed, "only the new message counts; the cursor overlap re-lists the chat's older one")
	require.Zero(t, rep.New, "the search had nothing left to find")
	require.Equal(t, []string{"chats:true", "list:chat:oc_b", "search", "render:false"},
		f.Calls[:4], "the probe pulls before the search asks, and what it found is rendered with the rest")

	m, err := s.Store.GetMessage(ctx, "om_fresh")
	require.NoError(t, err)
	require.Equal(t, "oc_b", m.ChatID)
}

func TestActiveProbe_ReachesASecondMessageInTheChatAlreadyAtTheHead(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.RepairEvery = 0
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

func TestTick_SearchIsASafetyNetOnItsOwnInterval(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.BackfillPerTick = 0
	s.Opt.RepairEvery = 0
	s.Opt.BackfillDays = 0 // history caught up from the start, so only the live search counts
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_seed", "oc_a", clk.t.Add(-time.Hour), "seed"))

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	clk.t = clk.t.Add(searchEvery - time.Second)
	f.Calls = nil
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, callsTo(f, "search"), "inside the interval the probe carries discovery alone")
	require.Contains(t, f.Calls, "chats:true", "the probe still runs every tick")

	clk.t = clk.t.Add(time.Second)
	f.Calls = nil
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "search"), "past the interval the safety net runs once")
}
