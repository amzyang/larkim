package tui

import (
	"context"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// markAllAskMsg carries the chats a mark-all would walk, for the question
// that precedes it.
type markAllAskMsg struct {
	chats []store.ChatUnread
	err   error
}

// markAllDoneMsg closes the local write. Everything after it is best effort.
type markAllDoneMsg struct {
	chats []store.ChatUnread
	err   error
}

// startMarkAll reads what is waiting. Nothing is written yet: the reader is
// asked first, and the set has to be read before the write that erases it.
func (m Model) startMarkAll() (tea.Model, tea.Cmd) {
	// A question already on screen owns the next key, so a second press must
	// not put a second one behind it.
	if m.confirm.kind != confirmNone {
		return m, nil
	}
	d := m.deps
	return m, func() tea.Msg {
		chats, err := d.Store.ChatsWithUnread(context.Background())
		return markAllAskMsg{chats: chats, err: err}
	}
}

// onMarkAllAsk puts the count in front of the reader. Marking everything read
// cannot be undone and walks the client across every chat it names, which is
// the same reason a recall asks.
func (m Model) onMarkAllAsk(msg markAllAskMsg) (Model, tea.Cmd) {
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
	m.confirm = confirmation{kind: confirmMarkAllRead, chats: chats}
	return m.notify("mark "+strconv.Itoa(len(chats))+" chats read? y/n", false), nil
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

// onMarkAllDone hands the written-off chats to the queue that paces them.
func (m Model) onMarkAllDone(msg markAllDoneMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m.notify(msg.err.Error(), true), nil
	}
	m.applinks.swept = len(msg.chats)
	m, cmd := m.pushApplinks(msg.chats)
	return m.notify(sweepNote(len(msg.chats)), false), cmd
}
