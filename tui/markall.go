package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// markAllSetMsg carries the chats a mark-all will walk: what Feishu still
// reports unseen, which the local write below does not touch.
type markAllSetMsg struct {
	chats []store.ChatUnread
	err   error
}

// markAllDoneMsg closes the local write. Everything after it is best effort.
type markAllDoneMsg struct {
	chats []store.ChatUnread
	err   error
}

// startMarkAll reads what the Feishu client is still showing. That set and the
// one the local write settles are no longer the same, and only this one
// decides who gets walked.
func (m Model) startMarkAll() (tea.Model, tea.Cmd) {
	d := m.deps
	return m, func() tea.Msg {
		chats, err := d.Store.ChatsWithUnread(context.Background())
		return markAllSetMsg{chats: chats, err: err}
	}
}

// onMarkAllSet orders the walk and starts the write.
func (m Model) onMarkAllSet(msg markAllSetMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m.notify(msg.err.Error(), true), nil
	}
	if len(msg.chats) == 0 {
		return m.notify("nothing waiting in Feishu", false), nil
	}
	// The chat on screen goes last so the client comes to rest where the
	// terminal is, rather than on whichever chat the walk happened to end on.
	chats := slices.Clone(msg.chats)
	if i := slices.IndexFunc(chats, func(c store.ChatUnread) bool { return c.ChatID == m.chatID }); i >= 0 {
		here := chats[i]
		chats = append(slices.Delete(chats, i, i+1), here)
	}
	return m.notify("marking read…", false), markAllRead(m.deps, chats)
}

// markAllRead settles every waiting message locally, which is the half larkim
// can write. It runs before a single applink goes out: the durable state is
// what the reader asked for, and the client's own dots are the best-effort
// half behind it.
func markAllRead(d Deps, chats []store.ChatUnread) tea.Cmd {
	return func() tea.Msg {
		if _, err := d.Store.MarkAllRead(context.Background(), time.Now().UnixMilli()); err != nil {
			return markAllDoneMsg{err: err}
		}
		return markAllDoneMsg{chats: chats}
	}
}

// onMarkAllDone hands the written-off chats to the queue that paces them, and
// brings the panes to where the write left the store. Waiting for the watch
// would leave the press unanswered for a beat, and the reader pressed it.
func (m Model) onMarkAllDone(msg markAllDoneMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m.notify(msg.err.Error(), true), nil
	}
	// The markers are what this visit found waiting. A mark-all is the reader
	// saying none of it is, so they go with the counts rather than outliving
	// them: no page reloaded after this one can light them again.
	clear(m.dots)
	m.applinks.swept = len(msg.chats)
	m, cmd := m.pushApplinks(msg.chats)
	reload := m.reloadCurrent()
	return m.notify(sweepNote(len(msg.chats)), false), tea.Batch(cmd, reload)
}
