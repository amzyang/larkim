package tui

import (
	"context"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/store"
)

// openUnread puts the panel up. It borrows the chat's own machinery whole —
// m.msgs, the normal key handler, the composer, the outbox — because unlike a
// search hit, a message here is one the reader answers where it stands. All
// that differs is that the page runs across chats, and that a chat on it is
// read by the reader landing on or acting on one of its messages rather than
// by being shown.
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
	m.feed = &unreadFeed{from: m.chatID, readHere: map[string]int64{}}
	m.mode = modeNormal
	if take {
		m.focus = paneMessages
	}
	// saveComposer above already wrote both boxes back, so the close owes
	// nothing further.
	m.closeRight()
	m.setQuoteOn(sideMain, nil, false)
	m.input.SetValue("")
	m.clearMessagePane()
	m.msgSince, m.msgLimit = 0, 0
	m.rebuildMessages()
	return tea.Batch(keep, loadUnreadFeed(m.deps, m.feed))
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
	// The panel the list opens into has no chat behind it. The list's own
	// first chat is where Esc leaves the reader, so taking the page down
	// hands them a chat rather than an empty pane.
	if from == "" && len(m.chats) > 0 {
		from = m.chats[0].ChatID
	}
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

// chatPage reports whether the message pane is drawing the open chat's own
// page, rather than the panel's feed or the search hits, which run across
// chats the reader has not opened.
func (m Model) chatPage() bool { return m.feed == nil && !m.searching }

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
	if m.clearDotsAtCursor() {
		m.rebuildMessages()
	}
	m.scrollMessagesToSelection()
	cmd := m.feedRetarget()
	return m.notify("", false), cmd
}

// openFeedHit leaves the panel for the chat the selected message is in, the
// way pressing Enter on that chat's row does, with the cursor landing on the
// message itself. The backlog that put the message here is the chat's newest
// stretch, so its own page carries it and the history before it — an anchored
// window from the hit would pin those few rows to the top of the pane and
// hide the rest.
func (m Model) openFeedHit() (tea.Model, tea.Cmd) {
	sel, ok := m.selected()
	if !ok {
		return m, nil
	}
	m.pendingSelect = jumpTo(sel)
	m.focus = paneMessages
	cmd := m.openChat(sel.ChatID)
	return m.notify("", false), cmd
}

// feedTopChat is the chat the viewport's top row belongs to, which is the
// section the pinned rule names and the one its button marks.
func (m Model) feedTopChat() string {
	if m.msgTop < 0 || m.msgTop >= len(m.msgRows) {
		return ""
	}
	return m.feedChatAt(m.msgRows[m.msgTop].idx)
}

// feedRuleLine is the rule pinned under the pane's title: the section the
// viewport's top row belongs to. The pane already spends this line on a plain
// rule, so the section stays named however far into it the reader has scrolled
// without costing the page a row.
//
// pinned says that top row is the section's own rule, which the pane then
// holds open rather than drawing a second time.
func (m Model) feedRuleLine(w int) (line string, pinned bool) {
	chat := m.feedTopChat()
	if chat == "" {
		return paneRule(w), false
	}
	return feedRule(m.feed.section(chat).rule(), 0, w).text, m.msgRows[m.msgTop].rule
}

// markSectionRead takes one chat of the page as read: the deliberate act the
// panel's own reading gate refuses to make for the reader, reached by the check
// on the section's rule or by m.
//
// The local write settles the whole chat, so the client is walked onto the
// chat's newest waiting message as the store has it, not the page's: a
// section stops at unreadSectionLimit, and a watermark taken from the page
// would leave Feishu's dot on everything past the cut. Unlike takeRead's gate,
// this fires once per press, the way a mark-all does, so it can read the same
// set the sweep does. joinUnread drops the settled section on the reload that
// follows.
func (m Model) markSectionRead(chatID string) (tea.Model, tea.Cmd) {
	if !m.inFeed() || chatID == "" {
		return m, nil
	}
	label := m.feed.section(chatID).label()
	// A chat read here is held only because the reader was still in it, and
	// this is the reader saying they are done with it.
	delete(m.feed.readHere, chatID)
	// The reload answers the press rather than waiting on the store's own
	// revision, the way onMarkAllDone does.
	cmds := tea.Batch(sectionDot(m.deps, chatID), m.reloadCurrent())
	return m.notify(label+" taken as read", false), cmds
}

// sectionDotMsg carries the chat a section's mark-read still owes Feishu,
// nothing when the client has no dot for it.
type sectionDotMsg struct{ chats []store.ChatUnread }

// sectionDot reads the dot the client still draws for a chat. It does not
// depend on local_read_at, so it reads the same whether or not the local
// write batched beside it has landed.
func sectionDot(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		c, ok, err := d.Store.ChatWithUnread(context.Background(), chatID)
		if err != nil {
			d.Log.Warn("read feishu dot", "chat_id", chatID, "err", err)
		}
		if !ok {
			return sectionDotMsg{}
		}
		return sectionDotMsg{chats: []store.ChatUnread{c}}
	}
}

// readFeedChat reads a chat on the page the way opening it reads it in the
// client: the whole chat, past what its section shows. The chat is held from
// this moment rather than from when its watermark comes back, so no page read
// from here on can take its section away while the read is landing.
func (m *Model) readFeedChat(chatID string) tea.Cmd {
	if !m.inFeed() || !slices.ContainsFunc(m.feed.sections, func(s unreadSection) bool { return s.chatID == chatID }) {
		return nil
	}
	if _, ok := m.feed.readHere[chatID]; !ok {
		m.feed.readHere[chatID] = -1
		m.feed.gen++
	}
	return feedRead(m.deps, m.feed, chatID)
}

// readFeedCursor reads the chat of the message the cursor is on.
func (m *Model) readFeedCursor() tea.Cmd { return m.readFeedChat(m.feedChatAt(m.msgIdx)) }

// feedReadMsg carries a chat's watermark back to the visit that read it.
type feedReadMsg struct {
	visit   *unreadFeed
	chat    store.ChatUnread
	waiting bool
}

// feedRead reads the dot the client still draws for a chat the reader read.
func feedRead(d Deps, visit *unreadFeed, chatID string) tea.Cmd {
	return func() tea.Msg {
		c, ok, err := d.Store.ChatWithUnread(context.Background(), chatID)
		if err != nil {
			d.Log.Warn("read feishu dot", "chat_id", chatID, "err", err)
		}
		return feedReadMsg{visit: visit, chat: c, waiting: ok}
	}
}

// onFeedRead asks Feishu to settle a chat read here, once per watermark:
// walking about inside a chat already read asks for nothing, a message landing
// since raises the watermark, and a refused clear waits for m rather than being
// retried on every key. A chat let go meanwhile by m or Mark All is settled by
// them. The visit the read came from is the one consulted, so a read made just
// before the reader left still reaches Feishu.
func (m Model) onFeedRead(msg feedReadMsg) (Model, tea.Cmd) {
	asked, held := msg.visit.readHere[msg.chat.ChatID]
	if !msg.waiting || !held || msg.chat.Position <= asked {
		return m, nil
	}
	msg.visit.readHere[msg.chat.ChatID] = msg.chat.Position
	return m.pushClears([]store.ChatUnread{msg.chat})
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
