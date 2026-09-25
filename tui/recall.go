package tui

import (
	tea "charm.land/bubbletea/v2"
)

// recalledMsg closes a recall. id names the message so the row can be brought
// back into step without waiting for the next sync tick.
type recalledMsg struct {
	messageID string
	err       error
}

// recallCmd takes a message back and re-ingests it, so the row turns to
// (Recalled) at once rather than on the next tick.
func recallCmd(d Deps, messageID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		if err := d.Client.Recall(ctx, messageID); err != nil {
			return recalledMsg{messageID: messageID, err: err}
		}
		return recalledMsg{messageID: messageID, err: ingestMessage(d, messageID)}
	}
}

// askRecall arms the confirmation. A recall is visible to everybody who was in
// the chat and cannot be undone, so it is the one action here that asks first —
// the client asks too.
func (m Model) askRecall() (tea.Model, tea.Cmd) {
	x, ok := m.selected()
	if !ok {
		return m.notify("select a message to recall", true), nil
	}
	if x.SenderID != m.deps.Self {
		return m.notify("only your own messages can be recalled", true), nil
	}
	if x.Deleted {
		return m.notify("that message is already recalled", true), nil
	}
	if m.outboxAt(x.MessageID) != nil {
		return m.notify("that message has not reached Feishu yet", true), nil
	}
	m.confirm = confirmation{kind: confirmRecall, messageID: x.MessageID}
	return m.notify("recall this message? y/n", false), nil
}

// confirmKind is what a pending confirmation will do when it is answered.
type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmRecall
)

// confirmation is an action waiting on y or n. A zero value is nothing
// pending, so the ordinary key path is untouched while none is.
type confirmation struct {
	kind      confirmKind
	messageID string
}

// answerConfirm handles the key a pending confirmation is waiting on. ok is
// false when nothing was pending, which leaves the key to the ordinary path.
func (m Model) answerConfirm(key string) (tea.Model, tea.Cmd, bool) {
	if m.confirm.kind == confirmNone {
		return m, nil, false
	}
	pending := m.confirm
	m.confirm = confirmation{}
	if key != "y" {
		// Anything but y cancels, rather than only n: this is the answer where
		// a slip costs something everybody in the chat can see.
		return m.notify("", false), nil, true
	}
	switch pending.kind {
	case confirmRecall:
		return m.notify("recalling…", false), recallCmd(m.deps, pending.messageID), true
	}
	return m, nil, true
}
