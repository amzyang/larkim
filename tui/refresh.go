package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// chatRefreshDelay lets the cursor pass over a chat without spending a
// lark-cli call on it: only the chat still open once it elapses is asked
// about.
const chatRefreshDelay = 400 * time.Millisecond

// chatRefreshDueMsg fires once a chat has stayed open past chatRefreshDelay.
type chatRefreshDueMsg struct{ chatID string }

// scheduleChatRefresh arms the debounce for a chat that was just opened.
func scheduleChatRefresh(chatID string) tea.Cmd {
	return tea.Tick(chatRefreshDelay, func(time.Time) tea.Msg { return chatRefreshDueMsg{chatID} })
}

// claimChatRefresh reports whether chatID is still the chat this debounce was
// armed for. Only the process holding the data-dir lock syncs, and read_state
// and the messages table belong to it alone, so a TUI reading alongside a
// daemon never asks. Nothing paces the repeats: the open chat's beat owns
// those.
func (m *Model) claimChatRefresh(chatID string) bool {
	return m.deps.Syncer != nil && chatID != "" && chatID == m.openingChat()
}

// refreshReadStatus re-asks Feishu about the chat's unread messages. It writes
// read_state, which bumps the data revision, so the watch reloads the badges;
// nothing is returned on success.
func refreshReadStatus(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := beat(chatPollTimeout)
		defer cancel()
		if _, err := d.Syncer.RefreshReadStatus(ctx, chatID); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// refreshReactions re-asks Feishu who reacted to the chat's newest messages.
// It writes reactions_json, which bumps the data revision, so the watch
// reloads the panes; nothing is returned on success.
func refreshReactions(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := beat(chatPollTimeout)
		defer cancel()
		if _, err := d.Syncer.RefreshReactions(ctx, chatID); err != nil {
			return errMsg{err}
		}
		return nil
	}
}
