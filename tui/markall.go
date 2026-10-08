package tui

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// markAllSetMsg carries the chats a mark-all will walk.
type markAllSetMsg struct {
	chats []store.ChatUnread
	err   error
}

// startMarkAll reads what the Feishu client is still showing.
func (m Model) startMarkAll() (tea.Model, tea.Cmd) {
	d := m.deps
	return m, func() tea.Msg {
		chats, err := d.Store.ChatsWithUnread(context.Background())
		return markAllSetMsg{chats: chats, err: err}
	}
}

// onMarkAllSet orders the walk and queues clears.
func (m Model) onMarkAllSet(msg markAllSetMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m.notify(msg.err.Error(), true), nil
	}
	// Every chat is done, the ones held on the Unread page because they were
	// read there included. With clears to make, each settled one raises the
	// revision that reads the page again; a page read here, before them, would
	// put back the markers this press drops. With none, nothing else will.
	if m.feed != nil {
		clear(m.feed.readHere)
	}
	if len(msg.chats) == 0 {
		var reload tea.Cmd
		if m.feed != nil {
			reload = m.reloadCurrent()
		}
		return m.notify("nothing waiting in Feishu", false), reload
	}
	chats := slices.Clone(msg.chats)
	if i := slices.IndexFunc(chats, func(c store.ChatUnread) bool { return c.ChatID == m.chatID }); i >= 0 {
		here := chats[i]
		chats = append(slices.Delete(chats, i, i+1), here)
	}
	clear(m.dots)
	m.clears.swept = len(chats)
	m, cmd := m.pushClears(chats)
	return m.notify("marking read…", false), cmd
}
