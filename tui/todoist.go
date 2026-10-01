package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/todoist"
)

// taskContentMax is where a task's title stops. Todoist takes far more, but
// the title is read in a list at a glance and the description carries the
// link back to the whole message.
const taskContentMax = 120

// todoistTask files what the selection names: the message under the cursor in
// a message pane, the chat under it in the list. The chat pane is decided
// first because selected() answers for it too — with the open chat's
// last-highlighted message, which the reader is not looking at.
func (m Model) todoistTask() (tea.Model, tea.Cmd) {
	if m.deps.Todoist == nil {
		return m.notify("todoist not configured", false), nil
	}
	var task todoist.Task
	if m.focus == paneChats {
		r, ok := m.rowAtCursor()
		if !ok || r.isFeed() {
			return m.notify("no chat under the cursor", false), nil
		}
		task = todoist.Task{Content: truncate(flatten(r.chat.Name), taskContentMax),
			Description: applink.ChatLink(r.chatID(), "", 0)}
	} else {
		sel, ok := m.selected()
		if !ok {
			return m.notify("no message under the cursor", false), nil
		}
		task = todoist.Task{Content: taskExcerpt(sel),
			Description: applink.ChatLink(sel.ChatID, sel.MessageID, sel.MessagePosition)}
	}
	return m, sendTodoist(m.deps, task)
}

// taskExcerpt is the message pressed onto one line: its rendered body with
// attachments named the way the chat list names them (gistBody), the first
// line of it only — a task titled by a post's seventh paragraph says nothing
// — flattened and cut.
func taskExcerpt(m store.Message) string {
	line, _, _ := strings.Cut(m.Content, "\n")
	return truncate(flatten(gistBody(line)), taskContentMax)
}

// sendTodoist makes the one request and answers for it in the notice bar.
func sendTodoist(d Deps, task todoist.Task) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		t, err := d.Todoist.CreateTask(ctx, task)
		if err != nil {
			// The notice bar holds the message and is gone at the next
			// keypress; what was being filed only exists here.
			d.log().Error("todoist task", "err", err, "content", task.Content)
			return errMsg{err}
		}
		return noticeMsg{"todoist: " + t.Content}
	}
}
