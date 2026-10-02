package tui

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/agentctx"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"uuid"
)

// The assistant column: sessions isolated per chat, an input of its own under
// the column, and answers that belong to their session rather than to the
// pane. It is held on the Model by pointer, so an answer that finishes while
// the column is hidden — closed, covered by a frame, or left for another chat
// — lands in the model that is still on screen.

// aiPrompt is the client's own placeholder shape, standing in the panel's box
// while nothing is being typed.
const aiPrompt = "Ask about this chat…"

// aiHistory is how many of a session's earlier questions and answers the next
// question carries. A session older than this is a session worth a new one.
const aiHistory = 10

// aiPanel is the column's whole state. A zero value is closed.
type aiPanel struct {
	// open says the column is drawn in the right-hand pane.
	open bool
	// chat is the chat the panel is pointed at, which follows the reader the
	// way the info pane does rather than closing behind them.
	chat string
	// sess are the chat on screen's sessions in creation order, cur the one
	// on screen, and stashed the sessions of chats the reader left, picked
	// back up on return. loaded names the chats whose sessions the store has
	// already answered for this run, so coming back to a chat keeps what the
	// panel holds rather than re-reading it.
	sess    []*aiSession
	stashed map[string][]*aiSession
	loaded  map[string]bool
	cur     int
	// rows is the turn list as the pane draws it, rebuilt on every change,
	// and top the first row of it on screen. rowAct names, for every row, the
	// answer and card it belongs to, which is what the keys and the click
	// zones act on. sel is the cursor row; pendingG is the panel's own gg
	// half.
	rows   []msgRow
	rowAct []aiAct
	top    int
	sel    int
	pG     bool
	// sending maps the outbox ids of cards put on the wire to the cards they
	// came from, so the ✓ a successful send earns lands on the card that
	// earned it.
	sending map[string]aiAct
	// follow sticks the viewport to the bottom while an answer streams, and
	// is given up the moment a scroll says otherwise.
	follow bool
	// input is the panel's own box, the one sideAI names.
	input textarea.Model
	// anchor is the message the next question is about, selection the ids a
	// VISUAL range left, both dropped with ctrl+r and re-taken with a.
	anchor    *store.Message
	selection []string
}

// aiSession is one conversation with the assistant. An empty one — opened
// with a or A and never asked anything — stays in memory and is gone on quit.
type aiSession struct {
	id      string
	title   string
	created int64
	turns   []*aiTurn
}

// aiTurn is one question and its answer, with the context the question was
// asked in. Later actions on the answer read this record, never the live
// cursor: what the reader asked about when they asked is the only thing that
// says what an answer to it was about.
type aiTurn struct {
	id string
	// ask is what the reader typed, as the list shows it; sent is what the
	// model was asked, which differs for the :ai forms.
	ask, sent string
	// the recorded context. anchorID names the message the question was about
	// even after the message itself is gone; anchor is that message as it
	// stood, resolved from the id.
	anchorID string
	anchor   *store.Message
	thread   string
	window   int
	compose  string
	sel      []string

	seq    int
	state  aiTurnState
	answer string
	err    string
	at     time.Time
	// sentAt marks the cards of this answer that were sent, and when: the ✓ a
	// sent card shows until the panel closes. In-memory only — the wire, not
	// the panel, is where a sent message's truth lives.
	sentAt map[int]time.Time
	// stream is the card this answer streams into the chat as, when the
	// question was asked with ctrl+s. Nil when the answer stays in the panel.
	stream *aiStreamCard
	// traces are the dim lines the answer's history calls left, in the order
	// they ran.
	traces []string
	ch     <-chan ai.Chunk
	cancel context.CancelFunc
	// cache holds the turn's laid-out rows under their key. A rebuild runs
	// on every chunk of an answer; the turns that did not move are the whole
	// session, and re-rendering them at 20 chunks a second is what makes a
	// long one heavy.
	cache turnCache
}

// turnKey is everything a turn's laid-out rows depend on. metaGen is the
// model's meta revision: a turn's rows quote senders and preview cards
// through it, so a page that lands new names restyles them.
type turnKey struct {
	state                 aiTurnState
	answerLen, traces     int
	sent, w               int
	last, anchored        bool
	posted, failed, close bool
	metaGen               int
}

// turnCache is a turn's rows under the key they were laid out for.
type turnCache struct {
	key  turnKey
	rows []msgRow
	acts []aiAct
}

// keyOf is what would make this turn's rows different now.
func (t *aiTurn) keyOf(w int, last bool, metaGen int) turnKey {
	k := turnKey{state: t.state, answerLen: len(t.answer), traces: len(t.traces),
		sent: len(t.sentAt), w: w, last: last, anchored: t.anchor != nil, metaGen: metaGen}
	if c := t.stream; c != nil {
		k.posted, k.failed, k.close = c.messageID != "", c.err != "", c.closed
	}
	return k
}

type aiTurnState int

// The states are the store's own numbers, so a turn persists as the state it
// ran with — SaveAITurn writes State: int(t.state) and load casts back — and
// neither side can drift from the other.
const (
	aiAsking  = aiTurnState(store.AITurnAsking)
	aiDone    = aiTurnState(store.AITurnDone)
	aiFailed  = aiTurnState(store.AITurnFailed)
	aiStopped = aiTurnState(store.AITurnStopped)
	// aiInterrupted is an answer that never finished because the program
	// left: stopped on quit, asking at load.
	aiInterrupted = aiTurnState(store.AITurnInterrupted)
)

// streaming reports an answer still arriving.
func (t *aiTurn) streaming() bool { return t.state == aiAsking }

// aiOpen reports that the assistant column holds the right-hand pane, over
// whatever frame it covered without closing it.
func (m Model) aiOpen() bool { return m.aiP != nil && m.aiP.open }

// newAI builds the panel's state the first time it is opened.
func newAI(dark bool) *aiPanel {
	in := newComposer(dark)
	in.Placeholder = aiPrompt
	return &aiPanel{input: in, follow: true}
}

// point aims the panel at chat: the sessions of the chat it stood on are
// stashed and the new chat's own picked up — out of the store the first time
// — so the header never lists another chat's conversations.
func (p *aiPanel) point(chat string, m Model) {
	if p.chat != chat {
		if p.stashed == nil {
			p.stashed = map[string][]*aiSession{}
		}
		if len(p.sess) > 0 {
			p.stashed[p.chat] = p.sess
		}
		p.sess, p.stashed[chat] = p.stashed[chat], nil
		delete(p.stashed, chat)
		p.chat = chat
		p.cur, p.top, p.follow = 0, 0, true
	}
	p.load(m)
}

// load reads the chat's sessions out of the store the first time the panel
// stands on it: a restart finds its conversations where they were left. A
// turn still asking at load is an answer nobody finished, and reads as
// interrupted.
func (p *aiPanel) load(m Model) {
	// A model a test built by hand carries no store; its sessions are the
	// ones the test put in the panel, and the store has none to answer with.
	if m.deps.Store == nil {
		return
	}
	if p.loaded == nil {
		p.loaded = map[string]bool{}
	}
	if p.loaded[p.chat] {
		return
	}
	p.loaded[p.chat] = true
	ctx := context.Background()
	stored, err := m.deps.Store.ListAISessions(ctx, p.chat)
	if err != nil {
		m.deps.log().Error("load ai sessions", "chat_id", p.chat, "err", err)
		return
	}
	sessions := make([]*aiSession, 0, len(stored))
	var anchors []string
	for _, sv := range stored {
		s := &aiSession{id: sv.ID, title: sv.Title, created: sv.CreatedMs}
		turns, err := m.deps.Store.ListAITurns(ctx, sv.ID)
		if err != nil {
			m.deps.log().Error("load ai turns", "session_id", sv.ID, "err", err)
			turns = nil
		}
		for _, tv := range turns {
			t := &aiTurn{id: tv.ID, seq: tv.Seq, ask: tv.Ask, sent: tv.Sent,
				anchorID: tv.AnchorID, thread: tv.ThreadID, window: tv.Window,
				compose: tv.Compose, sel: tv.Sel, answer: tv.Answer, err: tv.Err,
				at: time.UnixMilli(tv.AtMs)}
			t.state = aiTurnState(tv.State)
			if t.state == aiAsking {
				// An answer still asking when the store is read is one nobody
				// finished: the program left mid-turn.
				t.state = aiInterrupted
			}
			if tv.AnchorID != "" {
				anchors = append(anchors, tv.AnchorID)
			}
			if tv.CardID != "" {
				// The card is a message in the chat now; the stream itself is
				// over, and what it left is closed at the text it last held.
				t.stream = &aiStreamCard{chatID: p.chat, messageID: tv.CardID,
					wrote: len(tv.Answer), closed: true}
			}
			s.turns = append(s.turns, t)
		}
		sessions = append(sessions, s)
	}
	if len(sessions) == 0 {
		return
	}
	// The anchors are messages of this chat, already synced; a recalled one
	// resolves to nothing and its chip goes with it.
	picked, err := m.deps.Store.MessagesByIDs(ctx, anchors)
	if err != nil {
		m.deps.log().Error("load ai anchors", "chat_id", p.chat, "err", err)
	}
	for _, s := range sessions {
		for _, t := range s.turns {
			if x, ok := picked[t.anchorID]; ok {
				a := x
				t.anchor = &a
			}
		}
	}
	// Sessions this run created are newer than anything stored, so they keep
	// the tail of the list.
	p.sess = append(sessions, p.sess...)
	p.cur = len(p.sess) - 1
}

// openAI shows the assistant column on chat. fresh starts a new session rather
// than the chat's latest, and none of it asks the agent anything: opening is
// reading, asking is Enter.
func (m Model) openAI(chat string, fresh bool) (Model, tea.Cmd) {
	if chat == "" {
		return m.notify("open a chat first", true), nil
	}
	if m.aiP == nil {
		m.aiP = newAI(m.dark)
	}
	m.aiP.open = true
	m.aiP.point(chat, m)
	if fresh || len(m.aiP.sess) == 0 {
		m.aiP.sess = append(m.aiP.sess, &aiSession{id: uuid.New().String(), created: time.Now().UnixMilli()})
		m.aiP.cur = len(m.aiP.sess) - 1
		m.aiP.top = 0
	}
	m.aiP.rebuild(m)
	m.focus = paneThread
	m.layout()
	return m, nil
}

// closeAI puts the column away and uncovers the frame it stood over, answer
// streams included: they belong to their sessions, not to the pane. The ✓ a
// sent card showed goes with the column — the wire is where a sent message's
// truth lives now.
func (m Model) closeAI() Model {
	if m.aiP != nil {
		m.aiP.open = false
		for _, s := range m.aiP.allSessions() {
			for _, t := range s.turns {
				clear(t.sentAt)
			}
		}
	}
	// The box goes with the column, so a cursor parked in it steps back to
	// the pane behind; the frame under the panel comes back focused, the way
	// the reader left it, and with no frame under it the step is to the
	// messages pane.
	if m.focus == paneInput {
		m.focus = paneThread
	}
	if !m.threadOpen() && m.focus == paneThread {
		m.focus = paneMessages
	}
	m.layout()
	return m
}

// aiContext is the context the next question carries, read where the reader
// stands: the message under the cursor, the quote the composer being written
// in answers, or nothing from the chats pane. The panel's own pane has no
// cursor to re-take, so its anchor stands.
func (m Model) aiContext() (*store.Message, []string) {
	switch m.focus {
	case paneThread:
		if m.aiOpen() {
			return m.aiP.anchor, m.aiP.selection
		}
		if sel, ok := m.selected(); ok {
			return &sel, nil
		}
	case paneInput:
		if q, ok := m.quotedOn(m.side); ok {
			return &q, nil
		}
	case paneChats:
		return nil, nil
	default:
		if sel, ok := m.selected(); ok {
			return &sel, nil
		}
	}
	return nil, nil
}

// enterAI moves the keys into the panel's box.
func (m Model) enterAI() (tea.Model, tea.Cmd) {
	m.mode = modeInsert
	m.focus = paneInput
	m.side = sideAI
	m.replan()
	m.layout()
	cmd := m.aiP.input.Focus()
	return m, cmd
}

// dropAIChip takes the last context chip away: the anchor first, then the
// selection. The window is the chat's own and stays.
func (m *Model) dropAIChip() {
	switch {
	case m.aiP.anchor != nil:
		m.aiP.anchor = nil
	case len(m.aiP.selection) > 0:
		m.aiP.selection = nil
	}
}

// session is the session on screen.
func (p *aiPanel) session() *aiSession {
	if p.cur < 0 || p.cur >= len(p.sess) {
		return nil
	}
	return p.sess[p.cur]
}

// switchAI moves between the chat's sessions, hiding nothing and stopping
// nothing: another session's answer keeps streaming into its own place.
func (m *Model) switchAI(d int) {
	p := m.aiP
	if len(p.sess) < 2 {
		return
	}
	p.cur = (p.cur + d + len(p.sess)) % len(p.sess)
	p.top, p.follow = 0, true
	p.rebuild(*m)
	m.layout()
}

// busy says the session on screen is still answering, which is the one thing
// that keeps another question out of it.
func (p *aiPanel) busy() bool {
	s := p.session()
	return s != nil && len(s.turns) > 0 && s.turns[len(s.turns)-1].streaming()
}

// stopAll cancels every answer in flight, which is what leaving the program
// owes them: nothing keeps a child process alive past its reader. The stashed
// sessions are in flight as surely as the chat on screen. Each answer that
// never finished is marked interrupted on its way out — its card marked so
// too, the closing write riding the exit ahead of the persist — so the next
// run reads it as that rather than as an answer still owed.
func (p *aiPanel) stopAll(m *Model) []tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range p.allSessions() {
		for _, t := range s.turns {
			if t.cancel != nil {
				t.cancel()
			}
			if t.streaming() {
				t.state, t.cancel = aiInterrupted, nil
				cmds = append(cmds, saveTurnCmd(m.deps, s, t))
			}
			if cmd := m.pumpStreamCard(t); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// storedTurn snapshots a turn whole, the shape the table upserts: every write
// carries the complete state, so an out-of-order write cannot leave half a
// turn behind.
func storedTurn(s *aiSession, t *aiTurn) store.AITurn {
	v := store.AITurn{ID: t.id, SessionID: s.id, Seq: t.seq, Ask: t.ask, Sent: t.sent,
		AnchorID: t.anchorID, ThreadID: t.thread, Window: t.window,
		Compose: t.compose, Sel: t.sel, State: int(t.state), Answer: t.answer,
		Err: t.err, AtMs: t.at.UnixMilli()}
	if t.stream != nil {
		v.CardID = t.stream.messageID
	}
	return v
}

// saveTurnCmd persists a turn as it stands at this moment. The snapshot is
// taken here, before the cmd runs, so a turn that keeps moving under the
// Update loop writes the state it was asked to write.
func saveTurnCmd(d Deps, s *aiSession, t *aiTurn) tea.Cmd {
	v := storedTurn(s, t)
	return func() tea.Msg {
		if err := d.Store.SaveAITurn(context.Background(), v); err != nil {
			d.log().Error("save ai turn", "id", v.ID, "err", err)
		}
		return nil
	}
}

// saveSessionCmd persists a session row. A session is stored when its first
// question is asked — until then it is the reader's to leave behind — and the
// upsert keeps that first write's creation time.
func saveSessionCmd(d Deps, chat string, s *aiSession) tea.Cmd {
	v := store.AISession{ID: s.id, ChatID: chat, Title: s.title, CreatedMs: s.created}
	return func() tea.Msg {
		if err := d.Store.SaveAISession(context.Background(), v); err != nil {
			d.log().Error("save ai session", "id", v.ID, "err", err)
		}
		return nil
	}
}

// aiDraftText is what the composer the anchor belongs to holds, the draft a
// question can be about.
func (m Model) aiDraftText() string {
	if a := m.aiP.anchor; a != nil && m.rightHasComposer() && a.ThreadID == m.threadID {
		return m.rightInput.Value()
	}
	return m.input.Value()
}

// submitAI is Enter in the panel's box: the question becomes a turn of the
// session on screen, and the asking itself happens off the Update loop.
func (m Model) submitAI() (tea.Model, tea.Cmd) {
	return m.askAI(strings.TrimSpace(m.aiP.input.Value()), "", false)
}

// cloneMsg copies a message the way a recorded context wants it: nobody's
// later edits to the model may reach back into what a question was asked
// about.
func cloneMsg(x *store.Message) *store.Message {
	if x == nil {
		return nil
	}
	c := *x
	return &c
}

// aiStartedMsg arms the reader of a stream askTurn began.
type aiStartedMsg struct {
	turn   string
	ch     <-chan ai.Chunk
	cancel context.CancelFunc
}

// askTurn composes the turn — the chat window read from the store, what the
// question is about, the session so far — and starts the stream. It runs off
// the Update loop because a fork and a store read are both too slow to sit
// under a keypress. off, when set, is the failure the turn ends with instead:
// a panel without an agent takes the question and answers it with the notice.
func askTurn(d Deps, client AIStreamer, off error, p *aiPanel, s *aiSession, t *aiTurn, h ai.History) tea.Cmd {
	if off != nil {
		return func() tea.Msg {
			return aiStartedMsg{turn: t.id, ch: errCh(ai.Chunk{Err: off, Done: true})}
		}
	}
	chatID := p.chat
	// The window is gathered ahead of the stream so the history and about are
	// the only things taken from the panel, which the Update loop owns. The
	// history stops at this turn's own place: a regenerated answer replays
	// the conversation as it stood when the question was asked, and its own
	// earlier answer is not part of that.
	history := make([]ai.QA, 0, aiHistory)
	for _, older := range s.turns {
		if older.seq >= t.seq || older.state != aiDone {
			continue
		}
		history = append(history, ai.QA{Question: older.sent, Answer: older.answer})
	}
	history = history[max(0, len(history)-aiHistory):]
	anchor := cloneMsg(t.anchor)
	sel := slices.Clone(t.sel)
	compose, window, until := t.compose, t.window, t.at.UnixMilli()
	return func() tea.Msg {
		ctx := context.Background()
		in, extra, err := aiWindow(ctx, d, chatID, window, anchor, sel, compose, until)
		if err != nil {
			return aiStartedMsg{turn: t.id, ch: errCh(ai.Chunk{Err: err, Done: true})}
		}
		turn := ai.Turn{Window: agentctx.Render(in), About: extra,
			History: history, Question: t.sent}
		cctx, cancel := context.WithCancel(ctx)
		// An answer that is the message is asked as the message: no
		// commentary, no reply blocks.
		if t.stream != nil {
			return aiStartedMsg{turn: t.id, ch: client.StreamChat(cctx, turn.Window, turn.Prompt()), cancel: cancel}
		}
		// With history on, the agent reads the chat itself through the
		// commands it was taught; the gate holds regardless of how the agent
		// was configured, and nothing else reaches it.
		if h.ChatID != "" {
			return aiStartedMsg{turn: t.id, ch: client.StreamHistory(cctx, turn.Window, turn.Prompt(), h), cancel: cancel}
		}
		return aiStartedMsg{turn: t.id, ch: client.Stream(cctx, turn.Window, turn.Prompt()), cancel: cancel}
	}
}

// errCh stands in for a stream that has already ended.
func errCh(c ai.Chunk) <-chan ai.Chunk {
	ch := make(chan ai.Chunk, 1)
	ch <- c
	return ch
}

// onAIStarted arms the reader of a stream the asking began.
func (m Model) onAIStarted(msg aiStartedMsg) (tea.Model, tea.Cmd) {
	_, t := m.aiP.findTurn(msg.turn)
	if t == nil {
		// The session left before the stream could hand its cancel over, and
		// nobody else will ever read the channel: the agent stops here.
		if msg.cancel != nil {
			msg.cancel()
		}
		return m, nil
	}
	t.ch, t.cancel = msg.ch, msg.cancel
	return m, waitForAI(t.id, t.ch)
}

// findTurn finds a turn by id across the panel's sessions, the chat on
// screen's and the stashed ones alike: an answer belongs to its session
// wherever the reader has since gone. The session it returns is what
// persisting a turn needs.
func (p *aiPanel) findTurn(id string) (*aiSession, *aiTurn) {
	for _, s := range p.allSessions() {
		for _, t := range s.turns {
			if t.id == id {
				return s, t
			}
		}
	}
	return nil, nil
}

// allSessions is every session the panel still holds.
func (p *aiPanel) allSessions() []*aiSession {
	out := p.sess
	for _, ss := range p.stashed {
		out = append(out, ss...)
	}
	return out
}

// onAIChunk takes one piece of an answer into its turn, wherever the panel
// itself has gone in the meantime. A turn that reaches its end is persisted
// as it stands, whole; a turn streaming into the chat keeps its card moving
// toward the text the answer holds now.
func (m Model) onAIChunk(msg aiChunkMsg) (tea.Model, tea.Cmd) {
	s, t := m.aiP.findTurn(msg.turn)
	if t == nil {
		return m, nil
	}
	// A closing chunk may carry the last piece with it, so text is taken in
	// every state.
	c := msg.chunk
	t.answer += c.Text
	if c.Trace != "" {
		t.traces = append(t.traces, c.Trace)
	}
	var cmds []tea.Cmd
	done := false
	switch {
	case c.Err != nil:
		t.state, t.err, done = aiFailed, c.Err.Error(), true
		t.cancel = nil
	case c.Stopped:
		t.state, t.cancel, done = aiStopped, nil, true
	case !c.Done:
		cmds = append(cmds, waitForAI(t.id, t.ch))
	default:
		t.state, t.cancel, done = aiDone, nil, true
	}
	if done {
		cmds = append(cmds, saveTurnCmd(m.deps, s, t))
	}
	cmds = append(cmds, m.pumpStreamCard(t))
	// A chunk only moves the panel's own rows. The panes around it keep
	// their layout, and a panel that is closed or on another chat shows
	// nothing to redraw — every way back into view rebuilds it.
	if m.aiOpen() && m.aiP.chat == m.chatID {
		m.aiP.rebuild(m)
	}
	if done && !m.aiOpen() || done && m.aiP.chat != m.chatID {
		return m.notify("assistant finished", false), tea.Batch(cmds...)
	}
	return m.notify("", false), tea.Batch(cmds...)
}

// stopAI cancels the answer in flight on the session on screen. The stream
// ends with a stop, which the turn shows rather than an error.
func (m Model) stopAI() (tea.Model, tea.Cmd) {
	s := m.aiP.session()
	if s == nil || !m.aiP.busy() {
		return m.notify("x stops an answer in flight", true), nil
	}
	t := s.turns[len(s.turns)-1]
	if t.cancel != nil {
		t.cancel()
	}
	return m, nil
}

// aiWindow reads the chat as the agent reads it: the newest window messages
// as one agentctx document, and whatever the question is about beyond them —
// the anchor and its thread, a selection, a draft — as blocks of the same
// shape. until cuts the window at a moment: a retried or regenerated answer
// is built from the chat as it stood when its question was asked, never the
// one the chat has moved on to.
func aiWindow(ctx context.Context, d Deps, chatID string, window int, anchor *store.Message, sel []string, composeText string, until int64) (agentctx.Input, string, error) {
	if window <= 0 {
		window = messagePageSize
	}
	q := store.MessageQuery{ChatID: chatID, Desc: true, Limit: window, ExcludeThreadReplies: true}
	if until > 0 {
		q.UntilMs = until
	}
	rows, err := d.Store.ListMessages(ctx, q)
	if err != nil {
		return agentctx.Input{}, "", err
	}
	slices.Reverse(rows)
	rows = slices.DeleteFunc(rows, func(x store.Message) bool { return x.Deleted })
	now := time.Now()
	in, err := assemble(ctx, d, chatID, rows, now)
	if err != nil {
		return agentctx.Input{}, "", err
	}
	in.More = "" // the agent has no shell to resume a copy with
	// No local paths: the agent cannot read this machine, and the paths name
	// it to whoever the provider is.
	for id, rs := range in.Res {
		for i := range rs {
			rs[i].LocalPath = ""
		}
		in.Res[id] = rs
	}
	imgText, err := d.Store.ImageTextsFor(ctx, messageIDs(rows))
	if err != nil {
		return agentctx.Input{}, "", err
	}
	in.ImgText = imgText

	var extra []store.Message
	if anchor != nil && indexOfID(rows, anchor.MessageID) < 0 {
		extra = append(extra, *anchor)
	}
	if anchor != nil && anchor.ThreadID != "" {
		replies, err := d.Store.ListMessages(ctx, store.MessageQuery{ThreadID: anchor.ThreadID, Limit: threadPageSize})
		if err != nil {
			return agentctx.Input{}, "", err
		}
		extra = append(extra, replies...)
	}
	if len(sel) > 0 {
		picked, err := d.Store.MessagesByIDs(ctx, sel)
		if err != nil {
			return agentctx.Input{}, "", err
		}
		for _, id := range sel {
			if x, ok := picked[id]; ok && indexOfID(extra, id) < 0 {
				extra = append(extra, x)
			}
		}
	}
	about := ""
	if draft := strings.TrimSpace(composeText); draft != "" {
		about += "<draft>\n" + draft + "\n</draft>\n\n"
	}
	if len(extra) > 0 {
		blk := agentctx.Input{Now: now, Boundary: in.Boundary, Self: in.Self,
			People: peopleOf(in, extra), Messages: extra, ImgText: imgText}
		about += agentctx.Blocks(blk)
	}
	return in, about, nil
}

// messageIDs names the ids a window carries.
func messageIDs(msgs []store.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, x := range msgs {
		ids = append(ids, x.MessageID)
	}
	return ids
}

// peopleOf picks the people line-up for a block set that is not the window's
// own, in first-appearance order the way Participants orders one.
func peopleOf(in agentctx.Input, msgs []store.Message) []agentctx.Person {
	ids := agentctx.Participants(in.Self.OpenID, msgs)
	var out []agentctx.Person
	for _, id := range ids {
		for _, p := range in.People {
			if p.OpenID == id {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// assistantOff is the failure a question ends with when no agent is
// configured. nil means the assistant is on.
func (m Model) assistantOff() error {
	if m.ai != nil {
		return nil
	}
	return fmt.Errorf("assistant off: %s not found (config ai.agent)", m.agentName())
}

// --- drawing ---------------------------------------------------------------

// aiActKind is one act of the assistant panel's own: the actions a finished
// card draws under itself, or the head's Regenerate and Stop. These are not
// places to open but things to do.
type aiActKind int

const (
	actNone aiActKind = iota
	actInsert
	actInsertThread
	actCopy
	actRegenerate
	actStop
	actSend
	actReply
	actJump
	actRecall
	actStreamRetry
)

// aiAct names what an act runs on: the answer's turn, and which card of it
// (-1 for the acts that take the whole answer).
type aiAct struct {
	kind aiActKind
	turn string
	card int
}

// rebuild lays the turn list out for the width the pane has now. The head
// rows are the panel's own; a finished answer's cards are drawn through the
// composer preview's rendering, so what the reader checks against the chat
// and what Send would post cannot describe different messages. Actions
// appear only on finished answers: streaming text cannot move a target under
// the mouse.
func (p *aiPanel) rebuild(m Model) {
	w := m.rightWidth() - 2
	p.rows, p.rowAct = nil, nil
	s := p.session()
	if s == nil {
		return
	}
	st := m.msgStyleFor(w, msgMeta{})
	add := func(r msgRow, a aiAct) {
		p.rows = append(p.rows, r)
		p.rowAct = append(p.rowAct, a)
	}
	addAll := func(rs []msgRow, a aiAct) {
		for _, r := range rs {
			add(r, a)
		}
	}
	for i, t := range s.turns {
		last := i == len(s.turns)-1
		if key := t.keyOf(w, last, m.metaGen); t.cache.key == key && t.cache.rows != nil {
			p.rows = append(p.rows, t.cache.rows...)
			p.rowAct = append(p.rowAct, t.cache.acts...)
			continue
		}
		base := len(p.rows)
		none := aiAct{turn: t.id, card: -1}
		add(aiRowOf(m.aiTurnHead(t, w), w), none)
		addAll(plainRows(wrap(t.ask, w), w), none)
		add(m.aiAnswerHeadRow(t, w, last), none)
		for _, line := range t.traces {
			add(aiRowOf(stDim.Render(line), w), none)
		}
		// An answer that streams into the chat is the message itself: one
		// text, no reply blocks, and the chat side's own actions under it —
		// from the moment its card posts, asking or not.
		if t.stream != nil {
			if strings.TrimSpace(t.answer) == "" && t.state == aiAsking {
				add(aiRowOf(stDim.Render("…"), w), none)
			} else if t.state == aiFailed {
				add(aiRowOf(stErr.Render(truncate(t.err, w)), w), none)
			} else {
				var b block
				g := leads{b: &b}
				addAll(mdRows(t.answer, store.Message{}, 0, st, &g, mentions{}), none)
			}
			add(streamFoot(t, w), none)
			continue
		}
		switch t.state {
		case aiAsking:
			if strings.TrimSpace(t.answer) == "" {
				add(aiRowOf(stDim.Render("…"), w), none)
				continue
			}
			// Half an answer is commentary while it grows: the cards it
			// becomes are not there yet, and a card still being written is
			// not a target to send.
			var b block
			g := leads{b: &b}
			addAll(mdRows(t.answer, store.Message{}, 0, st, &g, mentions{}), none)
			continue
		case aiFailed:
			add(aiRowOf(stErr.Render(truncate(t.err, w)), w), none)
			continue
		case aiStopped, aiInterrupted:
			continue
		}
		cards := 0
		for _, seg := range ai.SplitAnswer(t.answer) {
			if !seg.Card {
				var b block
				g := leads{b: &b}
				addAll(mdRows(seg.Text, store.Message{}, 0, st, &g, mentions{}), none)
				continue
			}
			act := aiAct{turn: t.id, card: cards}
			cards++
			sentWhen := t.sentAt[act.card]
			add(aiRowOf(stDim.Render("┌─"), w), act)
			addAll(m.cardBodyRows(seg.Text, w), act)
			add(aiCardFoot(w, act, t.anchor != nil, !sentWhen.IsZero(), sentWhen), act)
		}
		// Only a finished turn's rows are worth the cache: the turns that
		// ended early draw a line or two, and the streaming tail moves with
		// every chunk anyway.
		t.cache = turnCache{key: t.keyOf(w, last, m.metaGen),
			rows: slices.Clone(p.rows[base:]), acts: slices.Clone(p.rowAct[base:])}
	}
	p.sel = clamp(p.sel, 0, max(0, len(p.rows)-1))
	if p.follow {
		p.toBottom(m.aiListHeight())
		p.sel = max(0, len(p.rows)-1)
	}
}

// aiAnswerHeadRow names an answer and the state it is in. The head of the
// last answer carries its own acts: Stop while it streams, Regenerate —
// Retry, on one that failed, was stopped or interrupted — once it has ended.
func (m Model) aiAnswerHeadRow(t *aiTurn, w int, last bool) msgRow {
	head := stBold.Render("AI " + t.at.Format("15:04"))
	state := ""
	var act aiActKind
	switch t.state {
	case aiAsking:
		state, act = "answering · x stops", actStop
	case aiFailed:
		state, act = "failed · Retry", actRegenerate
	case aiStopped:
		state, act = "stopped · Retry", actRegenerate
	case aiInterrupted:
		state, act = "interrupted · Retry", actRegenerate
	case aiDone:
		if last {
			state, act = "Regenerate", actRegenerate
		}
	}
	if state == "" {
		return aiRowOf(head, w)
	}
	row := aiRowOf(head+stDim.Render("  "+state), w)
	if act != actNone && last {
		x := lipgloss.Width(head) + 2
		row.zones = append(row.zones, clickZone{x0: x, x1: min(w, x+lipgloss.Width(state)),
			act: aiAct{kind: act, turn: t.id, card: -1}})
	}
	return row
}

// labelAct is one labelled act of a foot row.
type labelAct struct {
	label string
	act   aiAct
}

// footRow lays out the action row under a card: an optional head before the
// └─ lead, an optional prefix after it, then the acts separated by · — each
// its own click zone — and a dash tail to the width.
func footRow(w int, head, prefix string, parts []labelAct) msgRow {
	const lead = "└─ "
	x, line := lipgloss.Width(head+lead), lead
	if prefix != "" {
		line += prefix
		x += lipgloss.Width(prefix)
	}
	var zones []clickZone
	for _, p := range parts {
		if len(line) > len(lead) {
			line += " · "
			x += 3
		}
		line += p.label
		zones = append(zones, clickZone{x0: x, x1: x + len(p.label), act: p.act})
		x += len(p.label)
	}
	tail := strings.Repeat("─", max(1, w-lipgloss.Width(head+line)))
	row := aiRowOf(stDim.Render(head+line)+stDim.Render(tail), w)
	row.zones = zones
	return row
}

// aiCardFoot is the action row a finished card draws under itself: Send,
// Reply (when its question had an anchor), Insert, Copy — the client's own
// words — and the ✓ of a card that was sent, with the time it went at.
func aiCardFoot(w int, a aiAct, anchored, sent bool, at time.Time) msgRow {
	parts := []labelAct{{"Send", aiAct{kind: actSend, turn: a.turn, card: a.card}}}
	if anchored {
		parts = append(parts, labelAct{"Reply", aiAct{kind: actReply, turn: a.turn, card: a.card}})
	}
	parts = append(parts,
		labelAct{"Insert", aiAct{kind: actInsert, turn: a.turn, card: a.card}},
		labelAct{"Copy", aiAct{kind: actCopy, turn: a.turn, card: a.card}})
	prefix := ""
	if sent {
		prefix = "✓ sent " + at.Format("15:04")
	}
	return footRow(w, "", prefix, parts)
}

// streamFoot is the action row a streamed answer draws: where its card
// stands in the chat, and what can be done with it — Jump, Copy, Recall,
// Send when the first post never landed, Retry when a write failed. The
// client's own words, the panel's own acts.
func streamFoot(t *aiTurn, w int) msgRow {
	c := t.stream
	state := "● live in chat"
	if c.messageID == "" && c.err != "" {
		state = "not posted"
	} else if c.err != "" {
		state = "write failed"
	} else if c.closed {
		state = "sent"
	}
	var parts []labelAct
	if c.messageID == "" && c.err != "" {
		parts = append(parts, labelAct{"Send", aiAct{kind: actSend, turn: t.id, card: 0}})
	}
	if c.err != "" && c.messageID != "" {
		parts = append(parts, labelAct{"Retry", aiAct{kind: actStreamRetry, turn: t.id, card: -1}})
	}
	if c.messageID != "" {
		parts = append(parts, labelAct{"Jump", aiAct{kind: actJump, turn: t.id, card: -1}})
	}
	parts = append(parts, labelAct{"Copy", aiAct{kind: actCopy, turn: t.id, card: -1}})
	if c.messageID != "" {
		parts = append(parts, labelAct{"Recall", aiAct{kind: actRecall, turn: t.id, card: -1}})
	}
	return footRow(w, state+"  ", "", parts)
}

// cardBodyRows draws a card's text the way Send would post it: the composer
// preview's own rendering, bodyRows over the planned draft.
func (m Model) cardBodyRows(text string, w int) []msgRow {
	p, _ := m.files.planDraft(text)
	it := outboxItem{localID: "ai", chatID: cmp.Or(m.aiP.chat, m.chatID),
		msgType: p.kind.msgType(), body: p.body, images: p.uploads()}
	meta := msgMeta{suffix: m.meta.suffix, people: m.meta.people,
		avatars: m.meta.avatars, docs: m.meta.docs}
	resPending(&meta, []outboxItem{it})
	st := m.msgStyleFor(w, meta)
	var b block
	g := leads{b: &b}
	return bodyRows(it.message(m.deps.Self, m.selfName), 0, st, &g)
}

// cardsOf is a finished answer's cards, in order.
func cardsOf(t *aiTurn) []string {
	var out []string
	for _, seg := range ai.SplitAnswer(t.answer) {
		if seg.Card {
			out = append(out, seg.Text)
		}
	}
	return out
}

// cursorAct is the act the cursor row belongs to: the card its rows make up,
// or the head's own acts.
func (p *aiPanel) cursorAct() aiAct {
	if p.sel >= 0 && p.sel < len(p.rowAct) {
		return p.rowAct[p.sel]
	}
	return aiAct{card: -1}
}

// aiMove walks the turn list by rows and keeps the cursor on screen.
func (m *Model) aiMove(n int) {
	p := m.aiP
	if len(p.rows) == 0 {
		return
	}
	p.follow = false
	p.sel = clamp(p.sel+n, 0, len(p.rows)-1)
	h := m.aiListHeight()
	if p.sel < p.top {
		p.top = p.sel
	}
	if p.sel >= p.top+h {
		p.top = p.sel - h + 1
	}
	p.top = clamp(p.top, 0, max(0, len(p.rows)-h))
}

// cardText is the card an act names. A streamed answer is one text with no
// blocks, so its Copy takes the whole of it.
func (m Model) cardText(a aiAct) (string, bool) {
	_, t := m.aiP.findTurn(a.turn)
	if t == nil {
		return "", false
	}
	if t.stream != nil {
		return t.answer, true
	}
	if a.card < 0 {
		return "", false
	}
	cards := cardsOf(t)
	if a.card >= len(cards) {
		return "", false
	}
	return cards[a.card], true
}

// insertCard fills a composer with a card's text. The frame's box when the
// anchor's thread is the frame under the panel; otherwise the chat's box,
// quoting the anchor — inside its thread when it is in one, or whenever the
// reader asked for a thread with R. A box that carried the question's draft
// chip is swapped (Replace); an empty one is filled; a written one gets the
// text at the cursor.
func (m Model) insertCard(a aiAct, inThread bool) (tea.Model, tea.Cmd) {
	text, ok := m.cardText(a)
	if !ok {
		return m.notify("no card here — j/k walks the cards", true), nil
	}
	_, t := m.aiP.findTurn(a.turn)
	anchor := cloneMsg(t.anchor)
	if anchor != nil && anchor.ThreadID != "" {
		inThread = true
	}
	// The frame under the panel owns the box when the anchor belongs to its
	// conversation — a reply of its thread, or its own root.
	frameBox := m.threadOpen() && anchor != nil &&
		(anchor.ThreadID == m.threadID || indexOfID(m.thread, anchor.MessageID) >= 0)
	m = m.closeAI()
	var closeFrame tea.Cmd
	if !frameBox && m.foldRight() {
		// The chat's box is under the folded column; the panel is gone and
		// the frame under it goes too, so the box the text lands in is on
		// screen.
		closeFrame = m.closeRight()
	}
	next, cmd := m.startInsert(anchor, inThread)
	m = next.(Model)
	switch {
	case strings.TrimSpace(t.compose) != "":
		m.areap().SetValue(text)
	case strings.TrimSpace(m.areap().Value()) == "":
		m.areap().SetValue(text)
	default:
		m.areap().InsertString(text)
	}
	m.replan()
	m.layout()
	return m.notify("card in the composer: i to edit, Enter to send", false), tea.Batch(cmd, closeFrame)
}

// copyCard puts a card's text on the clipboard.
func (m Model) copyCard(a aiAct) (tea.Model, tea.Cmd) {
	text, ok := m.cardText(a)
	if !ok {
		return m.notify("no card here", true), nil
	}
	return m.notify("card copied", false), tea.SetClipboard(text)
}

// aiSendPending is one card waiting on its y/n. file says its text names a
// local file, which turns n into "send without the upload" rather than
// "don't send".
type aiSendPending struct {
	act   aiAct
	reply bool
	file  bool
}

// askSendAI arms the y/n a card's send asks for: writing to Feishu is the one
// act here that other people see. The prompt names where the text goes and,
// for a reply, to whom, shows its first line, and says when it mentions @All
// or names a local file.
func (m Model) askSendAI(a aiAct, reply bool) (tea.Model, tea.Cmd, bool) {
	text, ok := m.cardText(a)
	if !ok {
		return m.notify("no card here", true), nil, true
	}
	_, t := m.aiP.findTurn(a.turn)
	if reply && t.anchor == nil {
		return m.notify("that question had no message to reply to", true), nil, true
	}
	plan, err := m.files.planDraft(text)
	if err != nil {
		return m.notify(err.Error(), true), nil, true
	}
	var asks []string
	if reply {
		asks = append(asks, "reply to "+displaySender(*t.anchor, m.deps.Self, m.suffixOf(t.anchor.SenderID)))
	} else {
		name, _ := m.chatName(m.aiP.chat)
		asks = append(asks, "send to "+cmp.Or(name, m.aiP.chat))
	}
	asks = append(asks, firstDisplayLine(text))
	if strings.Contains(text, allName) {
		asks = append(asks, "mentions @All")
	}
	if plan.kind == kindFile {
		asks = append(asks, "file "+cmp.Or(plan.file.key, filepath.Base(plan.file.local)))
	}
	// A name the roster cannot answer for stays text, and the reader should
	// know it will: the destination is not the open chat.
	if strings.Contains(text, "@") && m.aiP.chat != m.chatID {
		asks = append(asks, "@names stay text")
	}
	m.confirm = confirmation{kind: confirmAISend,
		aiSend: &aiSendPending{act: a, reply: reply, file: plan.kind == kindFile}}
	return m.notify(strings.Join(asks, " · ")+"? y/n", false), nil, true
}

// aiSendCard puts a card on the wire through the same path a typed send
// takes: the same conversion of the text, the same pending bubble, the same
// . and x when it fails. withFile is the y/n choice when the text named a
// local file: y sends it with the upload, n leaves the reference as text.
func (m Model) aiSendCard(p aiSendPending, withFile bool) (tea.Model, tea.Cmd) {
	a := p.act
	text, ok := m.cardText(a)
	if !ok {
		return m.notify("no card here", true), nil
	}
	_, t := m.aiP.findTurn(a.turn)
	dest, replyTo, inThread, threadID := m.aiP.chat, "", false, ""
	if p.reply {
		anchor := t.anchor
		dest, replyTo = anchor.ChatID, anchor.MessageID
		inThread, threadID = anchor.ThreadID != "", anchor.ThreadID
	}
	plan, err := m.files.planDraft(text)
	if err != nil {
		return m.notify(err.Error(), true), nil
	}
	if !withFile {
		// No upload: the reference stays the text the reader can read.
		plan = draftPlan{kind: kindText, body: text, send: larkcli.Text(text)}
	}
	// Names become tags only for the open chat, whose roster is the one
	// loaded; anywhere else the name stays text.
	if dest == m.chatID {
		plan.send = m.tagMentions(plan.send)
	}
	it := outboxItem{localID: uuid.New().String(), chatID: dest,
		replyTo: replyTo, inThread: inThread, threadID: threadID,
		msgType: plan.kind.msgType(), send: plan.send, body: plan.body,
		images: plan.uploads(), file: plan.file, createMs: time.Now().UnixMilli()}
	cmd := m.sendItem(it)
	m.enqueue(it)
	m.refreshPanes()
	if m.aiP.sending == nil {
		m.aiP.sending = map[string]aiAct{}
	}
	m.aiP.sending[it.localID] = a
	m.aiP.rebuild(m)
	m.layout()
	if m.aiOpen() {
		return m.notify("sending…", false), cmd
	}
	return m.notify("", false), cmd
}

// markAISent is the ✓ an outbox send earns its card: called when the wire
// answered for one of the panel's sends.
func (m *Model) markAISent(localID string) {
	a, ok := m.aiP.sending[localID]
	if !ok {
		return
	}
	delete(m.aiP.sending, localID)
	_, t := m.aiP.findTurn(a.turn)
	if t == nil {
		return
	}
	if t.sentAt == nil {
		t.sentAt = map[int]time.Time{}
	}
	t.sentAt[a.card] = time.Now()
	m.aiP.rebuild(*m)
}

// copyAnswer copies the whole answer the cursor stands in — or the session's
// last, when it stands in none — as the Markdown the agent wrote.
func (m Model) copyAnswer() (tea.Model, tea.Cmd, bool) {
	a := m.aiP.cursorAct()
	_, t := m.aiP.findTurn(a.turn)
	if t == nil {
		if s := m.aiP.session(); s != nil && len(s.turns) > 0 {
			t = s.turns[len(s.turns)-1]
		}
	}
	if t == nil || strings.TrimSpace(t.answer) == "" {
		return m.notify("nothing to copy", true), nil, true
	}
	return m.notify("answer copied", false), tea.SetClipboard(t.answer), true
}

// regenerateAI re-asks the session's last question from its own record: the
// window is cut at the time it was asked, so the rebuilt prompt is the one
// that built the answer, not the one the chat has moved on to. Regenerate on
// a finished answer and Retry on a failed, stopped or interrupted one are
// the same act.
func (m Model) regenerateAI() (tea.Model, tea.Cmd) {
	p := m.aiP
	s := p.session()
	if s == nil || len(s.turns) == 0 {
		return m.notify("no answer to redo", true), nil
	}
	t := s.turns[len(s.turns)-1]
	if t.streaming() {
		return m.notify("answering · x stops", true), nil
	}
	if t.stream != nil {
		return m.notify("a streamed answer is a message now — ask again with ctrl+s", true), nil
	}
	// A panel with no agent still takes the retry: the turn fails with the
	// notice rather than the key refusing to answer.
	off := m.assistantOff()
	t.answer, t.err, t.state, t.ch, t.cancel, t.traces = "", "", aiAsking, nil, nil, nil
	t.sentAt = nil
	p.follow = true
	p.rebuild(m)
	m.layout()
	asking := askTurn(m.deps, m.ai, off, p, s, t, m.aiHistory())
	return m.notify("asking "+m.agentName()+"…", false),
		tea.Batch(saveTurnCmd(m.deps, s, t), asking)
}

// aiPress runs one of the panel's acts, by mouse or by key.
func (m Model) aiPress(a aiAct) (tea.Model, tea.Cmd) {
	switch a.kind {
	case actInsert:
		return m.insertCard(a, false)
	case actInsertThread:
		return m.insertCard(a, true)
	case actCopy:
		return m.copyCard(a)
	case actSend:
		next, cmd, _ := m.askSendAI(a, false)
		return next, cmd
	case actReply:
		next, cmd, _ := m.askSendAI(a, true)
		return next, cmd
	case actRegenerate:
		return m.regenerateAI()
	case actStop:
		return m.stopAI()
	case actJump:
		_, t := m.aiP.findTurn(a.turn)
		if t == nil || t.stream == nil || t.stream.messageID == "" {
			return m, nil
		}
		return m, openInFeishu(m.deps, t.stream.chatID, t.stream.messageID, 0)
	case actRecall:
		_, t := m.aiP.findTurn(a.turn)
		if t == nil || t.stream == nil || t.stream.messageID == "" {
			return m, nil
		}
		// Recall is recall: the confirmation the messages pane's D runs, over
		// a message that is as visible to the chat as any other.
		m.confirm = confirmation{kind: confirmRecall, messageID: t.stream.messageID}
		return m.notify("recall the streamed card? y/n", false), nil
	case actStreamRetry:
		_, t := m.aiP.findTurn(a.turn)
		if t == nil {
			return m, nil
		}
		return m.retryStreamCard(t)
	}
	return m, nil
}

// openCardLink opens the first link the cursor row carries: a card's text is
// the same rendering a post gets, and its links lead the same places.
func (m Model) openCardLink() (tea.Model, tea.Cmd, bool) {
	if m.aiP.sel >= 0 && m.aiP.sel < len(m.aiP.rows) {
		for _, z := range m.aiP.rows[m.aiP.sel].zones {
			if len(z.urls) > 0 {
				return m, openZone(m.deps, z), true
			}
		}
	}
	return m.notify("no link here", true), nil, true
}

// toBottom puts the last rows on screen.
func (p *aiPanel) toBottom(h int) {
	p.top = max(0, len(p.rows)-h)
}

// scroll moves the viewport and gives up the follow the moment the reader
// scrolls anywhere but down at the tail.
func (p *aiPanel) scroll(n, h int) {
	p.top = clamp(p.top+n, 0, max(0, len(p.rows)-h))
	p.follow = p.top >= max(0, len(p.rows)-h)
}

// plainRows fits lines that carry no message of their own.
func plainRows(lines []string, w int) []msgRow {
	out := make([]msgRow, 0, len(lines))
	for _, l := range lines {
		out = append(out, aiRowOf(l, w))
	}
	return out
}

// aiRowOf is one such line.
func aiRowOf(l string, w int) msgRow { return msgRow{text: fit(l, w), plain: true} }

// aiTurnHead names a question, when it was asked and what it carried.
func (m Model) aiTurnHead(t *aiTurn, w int) string {
	head := stBold.Render("You " + t.at.Format("15:04"))
	chips := m.aiTurnChips(t, w-lipgloss.Width(head)-3)
	if chips == "" {
		return head
	}
	return head + stDim.Render("  "+chips)
}

// aiTurnChips is the marker line a question leaves behind, spelling what it
// was asked about now that the strip above has moved on.
func (m Model) aiTurnChips(t *aiTurn, w int) string {
	var chips []string
	if t.anchor != nil {
		chips = append(chips, "↩ "+displaySender(*t.anchor, m.deps.Self, m.suffixOf(t.anchor.SenderID)))
	}
	if n := len(t.sel); n > 0 {
		chips = append(chips, fmt.Sprintf("%d selected", n))
	}
	if strings.TrimSpace(t.compose) != "" {
		chips = append(chips, "✎ draft")
	}
	chips = append(chips, "▤ "+plural(max(1, t.window), "msg", "msgs"))
	return truncate(strings.Join(chips, " · "), w)
}

// aiHeader is the pane's title: the chat's sessions as tabs, the one on
// screen bold, an answering one marked, and + for a new one.
func (p *aiPanel) header(m Model, w int) string {
	var b strings.Builder
	b.WriteString(stBold.Render("AI"))
	for i, s := range p.sess {
		name := " " + truncate(cmp.Or(s.title, "new"), 12)
		busy := ""
		if n := len(s.turns); n > 0 && s.turns[n-1].streaming() {
			busy = " …"
		}
		if i == p.cur {
			b.WriteString(stBold.Render(name + busy))
			continue
		}
		b.WriteString(stDim.Render(name))
	}
	if len(p.sess) > 1 {
		b.WriteString(stDim.Render(fmt.Sprintf("  %d/%d", p.cur+1, len(p.sess))))
	}
	b.WriteString(stDim.Render("  +"))
	return fit(b.String(), w)
}

// aiChips is the strip of context the next question carries, under the header
// where the conversation it belongs to is always in view.
func (m Model) aiChips(w int) string {
	p := m.aiP
	var chips []string
	if p.anchor != nil {
		who := displaySender(*p.anchor, m.deps.Self, m.suffixOf(p.anchor.SenderID))
		chips = append(chips, "↩ "+who+": "+truncate(replyGist(*p.anchor), max(8, w-24)))
	}
	if n := len(p.selection); n > 0 {
		chips = append(chips, "☰ "+fmt.Sprintf("%d selected", n))
	}
	if strings.TrimSpace(m.aiDraftText()) != "" {
		chips = append(chips, "✎ draft")
	}
	if m.cfg.AI.History {
		chips = append(chips, "⌕ history")
	}
	chips = append(chips, fmt.Sprintf("▤ last %d", max(1, m.cfg.AI.Context)))
	return fit(stDim.Render(truncate(strings.Join(chips, " · "), w)), w)
}

// renderAI draws the column: header, the context strip, the turn list.
func (p *aiPanel) renderAI(m Model, h int) string {
	w := m.rightWidth() - 2
	lines := make([]string, 0, h)
	lines = append(lines, p.header(m, w), m.aiChips(w), paneRule(w))
	for i := p.top; i < len(p.rows) && len(lines) < h; i++ {
		line, _ := m.rowLine(p.rows[i], w)
		lines = append(lines, line)
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneThread).Height(h).Render(strings.Join(lines, "\n"))
}

// --- keys ------------------------------------------------------------------

// aiChat is the chat a question from here belongs to: the one under the
// Unread cursor there, the one highlighted in the list, the one a page is on
// its way to, else the open one.
func (m Model) aiChat() string {
	switch {
	case m.feed != nil:
		return m.feedChatAt(m.msgIdx)
	case m.focus == paneChats:
		vis := m.visibleRows()
		if len(vis) > 0 && !vis[m.chatIdx].isFeed() {
			return vis[m.chatIdx].chatID()
		}
	}
	return cmp.Or(m.pendingChat, m.chatID)
}

// openAIKey is the a key: open the panel on this chat, with the keys already
// in its box and the anchor taken from where the reader stands. Nothing is
// asked; asking is Enter.
func (m Model) openAIKey(fresh bool) (tea.Model, tea.Cmd) {
	// The anchor is read before the panel takes the focus, because the focus
	// is part of what names it.
	anchor, selection := m.aiContext()
	chat := m.aiChat()
	// From the chats pane the panel belongs to the highlighted chat, whose
	// page opens under it the way Enter would open it.
	var openPage tea.Cmd
	if m.focus == paneChats && chat != "" && chat != m.chatID && m.chatID != "" {
		openPage = m.openChat(chat)
	}
	next, cmd := m.openAI(chat, fresh)
	if next.aiP == nil {
		// No chat to open on: openAI answered with a notice, and there is no
		// panel to anchor or hand the keys to.
		return next, cmd
	}
	next.aiP.anchor, next.aiP.selection = anchor, selection
	out, enter := next.enterAI()
	return out, tea.Batch(cmd, enter, openPage)
}

// openAISelection is a in VISUAL: the range names the context, and the panel
// opens on it with the keys.
func (m Model) openAISelection() (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		return m.notify("open a chat first", true), nil
	}
	list := m.focusedList()
	lo, hi := m.selectionRange()
	if lo < 0 || hi >= len(list) {
		return m.notify("nothing selected", true), nil
	}
	sel := make([]string, 0, hi-lo+1)
	for _, x := range list[lo : hi+1] {
		sel = append(sel, x.MessageID)
	}
	m.mode = modeNormal
	next, cmd := m.openAI(m.chatID, false)
	next.aiP.anchor, next.aiP.selection = nil, sel
	out, enter := next.enterAI()
	return out, tea.Batch(cmd, enter)
}

// askCommand is :ai. Alone it opens the panel; with an argument it starts a
// new session and asks at once. A snippet's name asks the snippet, and
// anything else is the question as typed.
func (m Model) askCommand(input string) (tea.Model, tea.Cmd) {
	input = strings.TrimSpace(input)
	if input == "" {
		return m.openAIKey(false)
	}
	if m.aiChat() == "" {
		return m.notify("open a chat first", true), nil
	}
	sent := ""
	for _, sn := range m.snippets() {
		if strings.EqualFold(sn.Name, input) {
			input, sent = sn.Name, sn.Text
			break
		}
	}
	next, cmd := m.openAI(m.aiChat(), true)
	out, ask := next.askAI(input, sent, false)
	return out, tea.Batch(cmd, ask)
}

// askAI turns a question into a turn of the session on screen and starts it.
// sent is what the model is asked when it differs from the question shown,
// which is a snippet's text under the name the reader typed. intoChat asks
// with the answer streaming into the chat as one card.
func (m Model) askAI(ask, sent string, intoChat bool) (tea.Model, tea.Cmd) {
	p := m.aiP
	if ask == "" {
		return m, nil
	}
	if p.chat == "" {
		return m.notify("open a chat first", true), nil
	}
	if p.busy() {
		return m.notify("assistant is still answering", true), nil
	}
	if p.session() == nil {
		p.sess = append(p.sess, &aiSession{id: uuid.New().String(), created: time.Now().UnixMilli()})
		p.cur = len(p.sess) - 1
	}
	s := p.session()
	if s.title == "" {
		s.title = firstDisplayLine(ask)
	}
	if sent == "" {
		sent = ask
	}
	// A panel with no agent still takes the question: the turn fails with the
	// notice rather than the pane refusing to open.
	off := m.assistantOff()
	anchor := cloneMsg(p.anchor)
	t := &aiTurn{id: uuid.New().String(), ask: ask, sent: sent, seq: len(s.turns),
		anchorID: msgIDOf(anchor), anchor: anchor, thread: m.threadID, window: m.cfg.AI.Context,
		compose: m.aiDraftText(), sel: slices.Clone(p.selection), at: time.Now()}
	if intoChat {
		t.stream = &aiStreamCard{chatID: p.chat, replyTo: msgIDOf(anchor),
			inThread: anchor != nil && anchor.ThreadID != ""}
	}
	if s.created == 0 {
		s.created = t.at.UnixMilli()
	}
	s.turns = append(s.turns, t)
	p.input.Reset()
	p.follow = true
	p.rebuild(m)
	m.layout()
	asking := askTurn(m.deps, m.ai, off, p, s, t, m.aiHistory())
	return m.notify("asking "+m.agentName()+"…", false),
		tea.Batch(saveSessionCmd(m.deps, p.chat, s), saveTurnCmd(m.deps, s, t), asking)
}

// aiHistory is the reading reach the next ask carries: the chat it is about
// and the config path its commands spell, no chat when history is off. It is
// read off the session's cfg rather than deps because :set retunes it live.
func (m Model) aiHistory() ai.History {
	if !m.cfg.AI.History || m.aiP.chat == "" {
		return ai.History{}
	}
	return ai.History{ChatID: m.aiP.chat, ConfigPath: m.deps.ConfigPath}
}

// threadIDOf names the thread a message belongs to, ” for none.
func threadIDOf(x *store.Message) string {
	if x == nil {
		return ""
	}
	return x.ThreadID
}

// msgIDOf names a message a turn records, ” for none.
func msgIDOf(x *store.Message) string {
	if x == nil {
		return ""
	}
	return x.MessageID
}

// askDeleteAI arms the y/n a session's deletion asks for: the conversation
// and its questions go together, and there is no undo to reach them with.
func (m Model) askDeleteAI() (tea.Model, tea.Cmd, bool) {
	s := m.aiP.session()
	if s == nil {
		return m.notify("no session to delete", true), nil, true
	}
	m.confirm = confirmation{kind: confirmDeleteAI, aiSession: s.id}
	return m.notify("delete "+cmp.Or(s.title, "this session")+"? y/n", false), nil, true
}

// deleteAI drops a session: out of the panel's lists, and out of the store
// with its turns. An answer still streaming into it is stopped first —
// nothing keeps a child process alive past its conversation.
func (m Model) deleteAI(id string) (tea.Model, tea.Cmd) {
	p := m.aiP
	var doomed *aiSession
	for _, s := range p.sess {
		if s.id == id {
			doomed = s
			break
		}
	}
	if doomed == nil {
		return m, nil
	}
	for _, t := range doomed.turns {
		if t.cancel != nil {
			t.cancel()
		}
	}
	p.sess = slices.DeleteFunc(p.sess, func(s *aiSession) bool { return s.id == id })
	if len(p.sess) == 0 {
		p.sess = append(p.sess, &aiSession{id: uuid.New().String(), created: time.Now().UnixMilli()})
	}
	p.cur = min(p.cur, len(p.sess)-1)
	p.top, p.follow = 0, true
	p.rebuild(m)
	m.layout()
	return m.notify("session deleted", false), func() tea.Msg {
		if err := m.deps.Store.DeleteAISession(context.Background(), id); err != nil {
			m.deps.log().Error("delete ai session", "id", id, "err", err)
		}
		return nil
	}
}

// onAIKey takes the keys the assistant column owns, before any of them can
// reach the frame hidden under the panel: a message nobody can see is not a
// message to act on. It answers took only for keys it owns; the ones every
// pane shares — panes, help, Esc's ladder — fall through untouched. The
// panel's list is its own, so its movement keys are too.
func (m Model) onAIKey(s string) (Model, tea.Cmd, bool) {
	p := m.aiP
	// gg is the panel's own pair while it owns the keys.
	if p.pG {
		p.pG = false
		if s == "g" {
			m.aiMove(-len(p.rows))
			return m, nil, true
		}
	}
	// The y prefix armed here resolves on the panel's own objects: y card.
	if m.pendingY {
		m.pendingY = false
		if s == "y" {
			next, cmd := m.copyCard(p.cursorAct())
			return next.(Model), cmd, true
		}
	}
	switch s {
	case "i":
		next, cmd := m.enterAI()
		return next.(Model), cmd, true
	case "enter", "r":
		return m.insertAtCursor(false)
	case "R":
		return m.insertAtCursor(true)
	case "a":
		next, cmd := m.openAIKey(false)
		return next.(Model), cmd, true
	case "A":
		next, cmd := m.openAI(m.aiP.chat, true)
		out, enter := next.enterAI()
		return out.(Model), tea.Batch(cmd, enter), true
	case "[":
		m.switchAI(-1)
		return m, nil, true
	case "]":
		m.switchAI(1)
		return m, nil, true
	case "x":
		next, cmd := m.stopAI()
		return next.(Model), cmd, true
	case "s":
		next, cmd, _ := m.askSendAI(p.cursorAct(), false)
		return next.(Model), cmd, true
	case "S":
		next, cmd, _ := m.askSendAI(p.cursorAct(), true)
		return next.(Model), cmd, true
	case ".":
		next, cmd := m.regenerateAI()
		return next.(Model), cmd, true
	case "y":
		m.pendingY = true
		return m.notify("y… y card", false), nil, true
	case "Y":
		next, cmd, _ := m.copyAnswer()
		return next.(Model), cmd, true
	case "j", "down":
		m.aiMove(1)
		return m, nil, true
	case "k", "up":
		m.aiMove(-1)
		return m, nil, true
	case "ctrl+d":
		m.aiMove(m.listHeight() / 2)
		return m, nil, true
	case "ctrl+u":
		m.aiMove(-m.listHeight() / 2)
		return m, nil, true
	case "g":
		p.pG = true
		return m, nil, true
	case "G", "end":
		m.aiMove(len(p.rows))
		return m, nil, true
	case "h", "left":
		// The column keeps its frame; h steps to the messages pane rather
		// than backing out of a stack that is not even on screen.
		m.focus = paneMessages
		return m, nil, true
	case "o":
		next, cmd, _ := m.openCardLink()
		return next.(Model), cmd, true
	case "D":
		next, cmd, _ := m.askDeleteAI()
		return next.(Model), cmd, true
	case "e", "f", "C", "E", "t", "v", "0":
		// These act on a message, and no message is on screen here: the frame
		// under the panel keeps its own until Esc uncovers it.
		return m.notify("no message selected here — Esc uncovers the frame beneath", true), nil, true
	}
	// The digits reach the snippet offers, the same chips the band draws.
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		next, cmd := m.insertSnippet(int(s[0] - '1'))
		return next.(Model), cmd, true
	}
	return m, nil, false
}

// insertAtCursor is Enter, r and R on the card the cursor stands in.
func (m Model) insertAtCursor(inThread bool) (Model, tea.Cmd, bool) {
	a := m.aiP.cursorAct()
	if a.card < 0 {
		return m.notify("no card here — j/k walks the cards", true), nil, true
	}
	next, cmd := m.insertCard(a, inThread)
	return next.(Model), cmd, true
}

// snippets is what the panel offers: the config's list when it is set, else
// the built-ins.
func (m Model) snippets() []ai.Snippet {
	if len(m.cfg.AI.Snippets) > 0 {
		out := make([]ai.Snippet, len(m.cfg.AI.Snippets))
		for i, s := range m.cfg.AI.Snippets {
			out[i] = ai.Snippet{Name: s.Name, Text: s.Text}
		}
		return out
	}
	return ai.BuiltinSnippets()
}

// snippetNames is the offers' names, in order — what :ai's completion lists.
func (m Model) snippetNames() []string {
	sn := m.snippets()
	out := make([]string, len(sn))
	for i, s := range sn {
		out[i] = strings.ToLower(s.Name)
	}
	return out
}

// insertSnippet drops a snippet's text in the panel's box and puts the keys
// there. Inserting is never asking: Enter is what asks.
func (m Model) insertSnippet(i int) (tea.Model, tea.Cmd) {
	sn := m.snippets()
	if i < 0 || i >= len(sn) || i >= 9 {
		return m.notify("no snippet under that digit", true), nil
	}
	m.aiP.input.SetValue(sn[i].Text)
	return m.enterAI()
}

// aiChip is one snippet chip of the AI band's badge row: its label — the
// digit that reaches it and the snippet's name — and the columns it is drawn
// over.
type aiChip struct {
	idx    int
	label  string
	x0, x1 int
}

// aiChipRow lays the chips out at the width the AI band has. The row the
// reader sees and the click target the mouse answers come from the same
// layout, so they cannot disagree.
func (m Model) aiChipRow(w int) []aiChip {
	room := w - lipgloss.Width(snippetHint) - 2
	var out []aiChip
	x := 0
	for i, sn := range m.snippets() {
		if i >= 9 {
			break
		}
		chip := fmt.Sprintf("%d %s", i+1, sn.Name)
		if x > 0 {
			x += 2
		}
		if x+lipgloss.Width(chip) > room {
			break
		}
		out = append(out, aiChip{idx: i, label: chip, x0: x, x1: x + lipgloss.Width(chip)})
		x += lipgloss.Width(chip)
	}
	return out
}

// snippetHint names the other way in, beside the chips that fit.
const snippetHint = "/ snippets · Enter ask"

// renderSnippetRow is the AI band's badge row: the panel's snippet offers
// under their digits, as far as they fit — the rest are reached with /.
func (m Model) renderSnippetRow(w int) string {
	chips := m.aiChipRow(w)
	labels := make([]string, len(chips))
	for i, c := range chips {
		labels[i] = c.label
	}
	return padBetween(strings.Join(labels, "  "), stDim.Render(snippetHint), w)
}

// aiChipAt names the snippet chip at column x of the badge row's content, -1
// for none.
func (m Model) aiChipAt(x int) int {
	for _, c := range m.aiChipRow(m.bandWidth(sideAI) - 2) {
		if x >= c.x0 && x < c.x1 {
			return c.idx
		}
	}
	return -1
}

// --- stream to chat --------------------------------------------------------

// aiStreamCard is the chat side of an answer asked with ctrl+s: one card,
// posted at once and rewritten whole as the answer grows. The writer keeps
// one write in flight per card — Feishu has no sequence for concurrent
// rewrites of one message, and meters them per message besides — so each
// write carries the latest text and nothing older.
type aiStreamCard struct {
	chatID   string
	replyTo  string
	inThread bool
	// messageID is the card once the first post answered; until then nothing
	// of this answer is on the wire.
	messageID string
	// wrote is how much of the answer the card holds, busy whether a write
	// is in flight, closed whether the card is finished.
	wrote  int
	busy   bool
	closed bool
	// err is the write that failed, empty while the card keeps up. A first
	// post that failed leaves the answer in the panel marked not posted; a
	// rewrite that failed leaves it open at what it last held. Either way
	// Retry runs the write again.
	err string
}

// streamCardMax is where a streamed card closes: Feishu refuses a content
// past 30 KB, and an answer longer than that is not one message anymore. The
// budget is measured on the encoded card the wire carries, not the markdown
// it is cut from: escaping inflates the payload past the bytes of the text.
const streamCardMax = 30 << 10

// streamCardNote closes a card cut at the cap, and streamInterrupted marks an
// answer that never finished. Nothing is left half-written without saying so.
const (
	streamCardNote = "\n\n(the rest is in larkim)"
	streamStopped  = "\n\n(interrupted)"
)

// streamCardText is what the card holds for the answer as it stands: the text
// so far while it streams, the final text with its marker once it has ended.
// A text whose card would pass the cap is cut on rune boundaries until the
// card fits — a card that ends mid-character is a message nobody can read
// the tail of.
func streamCardText(t *aiTurn) string {
	text, tail := t.answer, ""
	if !t.streaming() && t.state != aiDone {
		tail = streamStopped
	}
	if cardLen(text+tail) <= streamCardMax {
		return text + tail
	}
	// Encoding only grows with the text, so the largest fitting prefix can
	// be searched for instead of walked.
	runes := []rune(text)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if cardLen(string(runes[:mid])+streamCardNote+tail) <= streamCardMax {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo]) + streamCardNote + tail
}

// cardLen is the size of the card the wire carries for text.
func cardLen(text string) int { return len(larkcli.Card(text).Card) }

// askStreamConfirm arms the y/n streaming into the chat asks for: the card is
// on the wire the moment the answer starts growing, which no composer step
// gates, and without an anchor it opens the chat as a new message. The keys
// step out of the box to answer it — a y typed on is a letter, not an answer.
func (m Model) askStreamConfirm() (tea.Model, tea.Cmd) {
	q := strings.TrimSpace(m.aiP.input.Value())
	if q == "" {
		return m.notify("type the question first", true), nil
	}
	if p := m.aiP; p.chat == "" {
		return m.notify("open a chat first", true), nil
	} else if p.busy() {
		return m.notify("assistant is still answering", true), nil
	}
	name, _ := m.chatName(m.aiP.chat)
	asks := []string{"stream the answer into " + cmp.Or(name, m.aiP.chat)}
	if a := m.aiP.anchor; a != nil {
		asks = append(asks, "as a reply to "+displaySender(*a, m.deps.Self, m.suffixOf(a.SenderID)))
	}
	m.mode = modeNormal
	m.focus = paneThread
	m.areap().Blur()
	m.confirm = confirmation{kind: confirmAIStream, aiStream: q}
	return m.notify(strings.Join(asks, " ")+"? y/n", false), nil
}

// streamWrittenMsg answers one write of a streamed card: how much of the
// answer the card now holds, the message it is, or the failure that kept it
// from holding more.
type streamWrittenMsg struct {
	turn      string
	wrote     int
	messageID string
	err       error
}

// writeStreamCard puts text on the card: the first post when the card has no
// message yet, a whole-card rewrite after.
func writeStreamCard(d Deps, t *aiTurn, text string) tea.Cmd {
	c := t.stream
	card, id := larkcli.Card(text), t.id
	var post func(ctx context.Context) (larkcli.SentMessage, error)
	if c.replyTo != "" {
		replyTo, inThread := c.replyTo, c.inThread
		post = func(ctx context.Context) (larkcli.SentMessage, error) {
			return d.Client.Reply(ctx, replyTo, card, inThread, "")
		}
	} else {
		chatID := c.chatID
		post = func(ctx context.Context) (larkcli.SentMessage, error) {
			return d.Client.Send(ctx, larkcli.Target{ChatID: chatID}, card, "")
		}
	}
	patchID := c.messageID
	return func() tea.Msg {
		// The card's own lane: rewrites are serial per message by the API's
		// own accounting, and a line of width one cannot outrun itself.
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		ctx = larkcli.WithLane(ctx, larkcli.LaneCard)
		if patchID != "" {
			if err := d.Client.PatchMessage(ctx, patchID, card.Card); err != nil {
				return streamWrittenMsg{turn: id, err: err}
			}
			return streamWrittenMsg{turn: id, wrote: len(text), messageID: patchID}
		}
		sent, err := post(ctx)
		if err != nil {
			return streamWrittenMsg{turn: id, err: err}
		}
		return streamWrittenMsg{turn: id, wrote: len(text), messageID: sent.MessageID}
	}
}

// pumpStreamCard starts the next card write when one is due: nothing in
// flight, no failure waiting on a Retry, and text the card does not hold yet.
func (m *Model) pumpStreamCard(t *aiTurn) tea.Cmd {
	c := t.stream
	if c == nil || c.busy || c.closed || c.err != "" {
		return nil
	}
	text := streamCardText(t)
	if c.wrote == len(text) {
		return nil
	}
	c.busy = true
	return writeStreamCard(m.deps, t, text)
}

// onStreamWritten takes a card write's answer. The turn's card closes when
// the write that carries its final text lands; a failure leaves it at what it
// last held and says so.
func (m Model) onStreamWritten(msg streamWrittenMsg) (tea.Model, tea.Cmd) {
	s, t := m.aiP.findTurn(msg.turn)
	if t == nil || t.stream == nil {
		return m, nil
	}
	c := t.stream
	c.busy = false
	if msg.err != nil {
		c.err = msg.err.Error()
		if m.aiOpen() && m.aiP.chat == m.chatID {
			m.aiP.rebuild(m)
		}
		return m.notify("card write failed: "+c.err, true), nil
	}
	// The first post's answer is the card_id the turn row owes the store: the
	// done-time save has usually gone out before the round trip came back,
	// and without this write a restart loses the card the chat is holding.
	firstCard := c.messageID == "" && msg.messageID != ""
	c.messageID, c.wrote = msg.messageID, msg.wrote
	if !t.streaming() && c.wrote == len(streamCardText(t)) {
		c.closed = true
	}
	cmd := m.pumpStreamCard(t)
	if firstCard {
		cmd = tea.Batch(cmd, saveTurnCmd(m.deps, s, t))
	}
	if m.aiOpen() && m.aiP.chat == m.chatID {
		m.aiP.rebuild(m)
	}
	return m, cmd
}

// retryStreamCard runs the write that failed again: the first post, or the
// rewrite the card last held.
func (m Model) retryStreamCard(t *aiTurn) (tea.Model, tea.Cmd) {
	c := t.stream
	if c == nil || c.busy || c.err == "" {
		return m, nil
	}
	c.err = ""
	cmd := m.pumpStreamCard(t)
	m.aiP.rebuild(m)
	m.layout()
	return m, cmd
}
