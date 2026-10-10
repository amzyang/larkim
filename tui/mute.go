package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// chatMutedMsg closes a mute toggle. muted is the value applied on success.
type chatMutedMsg struct {
	chatID string
	muted  bool
	err    error
}

func setChatMutedCmd(d Deps, chatID string, muted bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(begin("mute"), sendTimeout)
		defer cancel()
		if err := d.Client.SetChatMuted(ctx, chatID, muted); err != nil {
			return chatMutedMsg{chatID: chatID, muted: muted, err: err}
		}
		if err := d.Store.SetMuteStatus(context.Background(), map[string]bool{chatID: muted}, nil, time.Now().UnixMilli()); err != nil {
			return chatMutedMsg{chatID: chatID, muted: muted, err: err}
		}
		return chatMutedMsg{chatID: chatID, muted: muted}
	}
}
