package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// cursorRestDelay is how long the cursor has to stand still before the chat
// under it is opened. Autorepeat on a held j lands about every 30ms, so a
// sweep down the list collapses into the one chat it ends on. A cursor that
// was already at rest opens what it lands on at once, so a single j costs no
// delay at all.
const cursorRestDelay = 150 * time.Millisecond

// chatRestMsg fires once the cursor has stood still for cursorRestDelay.
type chatRestMsg struct{ chatID string }

// moveToChat opens the chat the cursor landed on, or arms a timer when the
// cursor is still moving. Opening on every move spends a message page, three
// detail queries and a read_state write on each row swept past, and the chat
// guard in messagesLoadedMsg then throws away all but the last of them.
func (m *Model) moveToChat(now time.Time) tea.Cmd {
	moving := now.Sub(m.cursorMovedAt) < cursorRestDelay
	m.cursorMovedAt = now
	chatID := m.highlightedChat()
	if chatID == "" {
		return nil
	}
	if !moving {
		return m.openChat(chatID)
	}
	return tea.Tick(cursorRestDelay, func(time.Time) tea.Msg { return chatRestMsg{chatID} })
}

// claimChatOpen reports whether the chat a rest timer named is still the one
// the cursor sits on. A sweep arms one timer per row, and only the row it came
// to rest on gets to spend a page on it.
func (m Model) claimChatOpen(chatID string) bool {
	return chatID != "" && m.highlightedChat() == chatID
}

// highlightedChat names the chat under the cursor that is not already open or
// on its way, which is the only one worth spending a page on. It is "" when
// the cursor sits on nothing to load.
func (m Model) highlightedChat() string {
	vis := m.visibleChats()
	if len(vis) == 0 || vis[m.chatIdx].ChatID == m.openingChat() {
		return ""
	}
	return vis[m.chatIdx].ChatID
}

// chatIDAt names the chat at idx, or "" when the list does not reach it.
func chatIDAt(chats []store.Chat, idx int) string {
	if idx < 0 || idx >= len(chats) {
		return ""
	}
	return chats[idx].ChatID
}

// openingChat is the chat the cursor belongs to: the one whose page is on its
// way while it is, and the one on screen otherwise.
func (m Model) openingChat() string {
	if m.pendingChat != "" {
		return m.pendingChat
	}
	return m.chatID
}
