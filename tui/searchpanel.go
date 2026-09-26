package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// searchRestDelay is how long the query has to stand still before the store is
// asked. Held keys and fast typing land well inside it, so a word costs one
// search rather than one per letter.
const searchRestDelay = 80 * time.Millisecond

// searchKind says which of a hit's fields is the one that is set.
type searchKind int

const (
	hitMessage searchKind = iota
	hitChat
	hitPerson
)

// searchHit is one row the panel can open. The three kinds share a cursor, so
// the reader moves through messages, chats and people with the same keys.
type searchHit struct {
	kind searchKind
	msg  store.Message
	chat store.Chat
	user larkcli.User
	// mark is the runes of a chat or person name the query landed on.
	mark []int
	// remote says the hit came from Feishu rather than the store, so it may
	// name a message this machine has never seen.
	remote bool
}

// searchRestMsg fires once the query has stood still for searchRestDelay, and
// remoteRestMsg once it has stood still long enough to be worth a round trip.
// gen names the query each was armed for.
type searchRestMsg struct{ gen int }
type remoteRestMsg struct{ gen int }

// openSearch puts the panel up over the messages pane, which is the widest
// one and already the surface a search draws on.
func (m Model) openSearch(seed string) (tea.Model, tea.Cmd) {
	m.mode = modeSearch
	m.searching = true
	m.focus = paneMessages
	m.searchLocal, m.searchRemote, m.searchHits, m.msgIdx, m.msgTop = nil, nil, nil, 0, 0
	m.cmdline.Prompt = "⌕ "
	m.cmdline.SetValue(seed)
	m.searchQuery = seed
	m.rebuildMessages()
	return m, tea.Batch(m.cmdline.Focus(), m.armSearch())
}

// armSearch starts the rest timers for the query in hand and drops whatever
// Feishu is still working on: it was asked about a query that no longer
// exists, and the lane it holds is one a keystroke may want.
func (m *Model) armSearch() tea.Cmd {
	m.cancelRemote()
	m.searchGen++
	gen := m.searchGen
	return tea.Batch(
		tea.Tick(searchRestDelay, func(time.Time) tea.Msg { return searchRestMsg{gen} }),
		tea.Tick(remoteRestDelay, func(time.Time) tea.Msg { return remoteRestMsg{gen} }),
	)
}

// cancelRemote stops the search in flight, whether it is queued for a lane or
// already running: the subprocess is killed with its process group.
func (m *Model) cancelRemote() {
	if m.searchCancel != nil {
		m.searchCancel()
		m.searchCancel = nil
	}
	m.searchBusy = false
}

// startRemote asks Feishu, once the query has stood still and is long enough
// to be worth asking about.
func (m *Model) startRemote() tea.Cmd {
	m.cancelRemote()
	if len([]rune(strings.TrimSpace(m.searchQuery))) < remoteMinQuery {
		return nil
	}
	ctx, cancel := context.WithCancel(larkcli.WithLane(context.Background(), larkcli.LaneInteractive))
	m.searchCancel, m.searchBusy = cancel, true
	return remoteSearch(ctx, m.deps, m.searchQuery, m.searchGen)
}

// rebuildHits lays the panel's rows out in the order they are drawn: the
// store's message hits, then Feishu's, then the chats and the people. Remote
// hits join the message group rather than opening one of their own — they are
// the same question answered further back.
func (m *Model) rebuildHits() {
	var msgs, rest []searchHit
	for _, h := range m.searchLocal {
		if h.kind == hitMessage {
			msgs = append(msgs, h)
			continue
		}
		rest = append(rest, h)
	}
	m.searchHits = slices.Concat(msgs, m.searchRemote, rest)
}

// claimSearch reports whether a timer's generation is still the current one,
// so a query typed over collapses into the one search it settles on.
func (m Model) claimSearch(gen int) bool { return m.searching && gen == m.searchGen }

// closeSearch takes the panel down and puts the cursor back on the open chat.
func (m *Model) closeSearch() {
	m.cancelRemote()
	m.mode = modeNormal
	m.cmdline.Blur()
	m.cmdline.Reset()
	m.searching, m.searchQuery, m.mentions = false, "", false
	m.searchLocal, m.searchRemote, m.searchHits = nil, nil, nil
	m.msgIdx = len(m.msgs) - 1
	m.rebuildMessages()
	m.scrollMessagesToSelection()
}

func (m Model) onSearchKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.closeSearch()
		return m.notify("", false), nil
	case "enter":
		return m.openHit()
	case "down", "ctrl+n":
		return m.moveSelection(1)
	case "up", "ctrl+p":
		return m.moveSelection(-1)
	case "pgdown":
		return m.moveSelection(m.msgListHeight())
	case "pgup":
		return m.moveSelection(-m.msgListHeight())
	}
	// The mentions list has no query to edit: it is the answer to a fixed
	// question, so a keystroke that would narrow a search does nothing here
	// rather than narrowing something the reader cannot see.
	if m.mentions {
		return m, nil
	}
	// Everything else edits the query, which is why the panel moves on
	// ctrl+n/ctrl+p rather than ctrl+d/ctrl+u: those already mean something
	// to the line editor the query is typed into.
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(k)
	if v := m.cmdline.Value(); v != m.searchQuery {
		m.searchQuery = v
		return m, tea.Batch(cmd, m.armSearch())
	}
	return m, cmd
}

// moveSelection walks the hit list under the panel, where a row is a hit
// rather than a message.
func (m Model) moveSelection(n int) (tea.Model, tea.Cmd) {
	m.msgIdx = clamp(m.msgIdx+n, 0, len(m.searchHits)-1)
	m.rebuildMessages()
	m.scrollMessagesToSelection()
	return m, nil
}

// selectedHit is the row under the cursor.
func (m Model) selectedHit() (searchHit, bool) {
	if m.msgIdx < 0 || m.msgIdx >= len(m.searchHits) {
		return searchHit{}, false
	}
	return m.searchHits[m.msgIdx], true
}

// openHit follows the row under the cursor: a message lands on the message, a
// chat opens the chat, a person opens the chat with them.
func (m Model) openHit() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHit()
	if !ok {
		return m, nil
	}
	switch h.kind {
	case hitChat:
		m.closeSearch()
		return m, m.openChat(h.chat.ChatID)
	case hitPerson:
		return m.openPerson(h.user)
	}
	if h.remote {
		return m.openColdHit(h)
	}
	m.pendingSelect = jumpTo(h.msg)
	m.notice = ""
	cmd := m.openChatFrom(h.msg.ChatID, h.msg.CreateMs)
	return m, cmd
}

// openColdHit follows a message Feishu found that this machine has not
// stored. With the sync lock in hand it is pulled in first, so the page opens
// on it like any other; without it, only the daemon may write, so the chat
// opens where it stands and the hit arrives on the daemon's own schedule.
func (m Model) openColdHit(h searchHit) (tea.Model, tea.Cmd) {
	if m.deps.Syncer == nil {
		m.closeSearch()
		// openChatFrom writes to m, so it runs before the model is copied
		// into the return: notify is a call too, and the two would otherwise
		// be sequenced the wrong way round.
		cmd := m.openChatFrom(h.msg.ChatID, h.msg.CreateMs)
		return m.notify("opening the chat; the daemon has yet to sync that message", false), cmd
	}
	m.cancelRemote()
	return m.notify("fetching…", false), ingestThenOpen(m.deps, h.msg.MessageID)
}

// openPerson opens the chat with someone. A person this machine has never
// exchanged a message with has no chat row of their own, so the composer is
// primed with the send that starts one.
func (m Model) openPerson(u larkcli.User) (tea.Model, tea.Cmd) {
	for _, c := range m.chats {
		if c.ChatID == u.P2PChatID && u.P2PChatID != "" {
			m.closeSearch()
			return m, m.openChat(c.ChatID)
		}
	}
	m.closeSearch()
	m.mode = modeCommand
	m.cmdline.Prompt = ":"
	m.cmdline.SetValue("send " + u.OpenID + " ")
	return m, m.cmdline.Focus()
}
