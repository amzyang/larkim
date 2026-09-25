package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	stdsync "sync"
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
	DownloadPerTick   int // batches of 50 messages with due resources
	ReadStatusPerTick int // batches of 50; the fallback behind probeReadStatus
	RepairEvery       time.Duration
	RepairPerTick     int
	MembersPerTick    int
	AvatarsPerTick    int
	// ContactDetailsPerTick bounds one tick's identity backfill; SearchUsers
	// splits it into as many `+search-user` calls as it needs.
	ContactDetailsPerTick int
	// DataDir is where lark-cli downloads land (resources/ below it); empty
	// disables downloads.
	DataDir string
	// ClientDir is where the Lark client keeps its per-account storage, the
	// only local source of sticker pictures; empty leaves stickers unfilled.
	ClientDir string
	// MaxBytes skips attachments larger than this (0 = unlimited).
	MaxBytes int64
}

// OptionsFrom maps the user config onto loop options.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		PollInterval:          cfg.PollInterval,
		Overlap:               cfg.Overlap,
		ChatsRefreshEvery:     cfg.ChatsRefreshEvery,
		SlowPathEvery:         cfg.SlowPathEvery,
		BackfillDays:          cfg.BackfillDays,
		ActiveTopK:            cfg.ActiveTopK,
		BackfillPerTick:       10,
		RenderPerTick:         4,
		DownloadPerTick:       1,
		ReadStatusPerTick:     1,
		RepairEvery:           cfg.RepairEvery,
		RepairPerTick:         3,
		MembersPerTick:        2,
		AvatarsPerTick:        5,
		ContactDetailsPerTick: larkcli.MaxUserIDsPerSearch,
		DataDir:               cfg.DataDir,
		ClientDir:             DefaultClientDir(),
		MaxBytes:              cfg.Resources.MaxBytes,
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
	KeySearchAt       = "search_at"
	// KeyHistoryCursor is the start of the next day-slice of historical search;
	// history is complete once it reaches the live window.
	KeyHistoryCursor = "history_cursor_ms"
	KeyReactionsAt   = "reactions_at"
	// KeyActiveOrder is the chat list's active-time ordering as the previous
	// tick saw it; comparing against it names the chats that have since seen
	// a message.
	KeyActiveOrder = "active_order"
)

// historySlice is how much history one tick searches.
const historySlice = 24 * time.Hour

// searchEvery paces the cross-chat search. The activity probe is what carries
// discovery now, so this is a reconciliation interval rather than a latency
// one: short enough that an edit or a recall shows up while the reader still
// has the chat on screen, long enough to leave the one background line free.
const searchEvery = 30 * time.Second

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
	// OnError, when set, observes every failed tick (for crash reporting).
	OnError func(error)
	// OnChange, when set, fires whenever a tick has written something a
	// reader displays, and once more at the end of every tick, failed ones
	// included, so an in-process consumer can compare revisions at once
	// instead of at its next poll. A tick that wrote nothing still fires at
	// its end: the comparison is what decides, not this.
	OnChange func()
	// Fetch downloads avatars; nil disables avatar files.
	Fetch Fetcher
}

// Report summarizes one tick.
type Report struct {
	Window Window
	// Searched says the cross-chat safety net ran this tick. Most ticks it
	// does not, and Complete claims nothing about a tick that did not search.
	Searched   bool
	Complete   bool // the whole window was searched without truncation
	Hits       int
	New        int
	Upserted   int
	Rendered   int
	Backfilled int
	SlowPath   int
	Chats      int
	// Moved counts the chats the activity probe named. The head is named
	// whether or not it moved, so a non-zero Moved says nothing about whether
	// the tick landed anything; Probed is what does.
	Moved      int
	Probed     int // messages new to the store that the activity probe reached first
	History    int // messages discovered by the historical search slice
	Downloaded int // attachments stored
	ReadChecks int // read-status answers recorded
	Reactions  int // p2p chats whose newest message was asked about
	Repaired   int // messages re-listed by the repair pass
	Members    int // chat members recorded
	Muted      int // chats whose do-not-disturb setting was answered
	Avatars    int // avatar files stored
	Stickers   int // sticker pictures copied out of the Lark client
	Contacts   int // contacts whose identity fields were resolved
}

// changed reports whether the tick moved anything. The daemon ticks every few
// seconds, so this is what keeps the log readable: an idle tick is debug
// detail, a tick that landed something is a record.
func (r Report) changed() bool {
	return r.New > 0 || r.Rendered > 0 || r.Backfilled > 0 || r.SlowPath > 0 ||
		r.Downloaded > 0 || r.History > 0 || r.Repaired > 0 || r.Probed > 0
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
	_ = s.setStateTime(ctx, KeyLastTickAt, end)
	if s.OnChange != nil {
		s.OnChange()
	}
	return rep, err
}

// changed prompts an in-process consumer to compare revisions now rather than
// at its next poll. A tick writes a message in stages — the body, then the
// rendering that makes it readable, then its pictures — and a reader watching
// the chat wants each as it lands, not all of them a sweep later.
func (s *Syncer) changed(n int) {
	if n > 0 && s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Syncer) tick(ctx context.Context, now time.Time) (Report, error) {
	var rep Report
	if status, _, _ := s.Store.GetState(ctx, KeyStatus); status == StatusNeedsLogin {
		if _, err := s.EnsureIdentity(ctx); err != nil {
			return rep, err
		}
	}

	// 0. The silence rules live in the config; the flags they produce live
	// in the database. Rebuilding is a full pass over messages, so the
	// fingerprint gates it and an unchanged config costs one keyed read.
	if silenced, err := s.Store.ReapplySilence(ctx); err != nil {
		return rep, fmt.Errorf("silence: %w", err)
	} else if silenced > 0 {
		s.log().Info("silence rules changed", "messages", silenced)
	}

	// 1. Activity probe: the chat list sorted by active time puts a chat that
	// has just seen a message at position 1, and it reads chat state rather
	// than the search index the fast path below waits on, so it names a chat
	// seconds before that search can find the message. Listing the chats it
	// names has no such lag either, which is why this runs first: what it
	// reaches is already stored by the time the search asks.
	//
	// The search below still runs on every tick and still covers these chats.
	// It stays until the two have been measured against each other.
	active, moved, err := s.activeProbe(ctx)
	if err != nil {
		return rep, fmt.Errorf("active probe: %w", err)
	}
	rep.Moved = len(moved)
	if _, rep.Probed, err = s.pullFromCursor(ctx, moved, "active probe", now); err != nil {
		return rep, fmt.Errorf("active probe pull: %w", err)
	}
	// Recording the ordering is what consumes the delta: once written, the
	// chats it named are no longer named. So it waits for the pull, and a
	// failed pull leaves them named next tick rather than dropping them on
	// the safety net half a minute out.
	if err := s.setActiveOrder(ctx, active); err != nil {
		return rep, fmt.Errorf("active order: %w", err)
	}
	rep.Upserted += rep.Probed
	s.changed(rep.Probed)

	// 2. Safety net: a cross-chat search over everything since the last one.
	// The probe above reaches a message a good ten seconds earlier — measured
	// against this very search — but it only sees the chat list's first page
	// and only says that a chat has moved, so this still has to run for what
	// the ordering cannot show: edits, recalls, and a chat the page missed.
	// It does not have to run often, which is the point: the search index is
	// what was slow, not the search.
	rep.Window = FastWindow(s.stateTime(ctx, KeyWatermark), now, s.Opt.Overlap)
	rep.Complete = true // no search, no window left uncovered by this tick
	if Due(s.stateTime(ctx, KeySearchAt), searchEvery, now) {
		hits, coveredEnd, err := s.searchWindow(ctx, rep.Window)
		if err != nil {
			return rep, fmt.Errorf("search: %w", err)
		}
		rep.Hits = len(hits)
		rep.Complete = coveredEnd.Equal(rep.Window.End)
		if rep.New, err = s.fetchUnknown(ctx, hits, now); err != nil {
			return rep, err
		}
		rep.Upserted += rep.New
		if err := s.setStateTime(ctx, KeyWatermark, coveredEnd); err != nil {
			return rep, err
		}
		if err := s.setStateTime(ctx, KeySearchAt, now); err != nil {
			return rep, err
		}
		s.changed(rep.New)
	}

	// 3. Render and fetch what discovery just found, ahead of the sweeps
	// below. A body lands unrendered, so a reader watching the chat sees the
	// message named by its type until a rendering replaces it; doing it here
	// rather than after the sweeps is the difference between a flicker and
	// half a minute of placeholder. A tick that found nothing skips it, which
	// is most of them.
	if rep.New+rep.Probed > 0 {
		if rep.Rendered, err = s.renderPending(ctx, "", s.Opt.RenderPerTick*50, now); err != nil {
			return rep, fmt.Errorf("render: %w", err)
		}
		if rep.Downloaded, err = s.downloadPending(ctx, now); err != nil {
			return rep, fmt.Errorf("resources: %w", err)
		}
		s.changed(rep.Rendered + rep.Downloaded)
	}

	// 4. Historical discovery: one day-slice of cross-chat search per tick,
	// from now-BackfillDays up to the live window. Far cheaper than listing
	// every chat, so recent history fills in within minutes.
	n, err := s.historySlice(ctx, rep.Window.Start, now)
	if err != nil {
		return rep, fmt.Errorf("history: %w", err)
	}
	rep.History = n

	// 5. Full chat listing, and the per-user settings it does not carry.
	if Due(s.stateTime(ctx, KeyChatsRefreshed), s.Opt.ChatsRefreshEvery, now) {
		n, err := s.refreshChats(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("chats: %w", err)
		}
		rep.Chats = n
		if rep.Muted, err = s.muteSlice(ctx, now); err != nil {
			return rep, fmt.Errorf("mute: %w", err)
		}
	}

	// 6. Slow path: reconcile the most active chats.
	if Due(s.stateTime(ctx, KeySlowPathAt), s.Opt.SlowPathEvery, now) {
		n, err := s.slowPath(ctx, active, now)
		if err != nil {
			return rep, fmt.Errorf("slow path: %w", err)
		}
		rep.SlowPath = n
	}

	// 7. Backfill a few chats per tick so live data keeps flowing.
	n, err = s.backfillSlice(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("backfill: %w", err)
	}
	rep.Backfilled = n

	// 8. Render and fetch whatever the sweeps above added.
	n, err = s.renderPending(ctx, "", s.Opt.RenderPerTick*50, now)
	if err != nil {
		return rep, fmt.Errorf("render: %w", err)
	}
	rep.Rendered += n

	// 9. Download attachments that are pending or due for retry.
	n, err = s.downloadPending(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("resources: %w", err)
	}
	rep.Downloaded += n
	s.changed(rep.Rendered + rep.Downloaded)

	// 10. Copy sticker pictures out of the Lark client's own storage.
	n, err = s.copyStickers(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("stickers: %w", err)
	}
	rep.Stickers = n

	// 11. Poll whether the user has read recent messages from others.
	n, err = s.pollReadStatus(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("read status: %w", err)
	}
	rep.ReadChecks = n

	// 12. Keep the chat list's reactions current for the liveliest p2p chats.
	if Due(s.stateTime(ctx, KeyReactionsAt), reactionsEvery, now) {
		n, err = s.reactionsSlice(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("reactions: %w", err)
		}
		rep.Reactions = n
	}

	// 13. Repair recent history, refresh members, fetch avatars: a few each.
	if rep.Repaired, err = s.repairSlice(ctx, now); err != nil {
		return rep, fmt.Errorf("repair: %w", err)
	}
	if rep.Members, err = s.membersSlice(ctx, now); err != nil {
		return rep, fmt.Errorf("members: %w", err)
	}
	if rep.Avatars, err = s.avatarsSlice(ctx, now); err != nil {
		return rep, fmt.Errorf("avatars: %w", err)
	}
	if rep.Contacts, err = s.contactDetailsSlice(ctx, now); err != nil {
		return rep, fmt.Errorf("contact details: %w", err)
	}
	return rep, nil
}

// searchWindow searches w, bisecting on truncation. coveredEnd is the end of
// the contiguous prefix of w that was fully searched.
func (s *Syncer) searchWindow(ctx context.Context, w Window) ([]larkcli.SearchHit, time.Time, error) {
	hits, truncated, err := s.Client.SearchMessageIDs(ctx, w.Start, w.End)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.log().DebugContext(ctx, "search window", "start", w.Start, "end", w.End, "hits", len(hits), "truncated", truncated)
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
		// The live window advances every tick, so a cursor left on its edge is
		// behind again by the next one and history re-searches a stretch the
		// live path already covered. Keeping it abreast of now ends that for
		// good. An outage needs no catch-up here either: the watermark stalls
		// with the loop, so the next live window spans the whole gap itself.
		return 0, s.setStateTime(ctx, KeyHistoryCursor, now)
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
	return n, s.setStateTime(ctx, KeyHistoryCursor, coveredEnd)
}

// IngestIDs fetches the given messages regardless of the watermark and
// renders them; used after sending so the sender sees its message at once.
func (s *Syncer) IngestIDs(ctx context.Context, ids []string) error {
	now := s.now()
	for batch := range slices.Chunk(UniqueStrings(ids), 50) {
		msgs, err := s.Client.MGetRaw(ctx, batch)
		if err != nil {
			return err
		}
		if _, _, err := s.upsertRaw(ctx, msgs, now); err != nil {
			return err
		}
		rendered, err := s.Client.MGetRendered(ctx, batch, false)
		if err != nil {
			return err
		}
		for _, r := range rendered {
			if err := s.storeRendered(ctx, r, now); err != nil {
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
	for batch := range slices.Chunk(unknown, 50) {
		msgs, err := s.Client.MGetRaw(ctx, batch)
		if err != nil {
			return total, fmt.Errorf("mget: %w", err)
		}
		n, _, err := s.upsertRaw(ctx, msgs, now)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// upsertRaw stores msgs and reports how many rows it wrote and, of those, how
// many the store had never seen. Every listing re-reads an overlap, so the two
// differ on most calls; a caller that runs every tick has to tell them apart
// or it will report an idle chat as news forever.
func (s *Syncer) upsertRaw(ctx context.Context, msgs []larkcli.RawMessage, now time.Time) (n, fresh int, err error) {
	rows := make([]store.Message, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	chats := map[string]struct{}{}
	var resources []store.ResourceRef
	for _, m := range msgs {
		rows = append(rows, ToRow(m))
		ids = append(ids, m.MessageID)
		chats[m.ChatID] = struct{}{}
		if !m.Deleted {
			resources = append(resources, ExtractResources(m.MessageID, m.MsgType, m.Body.Content)...)
		}
	}
	unknown, err := s.Store.UnknownMessageIDs(ctx, UniqueStrings(ids))
	if err != nil {
		return 0, 0, err
	}
	fresh = len(unknown)
	for id := range chats {
		if err := s.Store.EnsureChat(ctx, id, now.UnixMilli()); err != nil {
			return 0, 0, err
		}
	}
	n, err = s.Store.UpsertMessages(ctx, rows, now.UnixMilli())
	if err != nil {
		return n, fresh, err
	}
	// Every path that stores a message funnels through here, so this is where
	// "the sweep ran but nothing landed" gets its answer.
	s.log().DebugContext(ctx, "upsert", "rows", len(rows), "changed", n, "fresh", fresh, "chats", len(chats))
	if err := s.Store.UpsertContacts(ctx, senderContacts(msgs), now.UnixMilli()); err != nil {
		return n, fresh, err
	}
	if s.Opt.DataDir != "" {
		if err := s.Store.AddPendingResources(ctx, resources); err != nil {
			return n, fresh, err
		}
	}
	return n, fresh, nil
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

// muteActiveDays bounds the mute lookup to chats recent enough for the list
// to mark. Older ones keep whatever was last known.
const muteActiveDays = 30

// muteSlice refreshes do-not-disturb for the chats that have gone longest
// without an answer. One call covers the API's whole batch, so the slice is a
// single round trip; chats beyond it come round on the next refresh.
func (s *Syncer) muteSlice(ctx context.Context, now time.Time) (int, error) {
	active := now.AddDate(0, 0, -muteActiveDays).UnixMilli()
	chats, err := s.Store.ChatsNeedingMute(ctx, active, now.Add(-s.Opt.ChatsRefreshEvery).UnixMilli(), larkcli.MaxChatIDsPerMuteCall)
	if err != nil || len(chats) == 0 {
		return 0, err
	}
	ids := make([]string, 0, len(chats))
	for _, c := range chats {
		ids = append(ids, c.ChatID)
	}
	muted, unknown, err := s.Client.MuteStatus(ctx, ids)
	if err != nil {
		return 0, err
	}
	if err := s.Store.SetMuteStatus(ctx, muted, unknown, now.UnixMilli()); err != nil {
		return 0, err
	}
	return len(muted), nil
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
	return len(rows), s.setStateTime(ctx, KeyChatsRefreshed, now)
}

// activeProbe reads the active-time ordering of the chat list's first page and
// names the chats that have moved up in it since the last tick. Feishu puts a
// chat that just received a message at position 1, so the first page is
// complete for this purpose however many chats there are. It answers with the
// ordering as well, which is the listing every other step of the tick reads
// the active chats from, and which setActiveOrder stores once the caller has
// acted on the delta.
func (s *Syncer) activeProbe(ctx context.Context) (order, moved []string, err error) {
	chats, err := s.Client.ListChats(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	order = make([]string, 0, len(chats))
	for _, c := range chats {
		order = append(order, c.ChatID)
	}
	var prev []string
	if raw, ok, err := s.Store.GetState(ctx, KeyActiveOrder); err != nil {
		return nil, nil, err
	} else if ok && raw != "" {
		// A hand-edited or half-written value would otherwise fail every tick;
		// treating it as a first run costs one cycle of blindness.
		if err := json.Unmarshal([]byte(raw), &prev); err != nil {
			s.log().WarnContext(ctx, "active order unreadable", "err", err)
			prev = nil
		}
	}
	return order, ActiveDelta(prev, order), nil
}

// setActiveOrder records the ordering the next tick compares against.
func (s *Syncer) setActiveOrder(ctx context.Context, order []string) error {
	enc, err := json.Marshal(order)
	if err != nil {
		return err
	}
	return s.Store.SetState(ctx, KeyActiveOrder, string(enc))
}

// slowPath reconciles the chats at the head of active, the ordering the
// probe already listed this tick.
func (s *Syncer) slowPath(ctx context.Context, active []string, now time.Time) (int, error) {
	total, _, err := s.pullFromCursor(ctx, active[:min(len(active), s.Opt.ActiveTopK)], "slow path", now)
	if err != nil {
		return total, err
	}
	return total, s.setStateTime(ctx, KeySlowPathAt, now)
}

// pullFromCursor re-lists each chat from its own cursor, less one overlap.
// why names the caller in the log lines a skipped chat produces. A chat the
// gateway refuses is recorded and stepped over; everything else stops the run.
func (s *Syncer) pullFromCursor(ctx context.Context, chatIDs []string, why string, now time.Time) (total, fresh int, err error) {
	ids := make([]string, 0, len(chatIDs))
	since := make(map[string]time.Time, len(chatIDs))
	for _, id := range chatIDs {
		local, err := s.Store.GetChat(ctx, id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && local.BackfillDoneAt == 0) {
			s.log().DebugContext(ctx, why+" skip", "chat_id", id, "reason", "not backfilled yet")
			continue // backfill will cover it
		}
		if err != nil {
			return 0, 0, err
		}
		if local.SyncError != "" {
			s.log().DebugContext(ctx, why+" skip", "chat_id", id, "reason", "chat rejected earlier", "err", local.SyncError)
			continue
		}
		ids = append(ids, id)
		since[id] = time.UnixMilli(local.CursorMs).Add(-s.Opt.Overlap)
	}
	return s.pullChats(ctx, ids, now, func(ctx context.Context, id string) (int, int, error) {
		return s.pullChat(ctx, id, since[id], time.Time{}, now)
	})
}

func (s *Syncer) backfillSlice(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Store.ChatsNeedingBackfill(ctx, s.Opt.BackfillPerTick)
	if err != nil {
		return 0, err
	}
	since := now.AddDate(0, 0, -s.Opt.BackfillDays)
	ids := make([]string, len(chats))
	for i, c := range chats {
		ids[i] = c.ChatID
	}
	total, _, err := s.pullChats(ctx, ids, now, func(ctx context.Context, id string) (int, int, error) {
		n, _, err := s.pullChat(ctx, id, since, time.Time{}, now)
		if err != nil {
			return 0, 0, err
		}
		return n, 0, s.Store.SetChatBackfillDone(ctx, id, now.UnixMilli())
	})
	return total, err
}

// fanOut runs do against every item at once and answers per item, in order.
// Nothing here bounds how many subprocesses that becomes: the lane inside
// larkcli does, and goroutines past its width simply wait there, so the
// concurrency has one place to be tuned.
//
// The first failure cancels the calls still in flight — answering a rate limit
// with the rest of the sweep is what the limit is asking us not to do — so a
// caller folds context.Canceled away and reports the answer the gateway gave
// rather than the echo of one it did.
func fanOut[T, R any](ctx context.Context, items []T, do func(context.Context, T) (R, error)) ([]R, []error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := make([]R, len(items))
	errs := make([]error, len(items))
	var wg stdsync.WaitGroup
	for i, it := range items {
		wg.Go(func() {
			out[i], errs[i] = do(ctx, it)
			if errs[i] != nil {
				cancel()
			}
		})
	}
	wg.Wait()
	return out, errs
}

// pullChats runs pull against every chat at once. A chat the gateway refuses
// permanently is recorded and stepped over, as it was when this ran one chat
// at a time; any other refusal ends the sweep.
func (s *Syncer) pullChats(ctx context.Context, chatIDs []string, now time.Time, pull func(context.Context, string) (int, int, error)) (total, fresh int, err error) {
	type pulled struct{ n, fresh int }
	res, errs := fanOut(ctx, chatIDs, func(ctx context.Context, id string) (pulled, error) {
		n, f, err := pull(ctx, id)
		if err != nil && s.recordChatError(ctx, id, err, now) {
			return pulled{}, nil
		}
		return pulled{n: n, fresh: f}, err
	})

	for i, r := range res {
		total += r.n
		fresh += r.fresh
		// A sibling's failure cancelled this one before it had an answer of
		// its own, so it has nothing to report that the sibling will not.
		if errs[i] != nil && err == nil && !errors.Is(errs[i], context.Canceled) {
			err = errs[i]
		}
	}
	return total, fresh, err
}

// listThreads fetches every thread rooted in one chat's window at once, then
// returns their replies in tids order so what reaches upsertRaw does not
// depend on which goroutine finished first.
func (s *Syncer) listThreads(ctx context.Context, tids []string, since, until time.Time) ([]larkcli.RawMessage, error) {
	replies, errs := fanOut(ctx, tids, func(ctx context.Context, tid string) ([]larkcli.RawMessage, error) {
		return s.Client.ListMessagesRaw(ctx, "thread", tid, since, until)
	})

	var out []larkcli.RawMessage
	for i, err := range errs {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				continue // cancelled by a sibling, which carries the real answer
			}
			return nil, err
		}
		out = append(out, replies[i]...)
	}
	return out, nil
}

// recordChatError persists a permanent API rejection for one chat (for
// example "restricted mode" chats refuse listing) so the loop moves on. It
// returns false for errors that must abort the tick (auth, network, rate limit).
func (s *Syncer) recordChatError(ctx context.Context, chatID string, err error, now time.Time) bool {
	var le *larkcli.Error
	if !errors.As(err, &le) || !le.IsPermanent() {
		return false
	}
	s.log().Warn("chat listing rejected; skipping chat", "chat_id", chatID, "code", le.Code, "error", le.Message)
	if serr := s.Store.SetChatSyncError(ctx, chatID, fmt.Sprintf("%d: %s", le.Code, le.Message), now.UnixMilli()); serr != nil {
		s.log().Warn("record chat error", "err", serr)
	}
	return true
}

// pullChat lists a chat container in [since, until] plus the threads rooted in
// that range, upserts everything and advances the chat cursor.
func (s *Syncer) pullChat(ctx context.Context, chatID string, since, until, now time.Time) (n, fresh int, err error) {
	msgs, err := s.Client.ListMessagesRaw(ctx, "chat", chatID, since, until)
	if err != nil {
		return 0, 0, err
	}
	var threads []string
	var maxMs int64
	for _, m := range msgs {
		if m.ThreadID != "" {
			threads = append(threads, m.ThreadID)
		}
		maxMs = max(maxMs, int64(m.CreateTime))
	}
	tids := UniqueStrings(threads)
	replies, err := s.listThreads(ctx, tids, since, until)
	if err != nil {
		return 0, 0, err
	}
	msgs = append(msgs, replies...)
	n, fresh, err = s.upsertRaw(ctx, msgs, now)
	if err != nil {
		return n, fresh, err
	}
	s.log().DebugContext(ctx, "pull chat", "chat_id", chatID, "since", since, "until", until,
		"messages", len(msgs), "threads", len(tids), "upserted", n, "fresh", fresh)
	if maxMs > 0 {
		if err := s.Store.SetChatCursor(ctx, chatID, maxMs); err != nil {
			return n, fresh, err
		}
	}
	return n, fresh, nil
}

// renderPending renders messages without attachments; those with pending
// downloads are rendered by downloadPending in the same lark-cli call. An
// empty chatID takes them from every chat; the chat somebody is reading names
// itself, so its own arrivals are not stuck behind a backlog elsewhere.
func (s *Syncer) renderPending(ctx context.Context, chatID string, limit int, now time.Time) (int, error) {
	total, err := s.renderLocal(ctx, now)
	if err != nil {
		return total, err
	}
	ids, err := s.Store.UnrenderedMessageIDs(ctx, chatID, limit)
	if err != nil {
		return total, err
	}
	for batch := range slices.Chunk(ids, 50) {
		rendered, err := s.Client.MGetRendered(ctx, batch, false)
		if err != nil {
			return total, err
		}
		got := map[string]bool{}
		for _, r := range rendered {
			got[r.MessageID] = true
			if err := s.storeRendered(ctx, r, now); err != nil {
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

// renderLocal renders the messages larkim can read off the API body already
// on disk: a system message Feishu templates out of values the body carries,
// and a call, whose body names the meeting. They cost no call and stay
// correct whatever lark-cli does with them.
func (s *Syncer) renderLocal(ctx context.Context, now time.Time) (int, error) {
	pending, err := s.Store.UnrenderedLocalMessages(ctx, s.Opt.RenderPerTick*50)
	if err != nil {
		return 0, err
	}
	for i, m := range pending {
		if err := s.Store.UpdateRendered(ctx, m.MessageID, localText(m), "", "", now.UnixMilli()); err != nil {
			return i, err
		}
	}
	return len(pending), nil
}

// storeRendered saves a rendered message's text, mentions and reactions, and
// registers the pictures that exist nowhere but that text.
func (s *Syncer) storeRendered(ctx context.Context, r larkcli.RenderedMessage, now time.Time) error {
	text := renderedText(r)
	if err := s.Store.UpdateRendered(ctx, r.MessageID, text, rawString(r.Mentions), rawString(r.Reactions), now.UnixMilli()); err != nil {
		return err
	}
	return s.Store.AddPendingResources(ctx, ExtractRendered(r.MessageID, r.MsgType, text))
}

func rawString(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	return string(r)
}

// stateTime and setStateTime keep sync_state timestamps as Unix milliseconds.
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

func (s *Syncer) setStateTime(ctx context.Context, key string, t time.Time) error {
	return s.Store.SetState(ctx, key, strconv.FormatInt(t.UnixMilli(), 10))
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
			s.log().Warn("tick failed", "err", err, "class", errClass(err), "failures", failures, "retry_in", delay)
			if s.OnError != nil {
				s.OnError(err)
			}
		} else {
			failures = 0
			level := slog.LevelDebug
			if rep.changed() {
				level = slog.LevelInfo
			}
			s.log().Log(ctx, level, "tick", "hits", rep.Hits, "new", rep.New, "rendered", rep.Rendered,
				"backfilled", rep.Backfilled, "slow_path", rep.SlowPath, "history", rep.History,
				"downloaded", rep.Downloaded, "repaired", rep.Repaired, "chats", rep.Chats,
				"moved", rep.Moved, "probed", rep.Probed, "complete", rep.Complete)
		}
		s.SetStatus(ctx, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// errClass names why the loop is backing off, which the delay alone does not
// say.
func errClass(err error) string {
	var le *larkcli.Error
	if !errors.As(err, &le) {
		return "other"
	}
	switch {
	case le.IsAuth():
		return "auth"
	case le.IsNetwork():
		return "network"
	case le.IsRateLimit():
		return "rate_limit"
	}
	return "api"
}

func (s *Syncer) delayFor(err error, failures int) time.Duration {
	var le *larkcli.Error
	if errors.As(err, &le) {
		switch {
		case le.IsAuth(), le.IsNetwork():
			return Backoff(failures, 30*time.Second, 10*time.Minute)
		case le.IsRateLimit():
			return max(le.RetryAfter, 30*time.Second)
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
