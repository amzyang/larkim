package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// waitingFor reports whether a chat is one the reader still owes an answer.
// Muted chats are out, on the same rule the pane header counts by: silencing a
// chat is a request not to be pulled by it, and being walked onto it is that
// pull.
func waitingFor(c store.Chat, unread map[string]int64) bool {
	return !c.Muted && unread[c.ChatID] > 0
}

// nextUnread is the index of the chat holding something to read, starting one
// step from cur in the direction step and wrapping once. It returns -1 when
// the list holds nothing waiting.
//
// The scan starts past the cursor and covers every other row exactly once, so
// the chat under the cursor is the last one considered rather than the first:
// pressing the key on a chat that still has something waiting moves on to the
// next, which is what clearing a queue means.
func nextUnread(chats []store.Chat, unread map[string]int64, cur, step int) int {
	n := len(chats)
	if n == 0 {
		return -1
	}
	for i := 1; i <= n; i++ {
		at := ((cur+step*i)%n + n) % n
		if waitingFor(chats[at], unread) {
			return at
		}
	}
	return -1
}

// jumpUnread walks the cursor to the next chat with something waiting and
// opens it. The chats pane takes focus whatever had it, because the reader
// asked to be somewhere else and the panes have to agree on where that is.
func (m Model) jumpUnread(step int) (tea.Model, tea.Cmd) {
	vis := m.visibleChats()
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
	return m.notify("", false), m.openChat(vis[at].ChatID)
}
