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
	// back up on return.
	sess    []*aiSession
	stashed map[string][]*aiSession
	cur     int
	// rows is the turn list as the pane draws it, rebuilt on every change,
	// and top the first row of it on screen.
	rows []msgRow
	top  int
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
	id    string
	title string
	turns []*aiTurn
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
	// the recorded context
	anchor  *store.Message
	thread  string
	window  int
	compose string
	sel     []string

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
// stashed and the new chat's own picked up, so the header never lists another
// chat's conversations.
func (p *aiPanel) point(chat string) {
	if p.chat == chat {
		return
	}
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
	if m.aiP.chat != chat {
		m.aiP.point(chat)
		m.aiP.rebuild(m)
	}
	if fresh || len(m.aiP.sess) == 0 {
		m.aiP.sess = append(m.aiP.sess, &aiSession{id: uuid.New().String()})
		m.aiP.cur = len(m.aiP.sess) - 1
		m.aiP.top = 0
	}
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
// sessions are in flight as surely as the chat on screen.
func (p *aiPanel) stopAll() {
	for _, s := range p.allSessions() {
		for _, t := range s.turns {
			if t.cancel != nil {
				t.cancel()
			}
		}
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
	// the only things taken from the panel, which the Update loop owns.
	history := make([]ai.QA, 0, aiHistory)
	for _, older := range s.turns {
		if older == t || older.state != aiDone {
			continue
		}
		history = append(history, ai.QA{Question: older.sent, Answer: older.answer})
	}
	history = history[max(0, len(history)-aiHistory):]
	anchor := cloneMsg(t.anchor)
	sel := slices.Clone(t.sel)
	compose, window := t.compose, t.window
	return func() tea.Msg {
		ctx := context.Background()
		in, extra, err := aiWindow(ctx, d, chatID, window, anchor, sel, compose)
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
	for _, s := range p.allSessions() {
		for _, t := range s.turns {
			if t.id == id {
				return t
			}
		}
	}
	return nil
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
// itself has gone in the meantime.
func (m Model) onAIChunk(msg aiChunkMsg) (tea.Model, tea.Cmd) {
	t := m.aiP.turn(msg.turn)
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
			return m.notify("draft placed in the composer: i to edit, Enter to send", false), nil
		}
		if !m.aiOpen() || m.aiP.chat != m.chatID {
			return m.notify("assistant finished", false), nil
		}
	}
	m.aiP.rebuild(m)
	m.layout()
	return m.notify("", false), nil
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
// shape.
func aiWindow(ctx context.Context, d Deps, chatID string, window int, anchor *store.Message, sel []string, composeText string) (agentctx.Input, string, error) {
	if window <= 0 {
		window = messagePageSize
	}
	q := store.MessageQuery{ChatID: chatID, Desc: true, Limit: window, ExcludeThreadReplies: true}
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

// rebuild lays the turn list out for the width the pane has now. The head rows
// are the panel's own; answer bodies go through the markdown renderer posts
// use, so what the reader checks against the chat and what the agent writes
// are one language.
func (p *aiPanel) rebuild(m Model) {
	w := m.rightWidth() - 2
	p.rows = nil
	s := p.session()
	if s == nil {
		return
	}
	st := m.msgStyleFor(w, msgMeta{})
	for _, t := range s.turns {
		p.rows = append(p.rows, aiRowOf(m.aiTurnHead(t, w), w))
		p.rows = append(p.rows, plainRows(wrap(t.ask, w), w)...)
		p.rows = append(p.rows, aiRowOf(m.aiAnswerHead(t, w), w))
		switch t.state {
		case aiAsking:
			if strings.TrimSpace(t.answer) == "" {
				p.rows = append(p.rows, aiRowOf(stDim.Render("…"), w))
				continue
			}
		case aiFailed:
			p.rows = append(p.rows, aiRowOf(stErr.Render(truncate(t.err, w)), w))
			continue
		case aiStopped:
			p.rows = append(p.rows, aiRowOf(stDim.Render("stopped"), w))
			continue
		}
		var b block
		g := leads{b: &b}
		p.rows = append(p.rows, mdRows(t.answer, store.Message{}, 0, st, &g, mentions{})...)
	}
	if p.follow {
		p.toBottom(m.listHeight())
	}
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

// aiAnswerHead names an answer and the state it is in.
func (m Model) aiAnswerHead(t *aiTurn, w int) string {
	head := stBold.Render("AI "+t.at.Format("15:04"))
	state := ""
	switch t.state {
	case aiAsking:
		state = "answering · x stops"
	case aiFailed:
		state = "failed"
	case aiStopped:
		state = "stopped"
	}
	if state == "" {
		return head
	}
	return head + stDim.Render("  "+state)
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
		p.sess = append(p.sess, &aiSession{id: uuid.New().String()})
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
	t := &aiTurn{id: uuid.New().String(), ask: ask, sent: sent, draft: draft,
		anchor: cloneMsg(p.anchor), thread: m.threadID, window: m.cfg.AI.Context,
		compose: m.aiDraftText(), sel: slices.Clone(p.selection), at: time.Now()}
	s.turns = append(s.turns, t)
	p.input.Reset()
	p.follow = true
	p.rebuild(m)
	m.layout()
	return m.notify("asking "+m.agentName()+"…", false), askTurn(m.deps, m.ai, off, p, s, t)
}

// onAIKey takes the keys the assistant column owns, before any of them can
// reach the frame hidden under the panel: a message nobody can see is not a
// message to act on. It answers took only for keys it owns; the ones every
// pane shares — movement, panes, help, Esc's ladder — fall through untouched.
func (m Model) onAIKey(s string) (Model, tea.Cmd, bool) {
	switch s {
	case "i", "enter":
		next, cmd := m.enterAI()
		return next.(Model), cmd, true
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
	case "h", "left":
		// The column keeps its frame; h steps to the messages pane rather
		// than backing out of a stack that is not even on screen.
		m.focus = paneMessages
		return m, nil, true
	case "r", "R", "s", "S", "e", "f", "C", "D", "E", "t", "o", "Y", "y", "v", ".", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0":
		// These act on a message, and no message is on screen here: the frame
		// under the panel keeps its own until Esc uncovers it.
		return m.notify("no message selected here — Esc uncovers the frame beneath", true), nil, true
	}
	return m, nil, false
}
