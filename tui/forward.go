package tui

import (
	"context"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/google/uuid"
)

// fwdTarget is one place a message can be forwarded to: a chat, or a person
// whose chat larkim may not hold yet.
type fwdTarget struct {
	chatID string
	userID string
	name   string
	mark   []int
}

func (t fwdTarget) target() larkcli.Target {
	if t.chatID != "" {
		return larkcli.Target{ChatID: t.chatID}
	}
	return larkcli.Target{UserID: t.userID}
}

// forwarder is the destination chooser, open only in modeForward. A zero value
// is closed.
type forwarder struct {
	// msg is what is being forwarded, held by id because a sync tick can
	// replace the list under the chooser.
	msg   store.Message
	input textinput.Model
	hits  []fwdTarget
	idx   int
	top   int
}

// forwardedMsg closes a forward.
type forwardedMsg struct {
	messageID string
	err       error
}

// openForward arms the chooser on the selected message.
func (m Model) openForward() (tea.Model, tea.Cmd) {
	x, ok := m.selected()
	if !ok {
		return m.notify("select a message to forward", true), nil
	}
	if x.Deleted {
		return m.notify("a recalled message has nothing left to forward", true), nil
	}
	if m.outboxAt(x.MessageID) != nil {
		return m.notify("that message has not reached Feishu yet", true), nil
	}
	in := textinput.New()
	in.Prompt = ""
	in.SetStyles(textinput.DefaultStyles(m.dark))
	in.SetVirtualCursor(false)
	m.mode = modeForward
	m.fwd = forwarder{msg: x, input: in}
	m.fwd.hits = m.fwdSearch("")
	m.layout()
	// The chats are already in hand, so the chooser opens on them and the
	// people drop in behind once the table has been read.
	return m, tea.Batch(m.fwd.input.Focus(), loadContacts(m.deps))
}

// fwdSearch narrows the places a message can go. Chats come before people, in
// the list's own order, so the chats a reader has been in lately are the first
// thing they see; a person is offered because a colleague larkim has no chat
// with yet is still somewhere a message can be sent.
func (m Model) fwdSearch(query string) []fwdTarget {
	ix := fuzzy.NewIndex()
	var out []fwdTarget
	for _, c := range m.chats {
		if len(out) == fwdLimit {
			return out
		}
		if mark, ok := ix.Match(c.ChatID, flatten(c.Name), query); ok {
			out = append(out, fwdTarget{chatID: c.ChatID, name: flatten(c.Name), mark: mark})
		}
	}
	seen := make(map[string]bool, len(out))
	for _, c := range m.chats {
		seen[c.P2PTargetID] = true
	}
	for _, p := range m.contacts {
		if len(out) == fwdLimit {
			return out
		}
		// Somebody already reachable as a chat is not offered twice.
		if seen[p.OpenID] || p.OpenID == m.deps.Self || p.Name == "" {
			continue
		}
		if mark, ok := ix.Match(p.OpenID, p.Name, query); ok {
			out = append(out, fwdTarget{userID: p.OpenID, name: p.Name, mark: mark})
		}
	}
	return out
}

// fwdLimit is how many destinations the chooser offers. Past this the list is
// something to scroll rather than to pick from, and the filter is the way
// through it.
const fwdLimit = 50

// fwdRows is how many destinations fit: the composer's box less the query line.
func (m Model) fwdRows() int { return max(1, m.composerHeight()-1) }

// onForwardKey drives the chooser, on the same keys the emoji picker uses:
// the filter owns everything it can edit with.
func (m Model) onForwardKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		return m.closeForward(), nil
	case "enter":
		return m.chooseForward()
	case "up", "ctrl+p":
		m.fwd.move(-1, m.fwdRows())
		return m, nil
	case "down", "ctrl+n":
		m.fwd.move(1, m.fwdRows())
		return m, nil
	}
	before := m.fwd.input.Value()
	var cmd tea.Cmd
	m.fwd.input, cmd = m.fwd.input.Update(k)
	if q := m.fwd.input.Value(); q != before {
		m.fwd.hits = m.fwdSearch(q)
		m.fwd.idx, m.fwd.top = 0, 0
	}
	return m, cmd
}

func (m Model) closeForward() Model {
	m.mode = modeNormal
	m.fwd = forwarder{}
	m.layout()
	return m
}

// chooseForward sends the message on. Unlike a recall this asks nothing first:
// it adds a message somewhere rather than taking one back, and the chooser the
// reader just picked from is itself the deliberate step.
func (m Model) chooseForward() (tea.Model, tea.Cmd) {
	if m.fwd.idx >= len(m.fwd.hits) {
		return m.closeForward(), nil
	}
	hit := m.fwd.hits[m.fwd.idx]
	messageID := m.fwd.msg.MessageID
	next := m.closeForward()
	return next.notify("forwarding to "+hit.name+"…", false),
		forwardCmd(m.deps, messageID, hit.target())
}

// forwardCmd sends one message on and ingests what came back, so the copy
// shows up in the destination without waiting for a sync tick.
func forwardCmd(d Deps, messageID string, target larkcli.Target) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		sent, err := d.Client.Forward(ctx, messageID, target, uuid.NewString())
		if err != nil {
			return forwardedMsg{messageID: messageID, err: err}
		}
		return forwardedMsg{messageID: sent.MessageID, err: ingestMessage(d, sent.MessageID)}
	}
}

func (f *forwarder) move(d, rows int) { moveCursor(&f.idx, &f.top, d, len(f.hits), rows) }

func (m Model) fwdVisible() []fwdTarget { return window(m.fwd.hits, m.fwd.top, m.fwdRows()) }

// fwdPrompt labels the query box. The cursor is placed past it, so its width
// cannot be measured in two places.
func fwdPrompt() string { return stBold.Render("forward") + stAccent.Render(" › ") }

// renderForward draws the chooser in the composer's place, naming above it the
// message being sent on so the reader can see they picked the right one.
func (m Model) renderForward() string {
	w := m.width - 2
	rows := m.fwdRows()
	gist := m.fwdGist(w)
	lines := []string{padBetween(fwdPrompt()+m.fwd.input.View(), stDim.Render(strconv.Itoa(len(m.fwd.hits))), w)}
	if len(m.fwd.hits) == 0 {
		lines = append(lines, fit(stDim.Render("  nothing matches "+m.fwd.input.Value()), w))
	}
	for i, t := range m.fwdVisible() {
		mark := " "
		if m.fwd.top+i == m.fwd.idx {
			mark = stAccent.Render("▸")
		}
		kind := ""
		if t.userID != "" {
			kind = stDim.Render("  person")
		}
		lines = append(lines, fit(mark+" "+markName(t.name, t.mark, stBold)+kind, w))
	}
	for len(lines) < rows+1 {
		lines = append(lines, fit("", w))
	}
	return paneStyle(true, w).Render(strings.Join(append(lines[:rows+1], gist), "\n"))
}

// fwdGist names the message being sent on, above the chooser. An official
// emoji in it is drawn the way the client draws it rather than spelled by the
// name it was written with.
func (m Model) fwdGist(w int) string {
	if segs := m.fwdGistSegs(w); segs != nil {
		return m.joinSegsWidth(segs)
	}
	head := stDim.Render(fwdGistMark)
	return head + stDim.Render(truncate(flatten(replyGist(m.fwd.msg)), w-lipgloss.Width(head)))
}

// fwdGistMark opens the line, the same arrow the status bar marks a forward
// with.
const fwdGistMark = "↪ "

// fwdGistSegs is that line in pieces, or nil when it needs no picture. The
// pane asks for it twice, once to claim the pictures and once to draw them.
func (m Model) fwdGistSegs(w int) []rowSeg {
	return gistSegs(stDim.Render(fwdGistMark), flatten(replyGist(m.fwd.msg)), w, stDim, m.chatPics().gist)
}

// loadContacts fills the list the forward chooser offers people from. It reads
// the whole contacts table, raw_json and all, so it is asked for when a pane
// that needs it opens rather than on every refresh.
func loadContacts(d Deps) tea.Cmd {
	return func() tea.Msg {
		people, err := d.Store.ListContacts(context.Background(), 0)
		if err != nil {
			d.log().Error("load contacts", "err", err)
			return contactsLoadedMsg{}
		}
		return contactsLoadedMsg{people: people}
	}
}

type contactsLoadedMsg struct{ people []store.Contact }
