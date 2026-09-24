package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// reactionRefreshDelay lets the cursor pass over a chat without spending a
	// lark-cli call on it: only the chat still open once it elapses is asked
	// about.
	reactionRefreshDelay = 400 * time.Millisecond
	// reactionRefreshCooldown bounds how often one chat is re-asked. Unlike
	// read status there is no answer that settles the question — a message can
	// gain a reaction at any time — so every revisit would otherwise spend a
	// call.
	reactionRefreshCooldown = 30 * time.Second
)

// reactionRefreshDueMsg fires once a chat has stayed open past the debounce.
type reactionRefreshDueMsg struct{ chatID string }

// scheduleReactionRefresh arms the debounce for a chat that was just opened.
func scheduleReactionRefresh(chatID string) tea.Cmd {
	return tea.Tick(reactionRefreshDelay, func(time.Time) tea.Msg { return reactionRefreshDueMsg{chatID} })
}

// claimReactionRefresh reports whether chatID may be re-asked about now, and
// records the attempt when it may. Only the process holding the data-dir lock
// syncs, and the messages table belongs to it alone, so a TUI reading
// alongside a daemon never asks.
func (m *Model) claimReactionRefresh(chatID string, now time.Time) bool {
	if m.deps.Syncer == nil || chatID == "" || chatID != m.openingChat() {
		return false
	}
	if now.Sub(m.reactionRefreshed[chatID]) < reactionRefreshCooldown {
		return false
	}
	m.reactionRefreshed[chatID] = now
	return true
}

// refreshReactions re-asks Feishu who reacted to the chat's newest messages.
// It writes reactions_json, which bumps the data revision, so the watch
// reloads the panes; nothing is returned on success.
func refreshReactions(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := d.Syncer.RefreshReactions(ctx, chatID); err != nil {
			return errMsg{err}
		}
		return nil
	}
}
