package triage

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/presence"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

const (
	// window is how old an arrival may be and still be judged. It keeps a
	// backfill from reading as news while covering a sweep that fell behind
	// for a few minutes.
	window = 15 * time.Minute
	// bannerFresh is how old a P0 may be and still raise a banner. A start
	// after a while away judges the window's backlog, and a stack of banners
	// for it would bury the one that matters; the unread list already has
	// them.
	bannerFresh = 3 * time.Minute
	// reminderLate is how late a reminder may fire before it is dropped: a
	// meeting a quarter of an hour in is one the banner no longer helps with.
	reminderLate = 15 * time.Minute
	// idleAway is how long without input reads as the reader having walked
	// away, which lets a banner through whatever is in front.
	idleAway = 2 * time.Minute
	// draftTries is how many times a draft is attempted before it is given
	// up on.
	draftTries = 2
	// draftFresh bounds how stale a P0 may be and still be drafted for: one
	// left over from a drafter that was missing for a day is no longer a
	// question anyone is waiting on.
	draftFresh = time.Hour
	// draftContext is how many messages up to the target the drafter reads.
	draftContext = 12
	// judgeContext is how many messages before a gray one the judge reads.
	judgeContext = 8
	// batch bounds one pass's arrivals.
	batch = 50
	// passEvery and draftEvery are the loops' paces. A pass is two indexed
	// queries when nothing arrived, so the judge loop polls rather than
	// threading a signal out of the sweep.
	passEvery  = 2 * time.Second
	draftEvery = 30 * time.Second
	// judgeTimeout and draftTimeout bound the two remote calls.
	judgeTimeout = 15 * time.Second
	draftTimeout = 2 * time.Minute
	// bannerTimeout bounds one banner: alerter closes its own after 60s.
	bannerTimeout = 90 * time.Second
	bodyMax       = 200
)

// selfAliases are names colleagues call the reader by besides their display
// name. Feishu's profile has no field for them, and each is the reader's
// alone, so a message that uses one without an @ is about the reader.
var selfAliases = []string{"大师兄"}

// GrayAsk is a message no rule decided, with what the judge reads around it.
type GrayAsk struct {
	Message store.Message
	Chat    store.Chat
	Context []store.Message
	Self    string
	// Reader is the reader's display name, empty when the contacts table does
	// not have it yet.
	Reader string
	// Aliases are the other names only the reader goes by.
	Aliases []string
}

// Judge decides what the rules leave open.
type Judge interface {
	Judge(ctx context.Context, a GrayAsk) (Verdict, error)
}

// DraftAsk is one P0 message to draft replies for.
type DraftAsk struct {
	ChatName string
	// Reader is the user's display name, empty when the contacts table does
	// not have it yet. The transcript marks their lines (me), but a window
	// they have not spoken in leaves them anonymous, and then a message to
	// whoever was @-ed reads as one to them.
	Reader string
	// Aliases are the other names only the user goes by.
	Aliases    []string
	Transcript string
	Target     string
	Now        time.Time
}

// Draft is what the drafter answered: reply candidates, best first, none
// empty, in Format, and a reminder when the message named a later time.
type Draft struct {
	Texts  []string
	Format string
	Remind *Remind
}

// Remind is a reminder a message asked for.
type Remind struct {
	At    time.Time
	Title string
}

// Drafter writes reply candidates for a P0 message.
type Drafter interface {
	Draft(ctx context.Context, a DraftAsk) (Draft, error)
}

// Banner is one desktop notification.
type Banner struct {
	Title, Body string
	// Icon is an absolute image path; empty draws the default.
	Icon      string
	ChatID    string
	MessageID string
	// Position is the message's place in its chat, for the applink; 0 opens
	// the chat at its tail.
	Position int64
	// JoinLink, when set, offers a Join action that goes straight into the
	// call.
	JoinLink string
}

// What a banner was answered with.
const (
	ActionOpen    = "@CONTENTCLICKED"
	ActionJoin    = "Join"
	ActionDismiss = "@CLOSED"
)

// Notifier shows a banner and blocks until it is answered or times out.
type Notifier interface {
	Show(ctx context.Context, b Banner) (string, error)
}

// Desktop is what triage reads off and hands to the machine it runs on.
type Desktop interface {
	LarkFrontmost(ctx context.Context) bool
	// Idle is how long since the last input; a negative value is unknown.
	Idle(ctx context.Context) time.Duration
	Open(ctx context.Context, url string) error
}

// Presence finds the running TUIs and brings one forward.
type Presence interface {
	Peers(ctx context.Context) ([]presence.State, error)
	Open(ctx context.Context, p presence.State, chat string) error
	Raise(ctx context.Context, p presence.State) error
}

// Triager judges arrivals and acts on the verdicts. It runs in the process
// holding daemon.lock, which makes it the only writer of triage and reminders.
// Judge, Drafter, Notifier and Presence may be nil: no judge leaves the gray
// zone at P1, no drafter writes no candidates, no notifier only records.
type Triager struct {
	Store    *store.Store
	Judge    Judge
	Drafter  Drafter
	Notifier Notifier
	Desktop  Desktop
	Presence Presence
	Clock    sync.Clock
	// Self is the reader's open id, read per pass because a first run learns
	// it only after the sweep's first identity call.
	Self func(context.Context) string
	// DataDir resolves the stored avatar paths a banner shows.
	DataDir string
	Log     *slog.Logger

	rules   atomic.Pointer[Rules]
	banners gosync.WaitGroup
	drafted chan struct{}
}

// SetRules installs the rule set the next pass judges by.
func (t *Triager) SetRules(r Rules) { t.rules.Store(&r) }

// Rules is the rule set the next pass judges by; false before any is set.
func (t *Triager) Rules() (Rules, bool) {
	r := t.rules.Load()
	if r == nil {
		return Rules{}, false
	}
	return *r, true
}

// Wait blocks until every banner shown so far has been answered.
func (t *Triager) Wait() { t.banners.Wait() }

func (t *Triager) log() *slog.Logger {
	if t.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return t.Log
}

// Run judges and drafts until ctx is done. The two loops are apart because a
// draft takes the agent a minute, and a banner must not wait behind one.
func (t *Triager) Run(ctx context.Context) {
	t.drafted = make(chan struct{}, 1)
	var wg gosync.WaitGroup
	loop := func(name string, every time.Duration, wake <-chan struct{}, pass func(context.Context) error) {
		for {
			if err := pass(ctx); err != nil && ctx.Err() == nil {
				t.log().WarnContext(ctx, name, "err", err)
			}
			if !sleep(ctx, every, wake) {
				return
			}
		}
	}
	wg.Go(func() { loop("triage pass", passEvery, nil, t.Pass) })
	wg.Go(func() { loop("triage draft", draftEvery, t.drafted, t.DraftPass) })
	wg.Wait()
	t.banners.Wait()
}

func sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-wake:
	}
	return true
}

// Pass judges what arrived, raises the banners owed and fires the reminders
// due.
func (t *Triager) Pass(ctx context.Context) error {
	self := t.Self(ctx)
	rules := t.rules.Load()
	if self == "" || rules == nil {
		return nil
	}
	now := t.Clock.Now()
	if err := t.judge(ctx, *rules, self, now); err != nil {
		return err
	}
	if err := t.notify(ctx, self, now); err != nil {
		return err
	}
	return t.remind(ctx, now)
}

func (t *Triager) judge(ctx context.Context, rules Rules, self string, now time.Time) error {
	msgs, err := t.Store.Untriaged(ctx, now.Add(-window).UnixMilli(), batch)
	if err != nil {
		return err
	}
	chats := map[string]store.Chat{}
	var gray []store.Message
	p0 := false
	// Rule verdicts land first: they cost nothing, and a judge call queued
	// in front of a p2p message would hold its banner back.
	for _, m := range msgs {
		c, err := t.chat(ctx, chats, m.ChatID)
		if err != nil {
			return err
		}
		v, decided := rules.Classify(m, c, self)
		if !decided {
			gray = append(gray, m)
			continue
		}
		p0 = p0 || v.Level == P0
		if err := t.put(ctx, m, v, now); err != nil {
			return err
		}
	}
	if p0 {
		if err := t.notify(ctx, self, now); err != nil {
			return err
		}
	}
	// Each gray P0 is announced as it is judged: the batch behind it can take
	// the judge minutes, past bannerFresh.
	for _, m := range gray {
		v := t.judgeGray(ctx, m, chats[m.ChatID], self)
		if err := t.put(ctx, m, v, now); err != nil {
			return err
		}
		if v.Level == P0 {
			if err := t.notify(ctx, self, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Triager) chat(ctx context.Context, cache map[string]store.Chat, id string) (store.Chat, error) {
	if c, ok := cache[id]; ok {
		return c, nil
	}
	c, err := t.Store.GetChat(ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return c, err
	}
	// A chat first seen through a message has no row yet; it is judged as a
	// group, which is what every chat the listing has not named turns out to
	// be — a new p2p arrives with its listing.
	c.ChatID = id
	cache[id] = c
	return c, nil
}

func (t *Triager) judgeGray(ctx context.Context, m store.Message, c store.Chat, self string) Verdict {
	// Muted is the reader saying this chat can wait; the client shows no
	// banner for it either.
	if c.Muted {
		return Verdict{Level: P1, Reason: "muted"}
	}
	if t.Judge == nil {
		return Verdict{Level: P1, Reason: "p1"}
	}
	before, err := t.Store.ListMessages(ctx, store.MessageQuery{ChatID: m.ChatID, BeforeID: m.MessageID,
		ExcludeThreadReplies: true, Desc: true, Limit: judgeContext})
	if err != nil {
		t.log().WarnContext(ctx, "triage judge context", "message", m.MessageID, "err", err)
	}
	slices.Reverse(before)
	reader, _ := t.Store.GetContact(ctx, self)
	ctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()
	v, err := t.Judge.Judge(ctx, GrayAsk{Message: m, Chat: c, Context: before, Self: self,
		Reader: reader.Name, Aliases: selfAliases})
	if err != nil {
		t.log().WarnContext(ctx, "triage judge", "message", m.MessageID, "chat", m.ChatID, "err", err)
		return Verdict{Level: P1, Reason: "jev-error"}
	}
	return v
}

func (t *Triager) put(ctx context.Context, m store.Message, v Verdict, now time.Time) error {
	row := store.Triage{MessageID: m.MessageID, ChatID: m.ChatID, Level: string(v.Level),
		Reason: v.Reason, JevP: v.JevP, JudgedMs: now.UnixMilli()}
	if v.Jev != nil {
		b, err := json.Marshal(NewJudgment(*v.Jev), json.Deterministic(true))
		if err != nil {
			return fmt.Errorf("triage: encode judgment: %w", err)
		}
		row.Jev = b
	}
	if err := t.Store.PutTriage(ctx, row); err != nil {
		return err
	}
	if v.Level == P0 && t.drafted != nil {
		select {
		case t.drafted <- struct{}{}:
		default:
		}
	}
	return nil
}

func (t *Triager) notify(ctx context.Context, self string, now time.Time) error {
	owed, err := t.Store.TriageToNotify(ctx)
	if err != nil {
		return err
	}
	// The desktop is asked once per call, and only if a banner gets that far:
	// every ask is a few subprocesses and a dial to each TUI.
	attending := gosync.OnceValue(func() bool { return t.attending(ctx) })
	for _, o := range owed {
		var b Banner
		show := false
		if t.Notifier != nil {
			if b, show, err = t.banner(ctx, o, self, now, attending); err != nil {
				return err
			}
		}
		// Stamped before the banner goes up: a banner lost to a crash is
		// cheaper than one shown twice.
		if err := t.Store.MarkTriageNotified(ctx, o.MessageID, now.UnixMilli()); err != nil {
			return err
		}
		if show {
			t.show(ctx, b)
		}
	}
	return nil
}

// banner builds the banner a P0 verdict owes and says whether to show it.
func (t *Triager) banner(ctx context.Context, o store.Triage, self string, now time.Time, attending func() bool) (Banner, bool, error) {
	m, err := t.Store.GetMessage(ctx, o.MessageID)
	if errors.Is(err, store.ErrNotFound) {
		return Banner{}, false, nil
	}
	if err != nil {
		return Banner{}, false, err
	}
	if m.Deleted || now.Sub(time.UnixMilli(m.CreateMs)) > bannerFresh {
		return Banner{}, false, nil
	}
	replied, err := t.Store.RepliedSince(ctx, m.ChatID, self, m.CreateMs)
	if err != nil {
		return Banner{}, false, err
	}
	if read := m.IsReadRemote != nil && *m.IsReadRemote; read || replied || attending() {
		return Banner{}, false, nil
	}
	c, err := t.Store.GetChat(ctx, m.ChatID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Banner{}, false, err
	}
	b := Banner{ChatID: m.ChatID, MessageID: m.MessageID, Icon: t.icon(c), Position: max(m.MessagePosition, 0)}
	who := cmp.Or(m.SenderName, m.SenderID)
	body := Text(m)
	if v, ok := sync.ParseVideoChat(m.ContentRaw); ok {
		body = cmp.Or(v.Topic, "Video call")
		if v.Live() && v.MeetNumber != "" {
			b.JoinLink = applink.MeetingLink(v.MeetNumber)
		}
	}
	if c.ChatMode == "p2p" {
		b.Title, b.Body = who, body
	} else {
		b.Title, b.Body = cmp.Or(c.Name, m.ChatID), who+": "+body
	}
	b.Body = clip(b.Body)
	return b, true, nil
}

// attending is whether the reader is looking at a conversation already: the
// client in front or a larkim TUI with focus, while they are at the machine.
func (t *Triager) attending(ctx context.Context) bool {
	if t.Desktop.Idle(ctx) >= idleAway {
		return false
	}
	return t.Desktop.LarkFrontmost(ctx) ||
		slices.ContainsFunc(t.peers(ctx), func(p presence.State) bool { return p.Focused })
}

func (t *Triager) peers(ctx context.Context) []presence.State {
	if t.Presence == nil {
		return nil
	}
	ps, err := t.Presence.Peers(ctx)
	if err != nil {
		t.log().WarnContext(ctx, "presence", "err", err)
	}
	return ps
}

func (t *Triager) icon(c store.Chat) string {
	f := c.AvatarFile()
	if f == "" || t.DataDir == "" {
		return ""
	}
	return filepath.Join(t.DataDir, f)
}

func (t *Triager) remind(ctx context.Context, now time.Time) error {
	due, err := t.Store.DueReminders(ctx, now.UnixMilli())
	if err != nil {
		return err
	}
	for _, r := range due {
		if err := t.Store.MarkReminderFired(ctx, r.MessageID, now.UnixMilli()); err != nil {
			return err
		}
		if now.Sub(time.UnixMilli(r.FireMs)) > reminderLate {
			t.log().InfoContext(ctx, "reminder dropped as late", "message", r.MessageID, "fire_ms", r.FireMs)
			continue
		}
		c, err := t.Store.GetChat(ctx, r.ChatID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		// A reminder is the moment itself, so it is not held back for a
		// reader who is looking at larkim: that is when it is most useful.
		t.show(ctx, Banner{Title: "⏰ " + cmp.Or(c.Name, r.ChatID), Body: clip(r.Title), Icon: t.icon(c),
			ChatID: r.ChatID, MessageID: r.MessageID})
	}
	return nil
}

// show raises b on its own goroutine, since a banner waits for the reader,
// and carries out whatever they answer it with.
func (t *Triager) show(ctx context.Context, b Banner) {
	if t.Notifier == nil {
		return
	}
	t.banners.Go(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bannerTimeout)
		defer cancel()
		action, err := t.Notifier.Show(ctx, b)
		if err != nil {
			t.log().WarnContext(ctx, "banner", "message", b.MessageID, "err", err)
			return
		}
		switch action {
		case ActionJoin:
			if b.JoinLink != "" {
				t.open(ctx, b.JoinLink)
			}
		case ActionOpen:
			t.goTo(ctx, b)
		}
	})
}

// goTo takes the reader to the chat: in a running TUI, brought to the front,
// or else in the client.
func (t *Triager) goTo(ctx context.Context, b Banner) {
	peers := t.peers(ctx)
	if len(peers) > 0 {
		p := peers[0]
		if i := slices.IndexFunc(peers, func(p presence.State) bool { return p.Focused }); i >= 0 {
			p = peers[i]
		}
		err := t.Presence.Open(ctx, p, b.ChatID)
		if err == nil {
			err = t.Presence.Raise(ctx, p)
		}
		if err == nil {
			return
		}
		t.log().WarnContext(ctx, "banner: open in larkim", "chat", b.ChatID, "pid", p.PID, "err", err)
	}
	t.open(ctx, applink.ChatLink(b.ChatID, b.MessageID, b.Position))
}

func (t *Triager) open(ctx context.Context, url string) {
	if err := t.Desktop.Open(ctx, url); err != nil {
		t.log().WarnContext(ctx, "banner: open", "url", url, "err", err)
	}
}

// DraftPass drafts replies for the P0 verdicts still owed one.
func (t *Triager) DraftPass(ctx context.Context) error {
	if t.Drafter == nil {
		return nil
	}
	self := t.Self(ctx)
	if self == "" {
		return nil
	}
	owed, err := t.Store.TriageToDraft(ctx, draftTries)
	if err != nil {
		return err
	}
	for _, o := range owed {
		if err := t.draft(ctx, o, self); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			t.log().ErrorContext(ctx, "triage draft", "message", o.MessageID, "chat", o.ChatID, "try", o.DraftTries+1, "err", err)
			if err := t.Store.FailTriageDraft(ctx, o.MessageID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Triager) draft(ctx context.Context, o store.Triage, self string) error {
	now := t.Clock.Now()
	done := func() error { return t.Store.MarkTriageDrafted(ctx, o.MessageID, t.Clock.Now().UnixMilli()) }
	if now.Sub(time.UnixMilli(o.JudgedMs)) > draftFresh {
		return done()
	}
	m, err := t.Store.GetMessage(ctx, o.MessageID)
	if errors.Is(err, store.ErrNotFound) {
		return done()
	}
	if err != nil {
		return err
	}
	replied, err := t.Store.RepliedSince(ctx, m.ChatID, self, m.CreateMs)
	if err != nil {
		return err
	}
	if replied {
		return done()
	}
	c, err := t.Store.GetChat(ctx, m.ChatID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	recent, err := t.Store.ListMessages(ctx, store.MessageQuery{ChatID: m.ChatID, UntilMs: m.CreateMs,
		ExcludeThreadReplies: m.MessagePosition >= 0, Desc: true, Limit: draftContext})
	if err != nil {
		return err
	}
	slices.Reverse(recent)
	name := cmp.Or(c.Name, m.ChatID)
	reader, _ := t.Store.GetContact(ctx, self)
	dctx, cancel := context.WithTimeout(ctx, draftTimeout)
	defer cancel()
	d, err := t.Drafter.Draft(dctx, DraftAsk{ChatName: name, Reader: reader.Name, Aliases: selfAliases, Transcript: ai.Transcript(name, recent, self, nil),
		Target: ai.Line(m, self, nil), Now: now})
	if err != nil {
		return err
	}
	if len(d.Texts) > 0 {
		if err := t.Store.PutCandidates(ctx, m.MessageID, m.ChatID, d.Texts, d.Format, now.UnixMilli()); err != nil {
			return fmt.Errorf("candidates: %w", err)
		}
	}
	if r := d.Remind; r != nil && r.At.After(now) && r.Title != "" {
		if err := t.Store.PutReminder(ctx, store.Reminder{MessageID: m.MessageID, ChatID: m.ChatID,
			FireMs: r.At.UnixMilli(), Title: r.Title}); err != nil {
			return fmt.Errorf("reminder: %w", err)
		}
	}
	return done()
}

func clip(s string) string {
	r := []rune(s)
	if len(r) <= bodyMax {
		return s
	}
	return string(r[:bodyMax-1]) + "…"
}
