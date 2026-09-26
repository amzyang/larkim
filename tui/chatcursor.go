package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// cursorRestDelay is how long the cursor has to stand still before the chat
// under it is opened. Autorepeat on a held j lands about every 30ms, so a
// sweep down the list collapses into the one chat it ends on. A cursor that
// was already at rest opens what it lands on at once, so a single j costs no
// delay at all.
const cursorRestDelay = 150 * time.Millisecond

// chatRestMsg fires once the cursor has stood still for cursorRestDelay. It
// names the row by the key the list identifies one with, since a thread and
// the chat it happens in are two rows leading to the same chat.
type chatRestMsg struct{ key string }

// moveToChat opens what the cursor landed on, or arms a timer when the cursor
// is still moving. Opening on every move spends a message page, three detail
// queries and a read_state write on each row swept past, and the chat guard in
// messagesLoadedMsg then throws away all but the last of them.
func (m *Model) moveToChat(now time.Time) tea.Cmd {
	moving := now.Sub(m.cursorMovedAt) < cursorRestDelay
	m.cursorMovedAt = now
	r, ok := m.highlightedRow()
	if !ok {
		return nil
	}
	if !moving {
		return m.openRow(r, false)
	}
	key := r.key()
	return tea.Tick(cursorRestDelay, func(time.Time) tea.Msg { return chatRestMsg{key} })
}

// claimRowOpen answers with the row a rest timer named, if the cursor is still
// on it. A sweep arms one timer per row, and only the row it came to rest on
// gets to spend a page on it.
func (m Model) claimRowOpen(key string) (listRow, bool) {
	r, ok := m.highlightedRow()
	return r, ok && key != "" && r.key() == key
}

// rowAtCursor is the row the cursor stands on, open or not. Enter asks this
// rather than highlightedRow: it has to know what the row leads to even when
// the row is already open and there is nothing left to load.
func (m Model) rowAtCursor() (listRow, bool) {
	vis := m.visibleRows()
	if m.chatIdx < 0 || m.chatIdx >= len(vis) {
		return listRow{}, false
	}
	return vis[m.chatIdx], true
}

// highlightedRow is the row under the cursor that is not already open or on
// its way, which is the only one worth spending a page on. ok is false when
// the cursor sits on nothing to load.
//
// What "already open" means differs by row: a chat is the page on screen, a
// thread is the frame in the right column. The chat under a thread is often
// the open one, and the thread over it still has to be opened.
func (m Model) highlightedRow() (listRow, bool) {
	r, ok := m.rowAtCursor()
	if !ok {
		return listRow{}, false
	}
	if r.isThread() {
		return r, m.openThreadID() != r.thread.ThreadID
	}
	return r, r.chat.ChatID != m.openingChat()
}

// openRow opens what a row leads to: a chat, or the thread over the chat it
// happens in, landing on its newest reply the way a search hit lands on the
// message it named.
//
// take says the frame comes away with the focus. Walking the cursor onto a
// thread row does not take it: the reader is reading the list, and a row that
// pulled them into the right column would cost them the next j.
func (m *Model) openRow(r listRow, take bool) tea.Cmd {
	if r.isThread() {
		m.pendingSelect = pendingJump{id: r.thread.Last.MessageID, thread: r.thread.ThreadID, takeFocus: take}
	}
	return m.openChat(r.chatID())
}

// openingChat is the chat the cursor belongs to: the one whose page is on its
// way while it is, and the one on screen otherwise.
func (m Model) openingChat() string {
	if m.pendingChat != "" {
		return m.pendingChat
	}
	return m.chatID
}
