package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/store"
)

// openUnread puts the panel up. It borrows the chat's own machinery whole —
// m.msgs, the normal key handler, the composer, the outbox — because unlike a
// search hit, a message here is one the reader answers where it stands. All
// that differs is that the page runs across chats and that nothing on it is
// taken as read.
func (m Model) openUnread() (tea.Model, tea.Cmd) {
	cmd := m.startUnread(true)
	return m, cmd
}

// startUnread is openUnread's own work, split out so the chats pane can reach
// it: openRow holds a pointer, and a value method's changes would not travel
// back through it.
//
// take says the page comes away with the focus, the way it does on a press or
// a click. The cursor walking onto the Unread row loads the page beside the
// reader and leaves them in the list, so the next j is still theirs to spend
// on the row below.
func (m *Model) startUnread(take bool) tea.Cmd {
	if m.feed != nil {
		return nil
	}
	// The composer belongs to the chat that is open, so its contents go back
	// there before the panel takes the widget over.
	keep := m.saveComposer()
	m.feed = &unreadFeed{from: m.chatID}
	m.mode = modeNormal
	if take {
		m.focus = paneMessages
	}
	m.closeRight()
	m.setReply(nil, false)
	m.input.SetValue("")
	m.clearMessagePane()
	m.msgSince, m.msgLimit = 0, 0
	m.rebuildMessages()
	return tea.Batch(keep, loadUnreadFeed(m.deps, nil))
}

// closeUnread takes the panel down, putting the reader back in the chat that
// was open when it went up. The feed itself is dropped by enterChat when that
// chat's page lands, so the panes never show its rows under a chat's name.
func (m *Model) closeUnread() tea.Cmd {
	from := m.feed.from
	keep := m.saveComposer()
	m.focus = paneChats
	// Cleared before openChat, whose own saveComposer would otherwise write
	// the emptied widget back a second time under the chat just saved.
	m.chatID = ""
	m.input.SetValue("")
	if from == "" {
		m.feed = nil
		m.clearMessagePane()
		m.rebuildMessages()
		return keep
	}
	return tea.Batch(keep, m.openChat(from))
}

// feedAnswer points the composer at the chat of the message r or R was pressed
// on, carrying whatever is already in it.
//
// Naming a message is the deliberate act the pinned target exists to make room
// for: the pin holds against a j across a border, not against the reader
// saying "this one". Without it submit would send under replyTo.ChatID while
// the title still named the chat the words were begun in, so the only chat
// name on screen would be the wrong one — and a send cannot be taken back.
func (m *Model) feedAnswer(replyTo *store.Message) tea.Cmd {
	if !m.inFeed() || replyTo == nil || replyTo.ChatID == m.chatID {
		return nil
	}
	m.chatID, m.feed.loaded, m.roster = replyTo.ChatID, "", nil
	return loadChatSide(m.deps, replyTo.ChatID)
}

// inFeed reports whether the panel is what the message pane is drawing. The
// search panel draws over it through the same rows and the same cursor, and
// while it does the cursor indexes hits rather than messages, so every key
// that reads m.msgs has to ask this rather than the field.
func (m Model) inFeed() bool { return m.feed != nil && !m.searching }

// composerHeld reports whether the composer is carrying something of the
// reader's: words they typed, or the message those words answer. Either makes
// the widget theirs rather than the target chat's.
func (m Model) composerHeld() bool { return m.input.Value() != "" || m.replyTo != nil }

// feedChatAt is the chat the message at idx belongs to.
func (m Model) feedChatAt(idx int) string {
	if idx < 0 || idx >= len(m.msgs) {
		return ""
	}
	return m.msgs[idx].ChatID
}

// feedRetarget points the composer at the chat under the cursor, which is what
// the pane's title names and where a reply goes.
//
// A composer holding anything — words, or the message they answer — keeps the
// target it was given. Sections are short and a border is one j away, so
// retargeting would file a half-written answer under the chat being left, or
// take away the quote r just put up, on one mistaken keypress. The pinned rule
// still follows the viewport, so which chat the eye is on and which chat the
// composer is for are both on screen.
func (m *Model) feedRetarget() tea.Cmd {
	if !m.inFeed() {
		return nil
	}
	want := m.feedChatAt(m.msgIdx)
	if want == "" || want == m.chatID || m.composerHeld() {
		return nil
	}
	keep := m.saveComposer()
	m.chatID, m.feed.loaded, m.roster = want, "", nil
	return tea.Batch(keep, loadChatSide(m.deps, want))
}

// jumpSection walks the cursor to the first message of the next section, the
// way n walks the chats list to the next row with something waiting.
func (m Model) jumpSection(step int) (tea.Model, tea.Cmd) {
	at := nextSection(sectionStarts(m.msgs), m.msgIdx, step)
	if at < 0 {
		return m.notify("nothing else waiting", false), nil
	}
	m.msgIdx, m.focus = at, paneMessages
	m.scrollMessagesToSelection()
	cmd := m.feedRetarget()
	return m.notify("", false), cmd
}

// openFeedHit leaves the panel for the chat the selected message is in,
// anchored on the message itself — the same landing a search hit makes.
// Reading it is that chat's business, which is the whole reason the panel does
// not do it here.
func (m Model) openFeedHit() (tea.Model, tea.Cmd) {
	sel, ok := m.selected()
	if !ok {
		return m, nil
	}
	m.pendingSelect = jumpTo(sel)
	m.focus = paneMessages
	cmd := m.openChatFrom(sel.ChatID, sel.CreateMs)
	return m.notify("", false), cmd
}

// feedRuleLine is the rule pinned under the pane's title: the section the
// viewport's top row belongs to. The pane already spends this line on a plain
// rule, so the section stays named however far into it the reader has scrolled
// without costing the page a row.
//
// pinned says that top row is the section's own rule, which the pane then
// holds open rather than drawing a second time.
func (m Model) feedRuleLine(w int) (line string, pinned bool) {
	if m.msgTop < 0 || m.msgTop >= len(m.msgRows) {
		return paneRule(w), false
	}
	top := m.msgRows[m.msgTop]
	chat := m.feedChatAt(top.idx)
	if chat == "" {
		return paneRule(w), false
	}
	return feedRule(m.feed.section(chat).rule(), 0, w).text, top.rule
}

// feedTitle names the panel and the chat a reply would go to.
func (m Model) feedTitle(w int) string {
	var waiting int64
	for _, s := range m.feed.sections {
		waiting += m.unread[s.chatID]
	}
	name := ""
	if c, ok := m.currentChat(); ok {
		name = stDim.Render(" · ") + flatten(c.Name)
	}
	tail := fmt.Sprintf(" · %d in %d chats · Esc to leave", waiting, len(m.feed.sections))
	return fit(stBold.Render(unreadLabel)+name+stDim.Render(tail), w)
}
