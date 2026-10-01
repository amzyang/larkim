package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/agentctx"
	"github.com/amzyang/larkim/ai"
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
	// draft says the answer goes to the composer when it lands, the :ai draft
	// hand-off.
	draft bool
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
	ch     <-chan ai.Chunk
	cancel context.CancelFunc
}

type aiTurnState int

const (
	aiAsking aiTurnState = iota
	aiDone
	aiFailed
	aiStopped
	// aiInterrupted is an answer that never finished because the program
	// left: stopped on quit, asking at load.
	aiInterrupted
)

// streaming reports an answer still arriving.
func (t *aiTurn) streaming() bool { return t.state == aiAsking }

// aiOpen reports that the assistant column holds the right-hand pane, over
// whatever frame it covered without closing it.
func (m Model) aiOpen() bool { return m.aiP != nil && m.aiP.open }

// newAI builds the panel's state the first time it is opened.
func newAI() *aiPanel {
	in := newComposer()
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
			t := &aiTurn{id: tv.ID, seq: tv.Seq, ask: tv.Ask, sent: tv.Sent, draft: tv.Draft,
				anchorID: tv.AnchorID, thread: tv.ThreadID, window: tv.Window,
				compose: tv.Compose, sel: tv.Sel, answer: tv.Answer, err: tv.Err,
				at: time.UnixMilli(tv.AtMs)}
			switch tv.State {
			case store.AITurnAsking, store.AITurnInterrupted:
				t.state = aiInterrupted
			case store.AITurnFailed:
				t.state = aiFailed
			case store.AITurnStopped:
				t.state = aiStopped
			default:
				t.state = aiDone
			}
			if tv.AnchorID != "" {
				anchors = append(anchors, tv.AnchorID)
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
		m.aiP = newAI()
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
// streams included: they belong to their sessions, not to the pane.
func (m Model) closeAI() Model {
	if m.aiP != nil {
		m.aiP.open = false
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
	p.cur = (p.cur+d+len(p.sess)) % len(p.sess)
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
// never finished is marked interrupted on its way out, so the next run reads
// it as that rather than as an answer still owed.
func (p *aiPanel) stopAll(d Deps) []tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range p.allSessions() {
		for _, t := range s.turns {
			if t.cancel != nil {
				t.cancel()
			}
			if t.streaming() {
				t.state, t.cancel = aiInterrupted, nil
				cmds = append(cmds, saveTurnCmd(d, s, t))
			}
		}
	}
	return cmds
}

// storedTurn snapshots a turn whole, the shape the table upserts: every write
// carries the complete state, so an out-of-order write cannot leave half a
// turn behind.
func storedTurn(s *aiSession, t *aiTurn) store.AITurn {
	return store.AITurn{ID: t.id, SessionID: s.id, Seq: t.seq, Ask: t.ask, Sent: t.sent,
		Draft: t.draft, AnchorID: t.anchorID, ThreadID: t.thread, Window: t.window,
		Compose: t.compose, Sel: t.sel, State: int(t.state), Answer: t.answer,
		Err: t.err, AtMs: t.at.UnixMilli()}
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
func askTurn(d Deps, client AIStreamer, off error, p *aiPanel, s *aiSession, t *aiTurn) tea.Cmd {
	if off != nil || client == nil {
		err := cmp.Or(off, errAssistantOff)
		return func() tea.Msg {
			return aiStartedMsg{turn: t.id, ch: errCh(ai.Chunk{Err: err, Done: true})}
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
	t := m.aiP.turn(msg.turn)
	if t == nil {
		return m, nil
	}
	t.ch, t.cancel = msg.ch, msg.cancel
	return m, waitForAI(t.id, t.ch)
}

// turn finds a turn by id across the panel's sessions, the chat on screen's
// and the stashed ones alike: an answer belongs to its session wherever the
// reader has since gone.
func (p *aiPanel) turn(id string) *aiTurn {
	_, t := p.findTurn(id)
	return t
}

// findTurn is turn with the session the turn belongs to, which is what
// persisting one needs.
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
// as it stands, whole.
func (m Model) onAIChunk(msg aiChunkMsg) (tea.Model, tea.Cmd) {
	s, t := m.aiP.findTurn(msg.turn)
	if t == nil {
		return m, nil
	}
	// A closing chunk may carry the last piece with it, so text is taken in
	// every state.
	c := msg.chunk
	t.answer += c.Text
	switch {
	case c.Err != nil:
		t.state, t.err = aiFailed, c.Err.Error()
		t.cancel = nil
	case c.Stopped:
		t.state, t.cancel = aiStopped, nil
	case !c.Done:
		if m.aiP.follow {
			m.aiP.toBottom(m.listHeight())
		}
		m.aiP.rebuild(m)
		return m, waitForAI(t.id, t.ch)
	default:
		t.state, t.cancel = aiDone, nil
		// The draft hand-off belongs to the chat it was asked in: a draft
		// landing in another chat's composer is a message aimed at nobody.
		// The chat's box is filled by name, so the keys being in the panel's
		// own box costs nothing.
		if t.draft && m.aiP.chat == m.chatID && strings.TrimSpace(t.answer) != "" {
			m.input.SetValue(strings.TrimSpace(t.answer))
			if m.side != sideAI {
				m.replan()
			}
			return m.notify("draft placed in the composer: i to edit, Enter to send", false),
				saveTurnCmd(m.deps, s, t)
		}
		if !m.aiOpen() || m.aiP.chat != m.chatID {
			return m.notify("assistant finished", false), saveTurnCmd(m.deps, s, t)
		}
	}
	m.aiP.rebuild(m)
	m.layout()
	return m.notify("", false), saveTurnCmd(m.deps, s, t)
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

// errAssistantOff names the one question a panel without an agent always
// answers with.
var errAssistantOff = errors.New("assistant off: no agent found (config ai.agent)")

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
		none := aiAct{turn: t.id, card: -1}
		add(aiRowOf(m.aiTurnHead(t, w), w), none)
		addAll(plainRows(wrap(t.ask, w), w), none)
		add(m.aiAnswerHeadRow(t, w, last), none)
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
			add(aiRowOf(stDim.Render("┌─"), w), act)
			addAll(m.cardBodyRows(seg.Text, w), act)
			add(aiCardFoot(w, act), act)
		}
	}
	p.sel = clamp(p.sel, 0, max(0, len(p.rows)-1))
	if p.follow {
		p.toBottom(m.listHeight())
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

// aiCardFoot is the action row a finished card draws under itself. The
// labels are the client's own words (Insert, Copy); Send and Reply join them
// from their phase.
func aiCardFoot(w int, a aiAct) msgRow {
	const lead = "└─ "
	parts := []struct {
		label string
		kind  aiActKind
	}{{"Insert", actInsert}, {"Copy", actCopy}}
	x, line := lipgloss.Width(lead), lead
	var zones []clickZone
	for _, p := range parts {
		if x > lipgloss.Width(lead) {
			line += " · "
			x += 3
		}
		line += p.label
		zones = append(zones, clickZone{x0: x, x1: x + len(p.label),
			act: aiAct{kind: p.kind, turn: a.turn, card: a.card}})
		x += len(p.label)
	}
	tail := strings.Repeat("─", max(1, w-lipgloss.Width(line)))
	row := aiRowOf(stDim.Render(line)+stDim.Render(tail), w)
	row.zones = zones
	return row
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
	h := m.listHeight()
	if p.sel < p.top {
		p.top = p.sel
	}
	if p.sel >= p.top+h {
		p.top = p.sel - h + 1
	}
	p.top = clamp(p.top, 0, max(0, len(p.rows)-h))
}

// cardText is the card an act names.
func (m Model) cardText(a aiAct) (string, bool) {
	if a.card < 0 {
		return "", false
	}
	_, t := m.aiP.findTurn(a.turn)
	if t == nil {
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
	// A panel with no agent still takes the retry: the turn fails with the
	// notice rather than the key refusing to answer.
	var off error
	if m.ai == nil {
		off = fmt.Errorf("assistant off: %s not found (config ai.agent)", m.agentName())
	}
	t.answer, t.err, t.state, t.ch, t.cancel = "", "", aiAsking, nil, nil
	p.follow = true
	p.rebuild(m)
	m.layout()
	asking := askTurn(m.deps, m.ai, off, p, s, t)
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
	case actRegenerate:
		return m.regenerateAI()
	case actStop:
		return m.stopAI()
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
	head := stBold.Render("You "+t.at.Format("15:04"))
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
		chips = append(chips, plural(n, "selected", "selected"))
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
		chips = append(chips, "☰ "+plural(n, "selected", "selected"))
	}
	if strings.TrimSpace(m.aiDraftText()) != "" {
		chips = append(chips, "✎ draft")
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
	if m.aiP == nil {
		m.aiP = newAI()
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
	m.aiP.anchor = nil
	m.aiP.selection = sel
	m.mode = modeNormal
	next, cmd := m.openAI(m.chatID, false)
	out, enter := next.enterAI()
	return out, tea.Batch(cmd, enter)
}

// askCommand is :ai. Alone it opens the panel; with an argument it starts a
// new session and asks at once, the scripted path the : line has always had.
func (m Model) askCommand(input string) (tea.Model, tea.Cmd) {
	input = strings.TrimSpace(input)
	if input == "" {
		return m.openAIKey(false)
	}
	if m.aiChat() == "" {
		return m.notify("open a chat first", true), nil
	}
	prompt, draft := ai.Prompt(input)
	next, cmd := m.openAI(m.aiChat(), true)
	out, ask := next.askAI(input, prompt, draft)
	return out, tea.Batch(cmd, ask)
}

// askAI turns a question into a turn of the session on screen and starts it.
// sent is what the model is asked when it differs from the question shown,
// which is the :ai forms.
func (m Model) askAI(ask, sent string, draft bool) (tea.Model, tea.Cmd) {
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
	var off error
	if m.ai == nil {
		off = fmt.Errorf("assistant off: %s not found (config ai.agent)", m.agentName())
	}
	anchor := cloneMsg(p.anchor)
	t := &aiTurn{id: uuid.New().String(), ask: ask, sent: sent, draft: draft, seq: len(s.turns),
		anchorID: msgIDOf(anchor), anchor: anchor, thread: m.threadID, window: m.cfg.AI.Context,
		compose: m.aiDraftText(), sel: slices.Clone(p.selection), at: time.Now()}
	if s.created == 0 {
		s.created = t.at.UnixMilli()
	}
	s.turns = append(s.turns, t)
	p.input.Reset()
	p.follow = true
	p.rebuild(m)
	m.layout()
	asking := askTurn(m.deps, m.ai, off, p, s, t)
	return m.notify("asking "+m.agentName()+"…", false),
		tea.Batch(saveSessionCmd(m.deps, p.chat, s), saveTurnCmd(m.deps, s, t), asking)
}

// msgIDOf names a message a turn records, '' for none.
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
	case "s", "S", "e", "f", "C", "E", "t", "v", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0":
		// These act on a message, and no message is on screen here: the frame
		// under the panel keeps its own until Esc uncovers it.
		return m.notify("no message selected here — Esc uncovers the frame beneath", true), nil, true
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
