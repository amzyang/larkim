package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// readRefreshDelay lets the cursor pass over a chat without spending a
	// lark-cli call on it: only the chat still open once it elapses is asked
	// about.
	readRefreshDelay = 400 * time.Millisecond
	// readRefreshCooldown bounds how often one chat is re-asked. A chat whose
	// messages Feishu genuinely holds as unread stays a candidate forever,
	// and without this every revisit would spend a call on it.
	readRefreshCooldown = 30 * time.Second
)

// readRefreshDueMsg fires once a chat has stayed open past readRefreshDelay.
type readRefreshDueMsg struct{ chatID string }

// scheduleReadRefresh arms the debounce for a chat that was just opened.
func scheduleReadRefresh(chatID string) tea.Cmd {
	return tea.Tick(readRefreshDelay, func(time.Time) tea.Msg { return readRefreshDueMsg{chatID} })
}

// claimReadRefresh reports whether chatID may be re-asked about now, and
// records the attempt when it may. Only the process holding the data-dir lock
// syncs, and read_state belongs to it alone, so a TUI reading alongside a
// daemon never asks.
func (m *Model) claimReadRefresh(chatID string, now time.Time) bool {
	if m.deps.Syncer == nil || chatID == "" || chatID != m.openingChat() {
		return false
	}
	if now.Sub(m.readRefreshed[chatID]) < readRefreshCooldown {
		return false
	}
	m.readRefreshed[chatID] = now
	return true
}

// refreshReadStatus re-asks Feishu about the chat's unread messages. It writes
// read_state, which bumps the data revision, so the watch reloads the badges;
// nothing is returned on success.
func refreshReadStatus(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := d.Syncer.RefreshReadStatus(ctx, chatID); err != nil {
			return errMsg{err}
		}
		return nil
	}
}
