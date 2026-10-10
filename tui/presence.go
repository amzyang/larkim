package tui

import (
	tea "charm.land/bubbletea/v2"
)

// openFromBannerMsg is a desktop banner's click arriving through the presence
// socket: the reader asked to be taken to chat, from outside the TUI. The
// Lark client's own banner does the same thing to the client — it opens the
// chat whatever the window was showing.
type openFromBannerMsg struct{ chat string }

// openFromBanner opens chat the way :goto does. Insert and the : line are put
// away as Esc would, the draft kept with its chat; an overlay the reader is
// working in is not torn down from outside, and is told about the chat
// instead.
func (m Model) openFromBanner(chat string) (tea.Model, tea.Cmd) {
	if m.config.open || m.help.open {
		return m.notify("banner: "+chat+" — close this panel to open it", false), nil
	}
	var keep tea.Cmd
	switch m.mode {
	case modeInsert:
		m, keep = m.leaveInsert()
	case modeCommand:
		m = m.leaveCommand()
	case modeNormal:
	default:
		return m.notify("banner: "+chat+" — press Esc to open it", false), nil
	}
	m, focus := m.focusMessages()
	cmd := m.openChat(chat)
	return m, tea.Batch(keep, focus, cmd)
}

// reportPresence puts what the socket says in step with the model.
func (m Model) reportPresence() {
	if m.deps.Presence != nil {
		m.deps.Presence.Set(m.focused, m.chatID)
	}
}
