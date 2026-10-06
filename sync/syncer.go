package sync

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	stdsync "sync"
	"sync/atomic"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/markread"
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
	DownloadPerTick   int // batches of 50 due resources
	// ForwardsPerTick bounds one tick's merged-forward expansions. The
	// endpoint takes one bundle per call, so this is a count of calls.
	ForwardsPerTick   int
	ReadStatusPerTick int // batches of 50; the fallback behind probeReadStatus
	// DocLinksPerTick bounds one tick's document-title reads, in batches of
	// larkcli.MaxDocTokensPerBatch.
	DocLinksPerTick int
	// ImageTextPerTick bounds one tick's picture readings. The recognizer
	// takes one picture per call, so this is a count of calls.
	ImageTextPerTick int
	RepairEvery      time.Duration
	RepairPerTick    int
	MembersPerTick   int
	AvatarsPerTick   int
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
		PollInterval:          time.Duration(cfg.PollIntervalMS) * time.Millisecond,
		Overlap:               cfg.Overlap,
		ChatsRefreshEvery:     cfg.ChatsRefreshEvery,
		SlowPathEvery:         cfg.SlowPathEvery,
		BackfillDays:          cfg.BackfillDays,
		ActiveTopK:            cfg.ActiveTopK,
		BackfillPerTick:       10,
		RenderPerTick:         4,
		DownloadPerTick:       1,
		ForwardsPerTick:       3,
		ReadStatusPerTick:     1,
		DocLinksPerTick:       1,
		ImageTextPerTick:      4,
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
	KeyTasksAt       = "tasks_at"
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
	// opts is the loop's tuning, held behind a pointer because the sweep
	// goroutine reads it every tick while :config and the daemon's own
	// config reload replace it from another one. Opt/SetOptions are the
	// only way in.
	opts atomic.Pointer[Options]
	Log  *slog.Logger
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
	// settle, when set, settles a chat's server-side read watermark
	// at a position the silence rules chose, so the Feishu clients' dots
	// follow them too; nil disables the settle step. It is the lever
	// markread.Clear, the same one a read settles through, so the two writes
	// reach Feishu the same way.
	//
	// Behind an atomic for the same reason opts is: silence_sync and
	// mark_read.browser retune it mid-run.
	settle atomic.Pointer[markread.Clear]
	// Recover, when set, is deferred at the top of the goroutine Run starts for
	// discovery, so a panic there is reported the way one in Run's own
	// goroutine is. It must call recover itself and re-panic.
	Recover func()
	// BeforeTick, when set, runs on the sweep's own goroutine just before
	// every tick, which is where the daemon rereads its configuration and
	// calls SetOptions: a swap landing between two ticks rather than inside
	// one is what keeps a tick reading one generation of the options.
	BeforeTick func()

	// attended is whether somebody is looking at what discovery finds; see
	// SetAttended.
	attended atomic.Bool
	// signalOnce makes wake and attend; see signals.
	signalOnce stdsync.Once
	// wake ends the sweep's pause when discovery has landed something; attend
	// ends discovery's pause when somebody comes back to look.
	wake, attend chan struct{}
}

// zeroOptions is what a Syncer built without SetOptions runs under, so the
// accessor never answers nil. Every field's zero disables the step it paces.
var zeroOptions Options

// Opt is the tuning this tick runs under. The pointer is handed back rather
// than a copy because a tick reads a dozen fields off it; SetOptions replaces
// the set whole rather than writing through it, so a reader holding one sees
// a coherent set however long it holds it. A Syncer without its own set
// answers with the one shared zeroOptions: never write through that pointer
// unless this Syncer installed a set of its own.
func (s *Syncer) Opt() *Options {
	if o := s.opts.Load(); o != nil {
		return o
	}
	return &zeroOptions
}

// SetOptions replaces the tuning. The next field a tick reads is the new
// one's: every option is read where it is used rather than captured when the
// loop starts, so a reload reaches a sweep already running.
//
// A pause already underway is not cut short — a shortened poll_interval_ms
// takes effect one pause late, which is the whole of what the swap costs.
func (s *Syncer) SetOptions(o Options) { s.opts.Store(&o) }

// SettleSilenced returns the silenced-unread settle lever, nil when the
// step is off.
func (s *Syncer) SettleSilenced() markread.Clear {
	if c := s.settle.Load(); c != nil {
		return *c
	}
	return nil
}

// SetSettleSilenced replaces the lever; nil turns the step off, which is what
// silence_sync going false means.
func (s *Syncer) SetSettleSilenced(c markread.Clear) { s.settle.Store(&c) }

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
	Forwards   int // merged-forward bundles expanded
	ReadChecks int // read-status answers recorded
	Reactions  int // p2p chats whose newest message was asked about
	Todos      int // todo messages re-rendered with the completion their task now holds
	Repaired   int // messages re-listed by the repair pass
	Threads    int // replies re-listed from the threads the reader has a stake in
	Members    int // chat members recorded
	Muted      int // chats whose do-not-disturb setting was answered
	Avatars    int // avatar files stored
	Stickers   int // sticker pictures copied out of the Lark client
	DocLinks   int // document links named or settled as out of reach
	ImageText  int // pictures whose writing was read
	Contacts   int // contacts whose identity fields were resolved
	Settled    int // chats whose silenced unread was settled server-side
}

// changed reports whether the tick moved anything. The daemon ticks every few
// seconds, so this is what keeps the log readable: an idle tick is debug
// detail, a tick that landed something is a record.
func (r Report) changed() bool {
	return r.New > 0 || r.Rendered > 0 || r.Backfilled > 0 || r.SlowPath > 0 ||
		r.Downloaded > 0 || r.History > 0 || r.Repaired > 0 || r.Probed > 0 || r.Forwards > 0 ||
		r.Threads > 0
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

// Tick performs one full synchronization pass: discovery, then the sweep —
// search, chat refresh, slow path, backfill, rendering and the rest. It is
// what a one-shot sync runs; Run keeps discovery on a loop of its own. It
// records a sync_runs row either way.
func (s *Syncer) Tick(ctx context.Context) (Report, error) { return s.pass(ctx, true) }

// pass is one tick, with discovery at its head when discover is set.
func (s *Syncer) pass(ctx context.Context, discover bool) (Report, error) {
	start := s.now()
	rep, err := s.tick(ctx, start, discover)
	end := s.now()
	run := store.Run{Kind: "tick", StartedAt: start.UnixMilli(), FinishedAt: end.UnixMilli(), OK: err == nil,
		Fetched: rep.Hits, Upserted: rep.Upserted + rep.Backfilled + rep.SlowPath}
	if err != nil {
		run.Error = err.Error()
	}
	if rerr := s.Store.RecordRun(ctx, run); rerr != nil {
		s.log().Warn("record run", "err", rerr)
	}
	if serr := s.setStateTime(ctx, KeyLastTickAt, end); serr != nil {
		s.log().Warn("stamp last tick", "err", serr)
	}
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

func (s *Syncer) tick(ctx context.Context, now time.Time, discover bool) (Report, error) {
	var rep Report
	loggedOut, err := s.loggedOut(ctx)
	if err != nil {
		return rep, err
	}
	if loggedOut {
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

	// 1. Discovery, when this pass carries it: a one-shot tick has nobody else
	// to find new messages, and runs it first so the search below has less
	// left to find. Run keeps it on a loop of its own instead, so that no
	// sweep below is ever in front of a message somebody is waiting to read.
	if discover {
		if rep.Moved, rep.Probed, err = s.discover(ctx, now); err != nil {
			return rep, err
		}
		rep.Upserted += rep.Probed
	}

	// 2. Safety net: a cross-chat search over everything since the last one.
	// Discovery reaches a message a good ten seconds earlier — measured
	// against this very search — but it only sees the chat list's first page
	// and only says that a chat has moved, so this still has to run for what
	// the ordering cannot show: edits, recalls, and a chat the page missed.
	// It does not have to run often, which is the point: the search index is
	// what was slow, not the search.
	watermark, err := s.stateTime(ctx, KeyWatermark)
	if err != nil {
		return rep, err
	}
	rep.Window = FastWindow(watermark, now, s.Opt().Overlap)
	searchedAt, err := s.stateTime(ctx, KeySearchAt)
	if err != nil {
		return rep, err
	}
	if Due(searchedAt, searchEvery, now) {
		rep.Searched = true
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

	// 3. Render, ask about and fetch what discovery and the search landed,
	// ahead of the sweeps below. A body lands unrendered, so a reader watching
	// the chat sees the message named by its type until a rendering replaces
	// it; doing it here rather than after the sweeps is the difference between
	// a flicker and half a minute of placeholder. A message that arrived is
	// stored unread, and one already read in the client stays wrongly so until
	// Feishu's answer comes, which is why the answer is fetched before the
	// downloads too. Discovery lands messages between sweeps and wakes this
	// one when it does, so the step runs on every sweep rather than on the
	// sweep's own finds: the renders and downloads cost nothing when nothing
	// is pending, and the read probe is the one every sweep asks anyway.
	if rep.Rendered, err = s.renderPending(ctx, "", s.Opt().RenderPerTick*50, now); err != nil {
		return rep, fmt.Errorf("render: %w", err)
	}
	s.changed(rep.Rendered)
	if rep.ReadChecks, err = s.probeReadStatus(ctx, now); err != nil {
		return rep, fmt.Errorf("read probe: %w", err)
	}
	s.changed(rep.ReadChecks)
	if rep.Downloaded, err = s.downloadPending(ctx, now); err != nil {
		return rep, fmt.Errorf("resources: %w", err)
	}
	s.changed(rep.Downloaded)

	// 4. Settle the chats a silence flag flip queued: push the server-side
	// read watermark past their silenced unread, so the Feishu clients' dots
	// follow the local rules. After the read probe on purpose — the plan
	// reads the remote flags it just refreshed.
	if rep.Settled, err = s.settleSilenced(ctx); err != nil {
		return rep, fmt.Errorf("silence settle: %w", err)
	}

	// 5. Historical discovery: one day-slice of cross-chat search per tick,
	// from now-BackfillDays up to the live window. Far cheaper than listing
	// every chat, so recent history fills in within minutes.
	n, err := s.historySlice(ctx, rep.Window.Start, now)
	if err != nil {
		return rep, fmt.Errorf("history: %w", err)
	}
	rep.History = n

	// 6. Full chat listing, and the per-user settings it does not carry.
	chatsRefreshed, err := s.stateTime(ctx, KeyChatsRefreshed)
	if err != nil {
		return rep, err
	}
	if Due(chatsRefreshed, s.Opt().ChatsRefreshEvery, now) {
		n, err := s.refreshChats(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("chats: %w", err)
		}
		rep.Chats = n
		if rep.Muted, err = s.muteSlice(ctx, now); err != nil {
			return rep, fmt.Errorf("mute: %w", err)
		}
	}

	// 7. Slow path: reconcile the most active chats, then the threads the
	// reader has a stake in, which no chat's listing carries.
	slowPathAt, err := s.stateTime(ctx, KeySlowPathAt)
	if err != nil {
		return rep, err
	}
	if Due(slowPathAt, s.Opt().SlowPathEvery, now) {
		n, err := s.slowPath(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("slow path: %w", err)
		}
		rep.SlowPath = n
		if rep.Threads, err = s.pullStakedThreads(ctx, now); err != nil {
			return rep, fmt.Errorf("staked threads: %w", err)
		}
	}

	// 8. Backfill a few chats per tick so live data keeps flowing.
	n, err = s.backfillSlice(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("backfill: %w", err)
	}
	rep.Backfilled = n

	// 9. Render and fetch whatever the sweeps above added.
	n, err = s.renderPending(ctx, "", s.Opt().RenderPerTick*50, now)
	if err != nil {
		return rep, fmt.Errorf("render: %w", err)
	}
	rep.Rendered += n

	// 10. Download attachments that are pending or due for retry.
	n, err = s.downloadPending(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("resources: %w", err)
	}
	rep.Downloaded += n
	s.changed(rep.Rendered + rep.Downloaded)

	// 11. Expand a few merged-forward bundles into their children.
	n, err = s.expandForwards(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("forwards: %w", err)
	}
	rep.Forwards = n
	s.changed(n)

	// 12. Copy sticker pictures out of the Lark client's own storage.
	n, err = s.copyStickers(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("stickers: %w", err)
	}
	rep.Stickers = n

	// 13. Name the documents linked to from messages.
	n, err = s.resolveDocLinks(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("doc links: %w", err)
	}
	rep.DocLinks = n
	s.changed(n)

	// 14. Walk the read-status ladder over recent messages from others; the
	// probe ran in step 3.
	n, err = s.pollReadStatus(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("read status: %w", err)
	}
	rep.ReadChecks += n
	s.changed(n)

	// 15. Keep the chat list's reactions current for the liveliest p2p chats.
	reactionsAt, err := s.stateTime(ctx, KeyReactionsAt)
	if err != nil {
		return rep, err
	}
	if Due(reactionsAt, reactionsEvery, now) {
		n, err = s.reactionsSlice(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("reactions: %w", err)
		}
		rep.Reactions = n
	}

	// 16. Keep the todo checkboxes current: the completion lives in the task
	// list and nowhere in a message body, so this listing is the only thing
	// that can move a box.
	tasksAt, err := s.stateTime(ctx, KeyTasksAt)
	if err != nil {
		return rep, err
	}
	if Due(tasksAt, tasksEvery, now) {
		n, err = s.refreshTodos(ctx, now)
		if err != nil {
			return rep, fmt.Errorf("todos: %w", err)
		}
		rep.Todos = n
		s.changed(n)
	}

	// 17. Repair recent history, refresh members, fetch avatars, name reacting
	// apps: a few each.
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
	if err := s.resolveApps(ctx, now); err != nil {
		return rep, fmt.Errorf("apps: %w", err)
	}

	// 18. Read the writing in pictures already on disk. Last because nothing
	// on screen waits for it: it feeds the assistant and the reaction
	// suggester, which are asked for by hand.
	if rep.ImageText, err = s.readImageText(ctx, now); err != nil {
		return rep, fmt.Errorf("image text: %w", err)
	}
	return rep, nil
}

// silenceSettlePerTick bounds one tick's settles; each is one gateway POST.
const silenceSettlePerTick = 10

// settleSilenced drains the silence settle queue. A chat whose plan answers
// no action is dropped without a write; a settle the gateway refuses keeps
// its row and counts an attempt, so the next tick retries it until the cap
// retires it. larkweb is not under lark-cli's lanes, so the pace between two
// posts is its own.
func (s *Syncer) settleSilenced(ctx context.Context) (int, error) {
	// Taken once: the step either runs this tick or it does not, and a
	// silence_sync turned off mid-drain would otherwise strand the rows it
	// had already dequeued.
	settle := s.SettleSilenced()
	if settle == nil {
		return 0, nil
	}
	pending, err := s.Store.PendingSilenceSettle(ctx, silenceSettlePerTick)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	settled := 0
	for _, p := range pending {
		if !p.Push {
			if err := s.Store.SilenceSettleDone(ctx, p.ChatID, 0); err != nil {
				return settled, err
			}
			continue
		}
		if settled > 0 {
			time.Sleep(larkweb.Pace)
		}
		if err := settle(ctx, store.ChatUnread{ChatID: p.ChatID, Position: p.Position}); err != nil {
			s.log().Warn("silence settle failed", "chat_id", p.ChatID, "err", err)
			if serr := s.Store.SilenceSettleFailed(ctx, p.ChatID); serr != nil {
				s.log().Warn("count silence settle failure", "chat_id", p.ChatID, "err", serr)
			}
			continue
		}
		if _, err := s.Store.AcceptRemoteRead(ctx, p.ChatID, p.Position); err != nil {
			s.log().Warn("accept remote read", "chat_id", p.ChatID, "err", err)
			if serr := s.Store.SilenceSettleFailed(ctx, p.ChatID); serr != nil {
				s.log().Warn("count silence settle failure", "chat_id", p.ChatID, "err", serr)
			}
			continue
		}
		settled++
		if err := s.Store.SilenceSettleDone(ctx, p.ChatID, p.Position); err != nil {
			return settled, err
		}
	}
	return settled, nil
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
	cur, err := s.stateTime(ctx, KeyHistoryCursor)
	if err != nil {
		return 0, err
	}
	if cur.IsZero() {
		cur = now.AddDate(0, 0, -s.Opt().BackfillDays)
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
		if err := s.ingest(ctx, msgs, now); err != nil {
			return err
		}
	}
	return nil
}

// IngestSent stores a message this process just sent from the answer the send
// came back with, which saves the fetch IngestIDs makes. The answer carries no
// position, so a root is stored at 0 until the next listing writes the real
// one over it. A reply inside a thread is fetched after all: whether its
// position is negative depends on the kind of chat, and the sign is what keeps
// it out of the chat's own flow. A card is fetched too, since only a fetch
// asks for its raw body.
func (s *Syncer) IngestSent(ctx context.Context, sent larkcli.SentMessage) error {
	m := sent.Message
	if m == nil || m.MsgType == "interactive" || (m.ThreadID != "" && m.ParentID != "") {
		return s.IngestIDs(ctx, []string{sent.MessageID})
	}
	// The answer names no sender either, and a row without a name draws its
	// sender as an id and opens a block of its own.
	if m.Sender.SenderName == "" {
		people, err := s.Store.ContactsByIDs(ctx, []string{senderIDOf(*m)})
		if err != nil {
			return err
		}
		m.Sender.SenderName = people[senderIDOf(*m)].Name
	}
	return s.ingest(ctx, []larkcli.RawMessage{*m}, s.now())
}

func (s *Syncer) ingest(ctx context.Context, msgs []larkcli.RawMessage, now time.Time) error {
	if _, _, err := s.upsertRaw(ctx, msgs, now); err != nil {
		return err
	}
	ids := make([]string, 0, len(msgs))
	remote := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.MessageID)
		if !store.LocallyRendered(m.MsgType) {
			remote = append(remote, m.MessageID)
		}
	}
	// The types larkim renders itself are done here rather than asked about:
	// the sender is waiting, and a call for a body already on disk would only
	// slow that down.
	if _, err := s.renderLocal(ctx, ids, len(msgs), now); err != nil {
		return err
	}
	rendered, err := s.Client.MGetRendered(ctx, remote)
	if err != nil {
		return err
	}
	for _, r := range rendered {
		if err := s.storeRendered(ctx, r, now); err != nil {
			return err
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
	var bundles []string
	for _, m := range msgs {
		rows = append(rows, ToRow(m))
		ids = append(ids, m.MessageID)
		chats[m.ChatID] = struct{}{}
		if m.Deleted {
			continue
		}
		resources = append(resources, ExtractResources(m.MessageID, m.MsgType, m.Body.Content)...)
		// Every path that stores a message funnels through here and msg_type
		// is known before any rendering, so this catches a bundle whose
		// rendering was judged empty too.
		if m.MsgType == "merge_forward" {
			bundles = append(bundles, m.MessageID)
		}
	}
	unknown, err := s.Store.UnknownMessageIDs(ctx, UniqueStrings(ids))
	if err != nil {
		return 0, 0, err
	}
	fresh = len(unknown)
	beforeBadge := make(map[string]int64, len(chats))
	for id := range chats {
		beforeBadge[id], err = s.Store.ChatBadgeCount(ctx, id)
		if err != nil {
			return 0, fresh, err
		}
	}
	for id := range chats {
		if err := s.Store.EnsureChat(ctx, id, now.UnixMilli()); err != nil {
			return 0, 0, err
		}
	}
	// Without the user's own id there is no telling their messages apart, so
	// nothing is stored unread until identity is known.
	self, _, err := s.Store.GetState(ctx, KeySelfOpenID)
	if err != nil {
		return 0, fresh, err
	}
	n, err = s.Store.UpsertMessagesArriving(ctx, rows, now.UnixMilli(),
		store.Arrival{Self: self, SinceMs: now.Add(-arrivalWindow).UnixMilli()})
	if err != nil {
		return n, fresh, err
	}
	// Every path that stores a message funnels through here, so this is where
	// "the sweep ran but nothing landed" gets its answer.
	s.log().DebugContext(ctx, "upsert", "rows", len(rows), "changed", n, "fresh", fresh, "chats", len(chats))
	if err := s.Store.UpsertContacts(ctx, senderContacts(msgs), now.UnixMilli()); err != nil {
		return n, fresh, err
	}
	// After the upsert: the queue row points at the message row.
	if err := s.Store.AddForwardRoots(ctx, bundles); err != nil {
		return n, fresh, err
	}
	if s.Opt().DataDir != "" {
		if err := s.Store.AddPendingResources(ctx, resources); err != nil {
			return n, fresh, err
		}
	}
	s.muteOnMessageArrival(ctx, msgs, unknown, chats, beforeBadge, now)
	return n, fresh, nil
}

// senderIDOf keys a sender: a bot by its open id, so it joins with
// p2p_target_id and contacts.
func senderIDOf(m larkcli.RawMessage) string { return cmp.Or(m.Sender.OpenBotID, m.Sender.ID) }

// ToRow maps a raw API message onto its store row.
func ToRow(m larkcli.RawMessage) store.Message {
	return store.Message{
		MessageID: m.MessageID, ChatID: m.ChatID, MsgType: m.MsgType,
		SenderID: senderIDOf(m), SenderType: m.Sender.SenderType, SenderName: m.Sender.SenderName,
		ContentRaw: m.Body.Content, CreateMs: int64(m.CreateTime), UpdateMs: int64(m.UpdateTime),
		MessagePosition: int64(m.MessagePosition), Updated: m.Updated, Deleted: m.Deleted,
		ThreadID: m.ThreadID, ReplyTo: m.ParentID, MentionsJSON: mentionsJSON(m.Mentions),
		RawJSON: string(m.Raw),
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
	chats, err := s.Store.ChatsNeedingMute(ctx, active, now.Add(-s.Opt().ChatsRefreshEvery).UnixMilli(), larkcli.MaxChatIDsPerMuteCall)
	if err != nil || len(chats) == 0 {
		return 0, err
	}
	ids := make([]string, 0, len(chats))
	for _, c := range chats {
		ids = append(ids, c.ChatID)
	}
	return s.refreshMuteForChats(ctx, ids, now)
}

func (s *Syncer) refreshChats(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Client.ListChats(ctx)
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

// maxActivePage is the largest page the chat list answers.
const maxActivePage = 100

// slowPath reconciles the most active chats from their cursors. It reads the
// ordering itself rather than taking discovery's: discovery keeps its page
// short for speed, and active_top_k may ask for more than that.
func (s *Syncer) slowPath(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Client.ActiveChats(ctx, min(s.Opt().ActiveTopK, maxActivePage))
	if err != nil {
		return 0, err
	}
	total, _, err := s.pullFromCursor(ctx, chatIDs(chats), "slow path", now)
	if err != nil {
		return total, err
	}
	return total, s.setStateTime(ctx, KeySlowPathAt, now)
}

// pullFromCursor re-lists each chat from its own cursor, less one overlap,
// and puts what a chat brings on screen as soon as that chat has it: the
// reader is told, with every body larkim renders itself already rendered,
// without waiting on a slower chat. why names the caller in the log lines a
// skipped chat produces. A chat the gateway refuses is recorded and stepped
// over; everything else stops the run.
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
		since[id] = time.UnixMilli(local.CursorMs).Add(-s.Opt().Overlap)
	}
	return s.pullChats(ctx, ids, now, func(ctx context.Context, id string) (int, int, error) {
		n, fresh, err := s.pullChat(ctx, id, since[id], time.Time{}, now)
		if err != nil || fresh == 0 {
			return n, fresh, err
		}
		// Local only: nothing it can fail with is a refusal pullChats would
		// pin on the chat.
		_, err = s.renderLocal(ctx, nil, s.Opt().RenderPerTick*50, now)
		s.changed(fresh)
		return n, fresh, err
	})
}

func (s *Syncer) backfillSlice(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Store.ChatsNeedingBackfill(ctx, s.Opt().BackfillPerTick)
	if err != nil {
		return 0, err
	}
	since := now.AddDate(0, 0, -s.Opt().BackfillDays)
	ids := make([]string, len(chats))
	for i, c := range chats {
		ids[i] = c.ChatID
	}
	total, _, err := s.pullChats(ctx, ids, now, func(ctx context.Context, id string) (int, int, error) {
		n, _, err := s.pullChat(ctx, id, since, time.Time{}, now)
		if err != nil {
			return 0, 0, err
		}
		return n, 0, s.Store.SetChatBackfillDone(ctx, id, since.UnixMilli(), now.UnixMilli())
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

// firstFailure is the answer a fanOut gave: the first error that is not the
// cancellation a sibling's failure caused.
func firstFailure(errs []error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
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

	for _, r := range res {
		total += r.n
		fresh += r.fresh
	}
	return total, fresh, firstFailure(errs)
}

// listThreads fetches every thread rooted in one chat's window at once, then
// returns their replies in tids order so what reaches upsertRaw does not
// depend on which goroutine finished first.
func (s *Syncer) listThreads(ctx context.Context, tids []string, since, until time.Time) ([]larkcli.RawMessage, error) {
	replies, errs := fanOut(ctx, tids, func(ctx context.Context, tid string) ([]larkcli.RawMessage, error) {
		return s.Client.ListMessagesRaw(ctx, "thread", tid, since, until)
	})

	if err := firstFailure(errs); err != nil {
		return nil, err
	}
	return slices.Concat(replies...), nil
}

// stakedThreadsTopK bounds one pass's thread listings. A thread container
// ignores start_time, so each thread costs its whole reply list however
// little of it is new, and the freshest few are where the next reply lands.
const stakedThreadsTopK = 20

// pullStakedThreads asks after the threads the reader has a stake in by name.
// pullChat follows only the threads whose root it saw in the window it just
// listed, and a chat's listing carries roots without replies, so a reply to a
// root older than that window — a thread's whole reason to exist — reaches
// the store no other way.
func (s *Syncer) pullStakedThreads(ctx context.Context, now time.Time) (int, error) {
	self, _, err := s.Store.GetState(ctx, KeySelfOpenID)
	if err != nil || self == "" {
		return 0, err
	}
	threads, err := s.Store.StakedThreads(ctx, self, stakedThreadsTopK)
	if err != nil || len(threads) == 0 {
		return 0, err
	}
	tids := make([]string, len(threads))
	for i, t := range threads {
		tids[i] = t.ThreadID
	}
	// No window: the container answers with the whole thread whatever it is
	// given, so naming one would only claim a filter that does not happen.
	replies, err := s.listThreads(ctx, tids, time.Time{}, time.Time{})
	if err != nil {
		// A thread Feishu refuses outright costs this pass rather than the
		// tick. The chat it lives in is refused the same way, and recording
		// that on the chat is what drops the thread from the staked set.
		if le, ok := errors.AsType[*larkcli.Error](err); ok && le.IsPermanent() {
			s.log().Warn("thread listing rejected; skipping the pass", "code", le.Code, "error", le.Message)
			return 0, nil
		}
		return 0, err
	}
	n, _, err := s.upsertRaw(ctx, replies, now)
	s.log().DebugContext(ctx, "pull staked threads", "threads", len(tids), "replies", len(replies), "upserted", n)
	return n, err
}

// recordChatError persists a permanent API rejection for one chat (for
// example "restricted mode" chats refuse listing) so the loop moves on. It
// returns false for errors that must abort the tick (auth, network, rate limit).
func (s *Syncer) recordChatError(ctx context.Context, chatID string, err error, now time.Time) bool {
	le, ok := errors.AsType[*larkcli.Error](err)
	if !ok || !le.IsPermanent() {
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

// renderPending renders every message still without a rendering, whatever its
// attachments are doing. An empty chatID takes them from every chat; the chat
// somebody is reading names itself, so its own arrivals are not stuck behind a
// backlog elsewhere.
func (s *Syncer) renderPending(ctx context.Context, chatID string, limit int, now time.Time) (int, error) {
	local, err := s.renderLocal(ctx, nil, s.Opt().RenderPerTick*50, now)
	if err != nil {
		return local, err
	}
	remote, err := s.renderRemote(ctx, chatID, limit, now)
	return local + remote, err
}

// renderRemote renders the types only Feishu can, asked for in batches.
func (s *Syncer) renderRemote(ctx context.Context, chatID string, limit int, now time.Time) (int, error) {
	total := 0
	ids, err := s.Store.UnrenderedMessageIDs(ctx, chatID, limit)
	if err != nil {
		return total, err
	}
	for batch := range slices.Chunk(ids, 50) {
		rendered, err := s.Client.MGetRendered(ctx, batch)
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
				if err := s.Store.UpdateRendered(ctx, id, "", "", now.UnixMilli()); err != nil {
					return total, err
				}
			}
		}
	}
	return total, nil
}

// renderLocal renders the local queue's head, or, with ids, exactly those
// messages: everything larkim can read off bodies already on disk. They cost
// no call and stay correct whatever lark-cli does with them.
func (s *Syncer) renderLocal(ctx context.Context, ids []string, limit int, now time.Time) (int, error) {
	pending, err := s.Store.UnrenderedLocalMessages(ctx, ids, limit)
	if err != nil {
		return 0, err
	}
	for i, m := range pending {
		text, err := s.localBody(ctx, m)
		if err != nil {
			return i, err
		}
		if err := s.Store.UpdateRendered(ctx, m.MessageID, text, "", now.UnixMilli()); err != nil {
			return i, err
		}
	}
	return len(pending), nil
}

// localBody is a pending message's rendering. Every type but a forwarded
// bundle follows from the body on the row; a bundle's text is in its children,
// which the queue guarantees are here by the time it is handed over.
func (s *Syncer) localBody(ctx context.Context, m store.PendingLocalMessage) (string, error) {
	if m.MsgType != "merge_forward" {
		return localText(m), nil
	}
	kids, err := s.Store.ForwardTree(ctx, m.MessageID)
	if err != nil {
		return "", err
	}
	return ForwardText(m.MessageID, kids, time.Local), nil
}

// storeRendered saves a rendered message's text and reactions. The mentions
// that came back with it are dropped: they are the ones the body already
// carried, which UpsertMessages stored before anything was rendered. No
// pictures are registered here — a bundle was the only rendering that named
// any, and larkim renders those itself now.
func (s *Syncer) storeRendered(ctx context.Context, r larkcli.RenderedMessage, now time.Time) error {
	text := renderedText(r)
	if err := s.Store.UpdateRendered(ctx, r.MessageID, text, rawString(r.Reactions), now.UnixMilli()); err != nil {
		return err
	}
	return s.Store.AddPendingDocLinks(ctx, store.FindDocRefs(text))
}

func rawString(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	return string(r)
}

// stateTime and setStateTime keep sync_state timestamps as Unix milliseconds.
// An unset key is the zero time, which the planners read as "never run"; a
// read that failed is reported, because a caller that took it for a first run
// would plan the smallest possible window and then write its end over the
// cursor it could not see.
func (s *Syncer) stateTime(ctx context.Context, key string) (time.Time, error) {
	v, ok, err := s.Store.GetState(ctx, key)
	if err != nil {
		return time.Time{}, fmt.Errorf("read %s: %w", key, err)
	}
	if !ok {
		return time.Time{}, nil
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ms == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(ms), nil
}

func (s *Syncer) setStateTime(ctx context.Context, key string, t time.Time) error {
	return s.Store.SetState(ctx, key, strconv.FormatInt(t.UnixMilli(), 10))
}

// Run syncs until ctx is cancelled: discovery on a loop of its own, and the
// sweep paced by PollInterval, each backing off on failures according to
// lark-cli's error classification. Discovery landing something ends the
// sweep's pause.
func (s *Syncer) Run(ctx context.Context) error {
	if _, err := s.EnsureIdentity(ctx); err != nil {
		s.SetStatus(ctx, err)
	}
	s.signals()
	var wg stdsync.WaitGroup
	defer wg.Wait()
	// Run can also leave by a panic in the sweep, which is no ctx of the
	// caller's ending: without this the wait above would hold it forever and
	// the panic would never reach the recover that re-panics it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg.Go(func() {
		if s.Recover != nil {
			defer s.Recover()
		}
		s.runDiscovery(ctx)
	})
	failures := 0
	for {
		if s.BeforeTick != nil {
			s.BeforeTick()
		}
		rep, err := s.pass(ctx, false)
		delay := s.Opt().PollInterval
		wake := s.wake
		if err != nil {
			failures++
			delay = s.delayFor(err, failures)
			// A find is no reason to retry an API that just failed.
			wake = nil
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
				"searched", rep.Searched, "complete", rep.Complete)
		}
		s.SetStatus(ctx, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-time.After(delay):
		}
	}
}

// errClass names why the loop is backing off, which the delay alone does not
// say.
func errClass(err error) string {
	le, ok := errors.AsType[*larkcli.Error](err)
	if !ok {
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
	if le, ok := errors.AsType[*larkcli.Error](err); ok {
		switch {
		case le.IsAuth(), le.IsNetwork():
			return Backoff(failures, 30*time.Second, 10*time.Minute)
		case le.IsRateLimit():
			return max(le.RetryAfter, 30*time.Second)
		}
	}
	// The poll interval can be set below a second, which is a pace for ticks
	// that succeed, not for retrying an API that just failed.
	return Backoff(failures, max(s.Opt().PollInterval, time.Second), 5*time.Minute)
}

// SetStatus records the outcome of the last tick in sync_state.
func (s *Syncer) SetStatus(ctx context.Context, err error) {
	status, msg := StatusRunning, ""
	if err != nil {
		status, msg = StatusError, err.Error()
		if le, ok := errors.AsType[*larkcli.Error](err); ok && le.IsAuth() {
			status = StatusNeedsLogin
		}
	}
	_ = s.Store.SetState(ctx, KeyStatus, status)
	_ = s.Store.SetState(ctx, KeyLastError, msg)
}
