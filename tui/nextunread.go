package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// waitingFor reports whether a row is one the reader still owes an answer.
// Muted rows are out, on the same rule the pane header counts by: silencing a
// chat is a request not to be pulled by it, and being walked onto it is that
// pull. A thread inherits the silence of the chat it happens in.
func waitingFor(r listRow, unread map[string]int64) bool {
	return !r.chat.Muted && r.unread(unread) > 0
}

// nextUnread is the index of the row holding something to read, starting one
// step from cur in the direction step and wrapping once. It returns -1 when
// the list holds nothing waiting.
//
// The scan starts past the cursor and covers every other row exactly once, so
// the row under the cursor is the last one considered rather than the first:
// pressing the key on a row that still has something waiting moves on to the
// next, which is what clearing a queue means.
func nextUnread(rows []listRow, unread map[string]int64, cur, step int) int {
	n := len(rows)
	if n == 0 {
		return -1
	}
	for i := 1; i <= n; i++ {
		at := ((cur+step*i)%n + n) % n
		if waitingFor(rows[at], unread) {
			return at
		}
	}
	return -1
}

// jumpUnread walks the cursor to the next row with something waiting and opens
// it. The chats pane takes focus whatever had it, because the reader asked to
// be somewhere else and the panes have to agree on where that is.
func (m Model) jumpUnread(step int) (tea.Model, tea.Cmd) {
	vis := m.visibleRows()
	at := nextUnread(vis, m.unread, m.chatIdx, step)
	if at < 0 {
		return m.notify("nothing unread", false), nil
	}
	m.focus = paneChats
	m.chatIdx = at
	m.clampChat()
	m.scrollChatToCursor()
	// Opened outright rather than through the cursor's rest timer: this is one
	// deliberate jump, not a sweep down the list, so there is nothing to
	// coalesce and waiting would only make the key feel slow.
	m.cursorMovedAt = time.Now()
	cmd := m.openRow(vis[at], false)
	return m.notify("", false), cmd
}
