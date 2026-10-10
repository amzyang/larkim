package triage

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/presence"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

type fakeJudge struct {
	verdict Verdict
	err     error
	asked   []string
	readers []string
	aliases [][]string
}

func (j *fakeJudge) Judge(_ context.Context, a GrayAsk) (Verdict, error) {
	j.asked = append(j.asked, a.Message.MessageID)
	j.readers = append(j.readers, a.Reader)
	j.aliases = append(j.aliases, a.Aliases)
	return j.verdict, j.err
}

type fakeDrafter struct {
	draft Draft
	err   error
	asked []DraftAsk
	// remindOn, when set, keeps the reminder for targets containing it only.
	remindOn string
}

func (d *fakeDrafter) Draft(_ context.Context, a DraftAsk) (Draft, error) {
	d.asked = append(d.asked, a)
	out := d.draft
	if d.remindOn != "" && !strings.Contains(a.Target, d.remindOn) {
		out.Remind = nil
	}
	return out, d.err
}

// fakeNotifier records every banner and answers each with action.
type fakeNotifier struct {
	mu      gosync.Mutex
	action  string
	banners []Banner
}

func (n *fakeNotifier) Show(_ context.Context, b Banner) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.banners = append(n.banners, b)
	return n.action, nil
}

func (n *fakeNotifier) shown() []Banner {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.banners)
}

type fakeDesktop struct {
	mu        gosync.Mutex
	larkFront bool
	idle      time.Duration
	opened    []string
}

func (d *fakeDesktop) LarkFrontmost(context.Context) bool { return d.larkFront }
func (d *fakeDesktop) Idle(context.Context) time.Duration { return d.idle }
func (d *fakeDesktop) Open(_ context.Context, url string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opened = append(d.opened, url)
	return nil
}

func (d *fakeDesktop) urls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.opened)
}

type fakePresence struct {
	mu       gosync.Mutex
	peers    []presence.State
	raiseErr error
	opened   []string
	raised   []int
}

func (p *fakePresence) Peers(context.Context) ([]presence.State, error) { return p.peers, nil }
func (p *fakePresence) Open(_ context.Context, s presence.State, chat string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opened = append(p.opened, chat)
	return nil
}

func (p *fakePresence) Raise(_ context.Context, s presence.State) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.raised = append(p.raised, s.PID)
	return p.raiseErr
}

type harness struct {
	tr       *Triager
	st       *store.Store
	clock    *fakeClock
	notifier *fakeNotifier
	desktop  *fakeDesktop
	presence *fakePresence
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	require.NoError(t, st.UpsertChats(t.Context(), []store.Chat{group, p2p,
		{ChatID: "oc_muted", Name: "闲聊群", ChatMode: "group"}}, 1))
	require.NoError(t, st.SetMuteStatus(t.Context(), map[string]bool{"oc_muted": true}, nil, 1))
	h := &harness{st: st, clock: &fakeClock{t: t0}, notifier: &fakeNotifier{action: ActionDismiss},
		desktop: &fakeDesktop{idle: time.Minute}, presence: &fakePresence{}}
	h.tr = &Triager{Store: st, Notifier: h.notifier, Desktop: h.desktop, Presence: h.presence,
		Clock: h.clock, Self: func(context.Context) string { return "ou_self" }}
	h.tr.SetRules(NewRules(config.Notifications{}))
	return h
}

// arrive stores m as a message created ago before the clock and rendered.
func (h *harness) arrive(t *testing.T, m store.Message, ago time.Duration) {
	t.Helper()
	m.CreateMs = h.clock.t.Add(-ago).UnixMilli()
	m.MessagePosition = m.CreateMs
	m.RawJSON = "{}"
	m.RenderedAt = 0
	_, err := h.st.UpsertMessages(t.Context(), []store.Message{m}, h.clock.t.UnixMilli())
	require.NoError(t, err)
	require.NoError(t, h.st.UpdateRendered(t.Context(), m.MessageID, m.Content, "", h.clock.t.UnixMilli()))
}

func (h *harness) pass(t *testing.T) {
	t.Helper()
	require.NoError(t, h.tr.Pass(t.Context()))
	h.tr.Wait()
}

func (h *harness) verdicts(t *testing.T) map[string]store.TriageEntry {
	t.Helper()
	rows, err := h.st.ListTriage(t.Context(), store.TriageQuery{})
	require.NoError(t, err)
	out := map[string]store.TriageEntry{}
	for _, r := range rows {
		out[r.MessageID] = r
	}
	return out
}

func TestTriager_JudgesOnlyRecentRenderedArrivals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.arrive(t, text("om_backfill", "oc_peer", "ou_a", "上周的事"), time.Hour)
	h.arrive(t, text("om_now", "oc_peer", "ou_a", "在吗"), time.Second)
	_, err := h.st.UpsertMessages(t.Context(), []store.Message{{MessageID: "om_raw", ChatID: "oc_peer", MsgType: "text",
		SenderID: "ou_a", SenderType: "user", ContentRaw: `{"text":"x"}`, CreateMs: t0.UnixMilli(), RawJSON: "{}"}}, t0.UnixMilli())
	require.NoError(t, err)

	h.pass(t)
	v := h.verdicts(t)
	require.Len(t, v, 1, "a backfilled and an unrendered message are not judged")
	require.Equal(t, "P0", v["om_now"].Level)
	require.Equal(t, "p2p", v["om_now"].Reason)
}

func TestTriager_GrayZoneFallsToP1WithoutJudgeOrOnJevError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.arrive(t, text("om_a", "oc_team", "ou_a", "午饭吃啥"), time.Second)
	h.pass(t)
	require.Equal(t, "P1", h.verdicts(t)["om_a"].Level)
	require.Equal(t, "p1", h.verdicts(t)["om_a"].Reason, "no judge configured")

	j := &fakeJudge{err: errors.New("jev: 503")}
	h.tr.Judge = j
	h.arrive(t, text("om_b", "oc_team", "ou_a", "谁能看下"), time.Second)
	h.pass(t)
	require.Equal(t, "P1", h.verdicts(t)["om_b"].Level)
	require.Equal(t, "jev-error", h.verdicts(t)["om_b"].Reason)

	p := 0.9
	j.err, j.verdict = nil, Verdict{Level: P0, Reason: "jev", JevP: &p, Jev: &jev.Rank{Model: "jev-1.13.0", Fits: p,
		Options: []jev.Option{{Key: "act", P: 1}}, Nouls: map[string]float64{ToReader: 0.8}}}
	h.arrive(t, text("om_c", "oc_team", "ou_a", "线上报错了"), time.Second)
	h.arrive(t, text("om_d", "oc_muted", "ou_a", "线上报错了"), time.Second)
	h.pass(t)
	require.Equal(t, "P0", h.verdicts(t)["om_c"].Level)
	require.InDelta(t, 0.9, *h.verdicts(t)["om_c"].JevP, 1e-9)
	require.JSONEq(t, `{"model":"jev-1.13.0","pick":{"act":1},"fits":0.9,"nouls":{"to_reader":0.8}}`,
		string(h.verdicts(t)["om_c"].Jev), "the whole answer is kept beside the verdict")
	require.Empty(t, h.verdicts(t)["om_b"].Jev, "a failed call has no answer to keep")
	require.Equal(t, "muted", h.verdicts(t)["om_d"].Reason, "a muted chat is not asked about")
	require.Equal(t, []string{"om_b", "om_c"}, j.asked)
}

func TestTriager_TellsTheJudgeTheReadersName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.st.UpsertContacts(t.Context(), []store.Contact{{OpenID: "ou_self", Name: "林岚"}}, 1))
	j := &fakeJudge{verdict: Verdict{Level: P1, Reason: "jev:fyi"}}
	h.tr.Judge = j
	h.arrive(t, text("om_a", "oc_team", "ou_a", "林岚 帮忙看下"), time.Second)
	h.pass(t)
	require.Equal(t, []string{"林岚"}, j.readers)
	require.Equal(t, [][]string{selfAliases}, j.aliases)
}

func TestTriager_SkipsBannerWhenReadElsewhereAnsweredOrLarkimFocused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.arrive(t, text("om_read", "oc_peer", "ou_a", "一"), 3*time.Second)
	_, err := h.st.DB().ExecContext(t.Context(), `INSERT INTO read_state(message_id, is_read_remote) VALUES('om_read', 1)`)
	require.NoError(t, err)
	h.pass(t)
	require.Empty(t, h.notifier.shown(), "read on another client")

	h.arrive(t, text("om_ans", "oc_peer", "ou_a", "二"), 2*time.Second)
	h.arrive(t, text("om_mine", "oc_peer", "ou_self", "好"), time.Second)
	h.pass(t)
	require.Empty(t, h.notifier.shown(), "the reader already answered")

	h.arrive(t, text("om_acked", "oc_peer", "ou_a", "周报已发"), time.Second)
	require.NoError(t, h.st.UpdateReactions(t.Context(), "om_acked", reactedBySelf))
	h.pass(t)
	require.Empty(t, h.notifier.shown(), "the reader already reacted")

	h.presence.peers = []presence.State{{PID: 1, Focused: true}}
	h.arrive(t, text("om_focus", "oc_team", "ou_a", "@林岚"), 0)
	setMention(t, h.st, "om_focus")
	h.pass(t)
	require.Equal(t, "at-me", h.verdicts(t)["om_focus"].Reason)
	require.Empty(t, h.notifier.shown(), "larkim has the reader's attention")

	h.presence.peers = nil
	h.desktop.larkFront = true
	h.arrive(t, text("om_front", "oc_peer", "ou_a", "三"), 0)
	h.pass(t)
	require.Empty(t, h.notifier.shown(), "the client is in front")

	h.desktop.idle = 10 * time.Minute
	h.arrive(t, text("om_away", "oc_peer", "ou_a", "四"), 0)
	h.pass(t)
	require.Len(t, h.notifier.shown(), 1, "a reader who walked away is told even with the client in front")

	for id, e := range h.verdicts(t) {
		if e.Level == "P0" {
			require.NotZero(t, e.NotifiedMs, "%s: a suppressed banner is still done", id)
		}
	}
}

func TestTriager_LeavesTheBacklogOfAStartUnannounced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.arrive(t, text("om_stale", "oc_peer", "ou_a", "早上好"), 10*time.Minute)
	h.pass(t)
	require.Equal(t, "P0", h.verdicts(t)["om_stale"].Level)
	require.Empty(t, h.notifier.shown(), "ten minutes late is the unread list's, not a banner's")
}

func TestTriager_ClickOpensChatInTUIElseFallsBackToApplink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.notifier.action = ActionOpen
	h.presence.peers = []presence.State{{PID: 1}, {PID: 2}}
	h.arrive(t, text("om_a", "oc_peer", "ou_a", "在吗"), 0)
	h.pass(t)
	require.Equal(t, []string{"oc_peer"}, h.presence.opened)
	require.Equal(t, []int{1}, h.presence.raised)
	require.Empty(t, h.desktop.urls())

	h.presence.peers = nil
	h.arrive(t, text("om_b", "oc_peer", "ou_a", "还在吗"), 0)
	h.pass(t)
	require.Len(t, h.desktop.urls(), 1)
	require.Contains(t, h.desktop.urls()[0], "lark://applink.feishu.cn/client/chat/open?openChatId=oc_peer")

	h.presence.peers = []presence.State{{PID: 3}}
	h.presence.raiseErr = errors.New("kitten: no window")
	h.arrive(t, text("om_c", "oc_peer", "ou_a", "人呢"), 0)
	h.pass(t)
	require.Len(t, h.desktop.urls(), 2, "a window that cannot be raised falls back to the client")
}

func TestTriager_VCBannerOffersJoinOnlyForLiveCallWithNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.notifier.action = ActionJoin
	call := store.Message{MessageID: "om_live", ChatID: "oc_team", MsgType: "video_chat", SenderID: "ou_a",
		SenderType: "user", SenderName: "张三", ContentRaw: `{"topic":"站会","meet_number":"100000000","start_time":"1"}`}
	ended := call
	ended.MessageID, ended.ContentRaw = "om_ended", `{"topic":"站会","meet_number":"100000000","start_time":"1","end_time":"60"}`
	h.arrive(t, call, 0)
	h.arrive(t, ended, 0)
	h.pass(t)

	byID := map[string]Banner{}
	for _, b := range h.notifier.shown() {
		byID[b.MessageID] = b
	}
	require.Equal(t, "lark://vc.feishu.cn/j/100000000", byID["om_live"].JoinLink)
	require.Empty(t, byID["om_ended"].JoinLink, "a call that ended has nothing to join")
	require.Equal(t, []string{"lark://vc.feishu.cn/j/100000000"}, h.desktop.urls())
}

func TestTriager_DraftsP0IntoCandidatesAndGivesUpAfterTwoTries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := &fakeDrafter{draft: Draft{Texts: []string{"在的，直接说"}, Format: "text"}}
	h.tr.Drafter = d
	h.arrive(t, text("om_q", "oc_peer", "ou_a", "在吗"), time.Second)
	h.arrive(t, text("om_p1", "oc_team", "ou_a", "午饭吃啥"), time.Second)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.Len(t, d.asked, 1, "only P0 is drafted for")
	require.Contains(t, d.asked[0].Transcript, "在吗")

	cands, err := h.st.ChatCandidates(t.Context(), "oc_peer", "ou_self")
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, "在的，直接说", cands[0].Text)

	d.err = errors.New("agent: exit 1")
	h.arrive(t, text("om_fail", "oc_peer", "ou_a", "急"), 0)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.Len(t, d.asked, 3, "two tries and no more")
	cands, err = h.st.ChatCandidates(t.Context(), "oc_peer", "ou_self")
	require.NoError(t, err)
	require.Len(t, cands, 1)
}

func TestTriager_TellsTheDrafterTheReadersName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.st.UpsertContacts(t.Context(), []store.Contact{{OpenID: "ou_self", Name: "林岚"}}, 1))
	d := &fakeDrafter{}
	h.tr.Drafter = d
	h.arrive(t, text("om_q", "oc_peer", "ou_a", "在吗"), time.Second)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.Len(t, d.asked, 1)
	require.Equal(t, "林岚", d.asked[0].Reader)
	require.Equal(t, selfAliases, d.asked[0].Aliases)
}

func TestTriager_AnEmptyDraftIsDoneWithoutCandidates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tr.Drafter = &fakeDrafter{}
	h.arrive(t, text("om_fyi", "oc_peer", "ou_a", "周报已发"), 0)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	cands, err := h.st.ChatCandidates(t.Context(), "oc_peer", "ou_self")
	require.NoError(t, err)
	require.Empty(t, cands)
	require.NotZero(t, h.verdicts(t)["om_fyi"].DraftedMs)
}

// reactedBySelf is a reaction block holding one reaction of the reader's.
const reactedBySelf = `{"details":[{"emoji_type":"OK","action_time":"1","operator":{"operator_id":"ou_self","operator_type":"user"}}]}`

func TestTriager_StoresAReactionOnlyAnswer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tr.Drafter = &fakeDrafter{draft: Draft{Reactions: []string{"Get"}, Format: "text"}}
	h.arrive(t, text("om_fyi", "oc_peer", "ou_a", "周报已发"), 0)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	cands, err := h.st.ChatCandidates(t.Context(), "oc_peer", "ou_self")
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, "Get", cands[0].Reaction)
}

func TestTriager_SkipsDraftingAMessageTheReaderReactedTo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := &fakeDrafter{draft: Draft{Texts: []string{"好"}, Format: "text"}}
	h.tr.Drafter = d
	h.arrive(t, text("om_q", "oc_peer", "ou_a", "在吗"), time.Second)
	h.pass(t)
	require.NoError(t, h.st.UpdateReactions(t.Context(), "om_q", reactedBySelf))
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.Empty(t, d.asked, "a reaction already answered it")
	require.NotZero(t, h.verdicts(t)["om_q"].DraftedMs)
}

func TestTriager_ResumesAfterRestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := &fakeDrafter{draft: Draft{Texts: []string{"收到"}, Format: "text"}}
	h.tr.Drafter = d
	h.arrive(t, text("om_a", "oc_peer", "ou_a", "在吗"), 0)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))

	again := &Triager{Store: h.st, Notifier: h.notifier, Desktop: h.desktop, Presence: h.presence,
		Clock: h.clock, Drafter: d, Self: h.tr.Self}
	again.SetRules(NewRules(config.Notifications{}))
	require.NoError(t, again.Pass(t.Context()))
	again.Wait()
	require.NoError(t, again.DraftPass(t.Context()))
	require.Len(t, h.notifier.shown(), 1, "no second banner")
	require.Len(t, d.asked, 1, "no second draft")
}

func TestTriager_FiresDueRemindersAndDropsLateOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	at := t0.Add(30 * time.Minute)
	h.tr.Drafter = &fakeDrafter{draft: Draft{Remind: &Remind{At: at, Title: "14:00 前交周报"}}}
	h.arrive(t, text("om_due", "oc_peer", "ou_a", "14:00 前交周报"), 0)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	require.Len(t, h.notifier.shown(), 1, "the arrival's own banner")

	h.clock.t = at.Add(-time.Minute)
	h.pass(t)
	require.Len(t, h.notifier.shown(), 1, "not due yet")
	h.clock.t = at
	h.pass(t)
	require.Len(t, h.notifier.shown(), 2)
	require.Equal(t, "14:00 前交周报", h.notifier.shown()[1].Body)
	h.pass(t)
	require.Len(t, h.notifier.shown(), 2, "fired once")

	require.NoError(t, h.st.PutReminder(t.Context(), store.Reminder{MessageID: "om_late", ChatID: "oc_peer",
		FireMs: h.clock.t.Add(-20 * time.Minute).UnixMilli(), Title: "错过了"}))
	h.pass(t)
	require.Len(t, h.notifier.shown(), 2, "twenty minutes late is dropped")
	due, err := h.st.DueReminders(t.Context(), h.clock.t.UnixMilli())
	require.NoError(t, err)
	require.Empty(t, due)
}

func TestTriager_APastReminderIsNotPlanned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tr.Drafter = &fakeDrafter{draft: Draft{Remind: &Remind{At: t0.Add(-time.Minute), Title: "已经过了"}}}
	h.arrive(t, text("om_past", "oc_peer", "ou_a", "11:59 开会"), 0)
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	due, err := h.st.DueReminders(t.Context(), t0.Add(time.Hour).UnixMilli())
	require.NoError(t, err)
	require.Empty(t, due)
}

func setMention(t *testing.T, st *store.Store, id string) {
	t.Helper()
	_, err := st.DB().ExecContext(t.Context(),
		`UPDATE messages SET mentions_json = '[{"id":"ou_self","key":"@_user_1","name":"林岚"}]' WHERE message_id = ?`, id)
	require.NoError(t, err)
}
