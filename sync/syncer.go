package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// Clock supplies time so ticks are reproducible in tests.
type Clock interface{ Now() time.Time }

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Options tunes the sync loop.
type Options struct {
	PollInterval      time.Duration
	Overlap           time.Duration
	ChatsRefreshEvery time.Duration
	SlowPathEvery     time.Duration
	BackfillDays      int
	ActiveTopK        int
	BackfillPerTick   int
	RenderPerTick     int // batches of 50
}

// OptionsFrom maps the user config onto loop options.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		PollInterval:      cfg.PollInterval,
		Overlap:           cfg.Overlap,
		ChatsRefreshEvery: cfg.ChatsRefreshEvery,
		SlowPathEvery:     cfg.SlowPathEvery,
		BackfillDays:      cfg.BackfillDays,
		ActiveTopK:        cfg.ActiveTopK,
		BackfillPerTick:   10,
		RenderPerTick:     4,
	}
}

// State keys in sync_state.
const (
	KeyWatermark      = "watermark_ms"
	KeySelfOpenID     = "self_open_id"
	KeyStatus         = "status"
	KeyLastError      = "last_error"
	KeyLastTickAt     = "last_tick_at"
	KeyChatsRefreshed = "chats_refreshed_at"
	KeySlowPathAt     = "slow_path_at"
	// KeyHistoryCursor is the start of the next day-slice of historical search;
	// history is complete once it reaches the live window.
	KeyHistoryCursor = "history_cursor_ms"
)

// historySlice is how much history one tick searches.
const historySlice = 24 * time.Hour

// Status values.
const (
	StatusRunning    = "running"
	StatusNeedsLogin = "needs_login"
	StatusError      = "error"
)

// Syncer runs ticks against a Client and a Store.
type Syncer struct {
	Client larkcli.Client
	Store  *store.Store
	Clock  Clock
	Opt    Options
	Log    *slog.Logger
}

// Report summarizes one tick.
type Report struct {
	Window     Window
	Complete   bool // the whole window was searched without truncation
	Hits       int
	New        int
	Upserted   int
	Rendered   int
	Backfilled int
	SlowPath   int
	Chats      int
	History    int // messages discovered by the historical search slice
}

func (s *Syncer) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Syncer) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

// EnsureIdentity records the current user's open_id; it fails with an auth
// error when no user token is available.
func (s *Syncer) EnsureIdentity(ctx context.Context) (larkcli.Identity, error) {
	id, err := s.Client.Whoami(ctx)
	if err != nil {
		return id, err
	}
	return id, s.Store.SetState(ctx, KeySelfOpenID, id.UserOpenID)
}

// Tick performs one synchronization pass: fast path, chat refresh, slow path,
// backfill slice and rendering. It records a sync_runs row either way.
func (s *Syncer) Tick(ctx context.Context) (Report, error) {
	start := s.now()
	rep, err := s.tick(ctx, start)
	end := s.now()
	run := store.Run{Kind: "tick", StartedAt: start.UnixMilli(), FinishedAt: end.UnixMilli(), OK: err == nil,
		Fetched: rep.Hits, Upserted: rep.Upserted + rep.Backfilled + rep.SlowPath}
	if err != nil {
		run.Error = err.Error()
	}
	if rerr := s.Store.RecordRun(ctx, run); rerr != nil {
		s.log().Warn("record run", "err", rerr)
	}
	_ = s.Store.SetState(ctx, KeyLastTickAt, strconv.FormatInt(end.UnixMilli(), 10))
	return rep, err
}

func (s *Syncer) tick(ctx context.Context, now time.Time) (Report, error) {
	var rep Report
	if status, _, _ := s.Store.GetState(ctx, KeyStatus); status == StatusNeedsLogin {
		if _, err := s.EnsureIdentity(ctx); err != nil {
			return rep, err
		}
	}

	// 1. Fast path: discover new message ids across all chats.
	wm := s.watermark(ctx)
	rep.Window = FastWindow(wm, now, s.Opt.Overlap)
	hits, coveredEnd, err := s.searchWindow(ctx, rep.Window)
	if err != nil {
		return rep, fmt.Errorf("search: %w", err)
	}
	rep.Hits = len(hits)
	rep.Complete = coveredEnd.Equal(rep.Window.End)
	rep.New, err = s.fetchUnknown(ctx, hits, now)
	if err != nil {
		return rep, err
	}
	rep.Upserted = rep.New
	if err := s.Store.SetState(ctx, KeyWatermark, strconv.FormatInt(coveredEnd.UnixMilli(), 10)); err != nil {
		return rep, err
	}

	// 1b. Historical discovery: one day-slice of cross-chat search per tick,
	// from now-BackfillDays up to the live window. Far cheaper than listing
	// every chat, so recent history fills in within minutes.
	n, err := s.historySlice(ctx, rep.Window.Start, now)
	if err != nil {
		return rep, fmt.Errorf("history: %w", err)
	}
	rep.History = n

	// 2. Full chat listing.
	if Due(s.stateTime(ctx, KeyChatsRefreshed), s.Opt.ChatsRefreshEvery, now) {
		n, err := s.refreshChats(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("chats: %w", err)
		}
		rep.Chats = n
	}

	// 3. Slow path: reconcile the most active chats.
	if Due(s.stateTime(ctx, KeySlowPathAt), s.Opt.SlowPathEvery, now) {
		n, err := s.slowPath(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("slow path: %w", err)
		}
		rep.SlowPath = n
	}

	// 4. Backfill a few chats per tick so live data keeps flowing.
	n, err = s.backfillSlice(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("backfill: %w", err)
	}
	rep.Backfilled = n

	// 5. Render human-readable text for new or edited messages.
	n, err = s.renderPending(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("render: %w", err)
	}
	rep.Rendered = n
	return rep, nil
}

// searchWindow searches w, bisecting on truncation. coveredEnd is the end of
// the contiguous prefix of w that was fully searched.
func (s *Syncer) searchWindow(ctx context.Context, w Window) ([]larkcli.SearchHit, time.Time, error) {
	hits, truncated, err := s.Client.SearchMessageIDs(ctx, w.Start, w.End)
	if err != nil {
		return nil, time.Time{}, err
	}
	if !truncated {
		return hits, w.End, nil
	}
	a, b, ok := Halves(w)
	if !ok {
		s.log().Warn("search window truncated at minimum width; some messages may only arrive via slow path", "start", w.Start, "end", w.End)
		return hits, w.End, nil
	}
	h1, end1, err := s.searchWindow(ctx, a)
	if err != nil {
		return nil, time.Time{}, err
	}
	if !end1.Equal(a.End) {
		return h1, end1, nil
	}
	h2, end2, err := s.searchWindow(ctx, b)
	if err != nil {
		return nil, time.Time{}, err
	}
	return append(h1, h2...), end2, nil
}

// historySlice searches [cursor, cursor+24h] ∩ [.., liveStart] and fetches
// unknown messages; it returns 0 once history is caught up.
func (s *Syncer) historySlice(ctx context.Context, liveStart, now time.Time) (int, error) {
	cur := s.stateTime(ctx, KeyHistoryCursor)
	if cur.IsZero() {
		cur = now.AddDate(0, 0, -s.Opt.BackfillDays)
	}
	if !cur.Before(liveStart) {
		return 0, nil
	}
	w := Window{Start: cur, End: cur.Add(historySlice)}
	if w.End.After(liveStart) {
		w.End = liveStart
	}
	hits, coveredEnd, err := s.searchWindow(ctx, w)
	if err != nil {
		return 0, err
	}
	n, err := s.fetchUnknown(ctx, hits, now)
	if err != nil {
		return n, err
	}
	return n, s.Store.SetState(ctx, KeyHistoryCursor, strconv.FormatInt(coveredEnd.UnixMilli(), 10))
}

// IngestIDs fetches the given messages regardless of the watermark and
// renders them; used after sending so the sender sees its message at once.
func (s *Syncer) IngestIDs(ctx context.Context, ids []string) error {
	now := s.now()
	for _, batch := range Chunk(UniqueStrings(ids), 50) {
		msgs, err := s.Client.MGetRaw(ctx, batch)
		if err != nil {
			return err
		}
		if _, err := s.upsertRaw(ctx, msgs, now); err != nil {
			return err
		}
		rendered, err := s.Client.MGetRendered(ctx, batch, false)
		if err != nil {
			return err
		}
		for _, r := range rendered {
			if err := s.Store.UpdateRendered(ctx, r.MessageID, r.Content, rawString(r.Mentions), rawString(r.Reactions), now.UnixMilli()); err != nil {
				return err
			}
		}
	}
	return nil
}

// fetchUnknown pulls and stores the hits not yet in the store.
func (s *Syncer) fetchUnknown(ctx context.Context, hits []larkcli.SearchHit, now time.Time) (int, error) {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.MessageID)
	}
	unknown, err := s.Store.UnknownMessageIDs(ctx, UniqueStrings(ids))
	if err != nil {
		return 0, err
	}
	total := 0
	for _, batch := range Chunk(unknown, 50) {
		msgs, err := s.Client.MGetRaw(ctx, batch)
		if err != nil {
			return total, fmt.Errorf("mget: %w", err)
		}
		n, err := s.upsertRaw(ctx, msgs, now)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (s *Syncer) upsertRaw(ctx context.Context, msgs []larkcli.RawMessage, now time.Time) (int, error) {
	rows := make([]store.Message, 0, len(msgs))
	chats := map[string]struct{}{}
	for _, m := range msgs {
		rows = append(rows, ToRow(m))
		chats[m.ChatID] = struct{}{}
	}
	for id := range chats {
		if err := s.Store.EnsureChat(ctx, id, now.UnixMilli()); err != nil {
			return 0, err
		}
	}
	return s.Store.UpsertMessages(ctx, rows, now.UnixMilli())
}

// ToRow maps a raw API message onto its store row. Bot senders are keyed by
// their open id so they join with p2p_target_id and contacts.
func ToRow(m larkcli.RawMessage) store.Message {
	senderID := m.Sender.ID
	if m.Sender.OpenBotID != "" {
		senderID = m.Sender.OpenBotID
	}
	return store.Message{
		MessageID: m.MessageID, ChatID: m.ChatID, MsgType: m.MsgType,
		SenderID: senderID, SenderType: m.Sender.SenderType, SenderName: m.Sender.SenderName,
		ContentRaw: m.Body.Content, CreateMs: int64(m.CreateTime), UpdateMs: int64(m.UpdateTime),
		MessagePosition: int64(m.MessagePosition), Updated: m.Updated, Deleted: m.Deleted,
		ThreadID: m.ThreadID, ReplyTo: m.ParentID, RawJSON: string(m.Raw),
	}
}

func (s *Syncer) refreshChats(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Client.ListChats(ctx, false)
	if err != nil {
		return 0, err
	}
	rows := make([]store.Chat, 0, len(chats))
	for _, c := range chats {
		rows = append(rows, store.Chat{ChatID: c.ChatID, Name: c.Name, Description: c.Description, ChatMode: c.ChatMode,
			ChatStatus: c.ChatStatus, OwnerID: c.OwnerID, External: c.External, P2PTargetID: c.P2PTargetID,
			P2PTargetType: c.P2PTargetType, AvatarURL: c.Avatar, RawJSON: string(c.Raw)})
	}
	ms := now.UnixMilli()
	if err := s.Store.UpsertChats(ctx, rows, ms); err != nil {
		return 0, err
	}
	if _, err := s.Store.MarkChatsLeft(ctx, ms); err != nil {
		return 0, err
	}
	return len(rows), s.Store.SetState(ctx, KeyChatsRefreshed, strconv.FormatInt(ms, 10))
}

func (s *Syncer) slowPath(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Client.ListChats(ctx, true)
	if err != nil {
		return 0, err
	}
	if len(chats) > s.Opt.ActiveTopK {
		chats = chats[:s.Opt.ActiveTopK]
	}
	total := 0
	for _, c := range chats {
		local, err := s.Store.GetChat(ctx, c.ChatID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && local.BackfillDoneAt == 0) {
			continue // backfill will cover it
		}
		if err != nil {
			return total, err
		}
		if local.SyncError != "" {
			continue
		}
		since := time.UnixMilli(local.CursorMs).Add(-s.Opt.Overlap)
		n, err := s.pullChat(ctx, c.ChatID, since, time.Time{}, now)
		if err != nil {
			if s.recordChatError(ctx, c.ChatID, err, now) {
				continue
			}
			return total, err
		}
		total += n
	}
	return total, s.Store.SetState(ctx, KeySlowPathAt, strconv.FormatInt(now.UnixMilli(), 10))
}

func (s *Syncer) backfillSlice(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Store.ChatsNeedingBackfill(ctx, s.Opt.BackfillPerTick)
	if err != nil {
		return 0, err
	}
	since := now.AddDate(0, 0, -s.Opt.BackfillDays)
	total := 0
	for _, c := range chats {
		n, err := s.pullChat(ctx, c.ChatID, since, time.Time{}, now)
		if err != nil {
			if s.recordChatError(ctx, c.ChatID, err, now) {
				continue
			}
			return total, err
		}
		total += n
		if err := s.Store.SetChatBackfillDone(ctx, c.ChatID, now.UnixMilli()); err != nil {
			return total, err
		}
	}
	return total, nil
}

// recordChatError persists a permanent API rejection for one chat (for
// example "restricted mode" chats refuse listing) so the loop moves on. It
// returns false for errors that must abort the tick (auth, network, rate limit).
func (s *Syncer) recordChatError(ctx context.Context, chatID string, err error, now time.Time) bool {
	var le *larkcli.Error
	if !errors.As(err, &le) || !le.IsPermanent() {
		return false
	}
	s.log().Warn("chat listing rejected; skipping chat", "chat_id", chatID, "code", le.Code, "msg", le.Message)
	if serr := s.Store.SetChatSyncError(ctx, chatID, fmt.Sprintf("%d: %s", le.Code, le.Message), now.UnixMilli()); serr != nil {
		s.log().Warn("record chat error", "err", serr)
	}
	return true
}

// pullChat lists a chat container in [since, until] plus the threads rooted in
// that range, upserts everything and advances the chat cursor.
func (s *Syncer) pullChat(ctx context.Context, chatID string, since, until, now time.Time) (int, error) {
	msgs, err := s.Client.ListMessagesRaw(ctx, "chat", chatID, since, until)
	if err != nil {
		return 0, err
	}
	var threads []string
	var maxMs int64
	for _, m := range msgs {
		if m.ThreadID != "" {
			threads = append(threads, m.ThreadID)
		}
		maxMs = max(maxMs, int64(m.CreateTime))
	}
	for _, tid := range UniqueStrings(threads) {
		replies, err := s.Client.ListMessagesRaw(ctx, "thread", tid, since, until)
		if err != nil {
			return 0, err
		}
		msgs = append(msgs, replies...)
	}
	n, err := s.upsertRaw(ctx, msgs, now)
	if err != nil {
		return n, err
	}
	if maxMs > 0 {
		if err := s.Store.SetChatCursor(ctx, chatID, maxMs); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *Syncer) renderPending(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.Store.UnrenderedMessageIDs(ctx, s.Opt.RenderPerTick*50)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, batch := range Chunk(ids, 50) {
		rendered, err := s.Client.MGetRendered(ctx, batch, false)
		if err != nil {
			return total, err
		}
		got := map[string]bool{}
		for _, r := range rendered {
			got[r.MessageID] = true
			if err := s.Store.UpdateRendered(ctx, r.MessageID, r.Content, rawString(r.Mentions), rawString(r.Reactions), now.UnixMilli()); err != nil {
				return total, err
			}
			total++
		}
		// Messages the renderer cannot return (no longer visible) are stamped
		// so they are not retried every tick.
		for _, id := range batch {
			if !got[id] {
				if err := s.Store.UpdateRendered(ctx, id, "", "", "", now.UnixMilli()); err != nil {
					return total, err
				}
			}
		}
	}
	return total, nil
}

func rawString(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	return string(r)
}

func (s *Syncer) watermark(ctx context.Context) time.Time {
	return s.stateTime(ctx, KeyWatermark)
}

func (s *Syncer) stateTime(ctx context.Context, key string) time.Time {
	v, ok, err := s.Store.GetState(ctx, key)
	if err != nil || !ok {
		return time.Time{}
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// Run ticks until ctx is cancelled, pacing by PollInterval and backing off on
// failures according to lark-cli's error classification.
func (s *Syncer) Run(ctx context.Context) error {
	if _, err := s.EnsureIdentity(ctx); err != nil {
		s.SetStatus(ctx, err)
	}
	failures := 0
	for {
		rep, err := s.Tick(ctx)
		delay := s.Opt.PollInterval
		if err != nil {
			failures++
			delay = s.delayFor(err, failures)
			s.log().Warn("tick failed", "err", err, "retry_in", delay)
		} else {
			failures = 0
			s.log().Debug("tick", "hits", rep.Hits, "new", rep.New, "rendered", rep.Rendered, "backfilled", rep.Backfilled)
		}
		s.SetStatus(ctx, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (s *Syncer) delayFor(err error, failures int) time.Duration {
	var le *larkcli.Error
	if errors.As(err, &le) {
		switch {
		case le.IsAuth():
			return Backoff(failures, 30*time.Second, 10*time.Minute)
		case le.IsRateLimit():
			return max(le.RetryAfter, 30*time.Second)
		case le.IsNetwork():
			return Backoff(failures, 30*time.Second, 10*time.Minute)
		}
	}
	return Backoff(failures, s.Opt.PollInterval, 5*time.Minute)
}

// SetStatus records the outcome of the last tick in sync_state.
func (s *Syncer) SetStatus(ctx context.Context, err error) {
	status, msg := StatusRunning, ""
	if err != nil {
		status, msg = StatusError, err.Error()
		var le *larkcli.Error
		if errors.As(err, &le) && le.IsAuth() {
			status = StatusNeedsLogin
		}
	}
	_ = s.Store.SetState(ctx, KeyStatus, status)
	_ = s.Store.SetState(ctx, KeyLastError, msg)
}
