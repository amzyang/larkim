package sync

import (
	"context"
	"path/filepath"
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
		SlowPathEvery: 10 * time.Minute, BackfillDays: 30, ActiveTopK: 30, BackfillPerTick: 5, RenderPerTick: 4, DownloadPerTick: 1, ReadStatusPerTick: 4,
	}}
	return s, f, clk
}

func msg(id, chat string, at time.Time, text string) larkcli.RawMessage {
	return larkcli.RawMessage{MessageID: id, ChatID: chat, MsgType: "text",
		CreateTime: msAt(at), UpdateTime: msAt(at), MessagePosition: 1,
		Sender: larkcli.RawSender{ID: "ou_a", SenderType: "user", SenderName: "A"},
		Body:   larkcli.RawBody{Content: `{"text":"` + text + `"}`}}
}

func msAt(t time.Time) larkcli.Millis { return larkcli.Ms(t.UnixMilli()) }

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

	// Second tick: nothing new; only the live search and one history slice.
	f.Calls = nil
	clk.t = now.Add(10 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.New)
	require.Equal(t, []string{"search", "search"}, f.Calls)
}

func TestHistorySlice_WalksDayByDayUntilLive(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	s.Opt.BackfillDays = 2
	s.Opt.BackfillPerTick = 0 // isolate the search-based history
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
	require.Equal(t, now.Add(-2*time.Minute).UnixMilli(), mustInt(cur), "cursor stops at the live window start")
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
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.AddMessage(msg("om_1", "oc_a", now.Add(-time.Hour), "one"))
	_, err := s.Tick(ctx)
	require.NoError(t, err)

	// A message the search index never surfaces lands outside the fast window.
	f.AddMessage(msg("om_hidden", "oc_a", now.Add(-30*time.Minute), "hidden"))

	clk.t = now.Add(11 * time.Minute) // slow path due; fast window [wm-2m, now] misses -30m
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.SlowPath, "om_1 re-listed from cursor-overlap plus om_hidden")
	_, err = s.Store.GetMessage(ctx, "om_hidden")
	require.NoError(t, err)
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
