package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
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

// recallable is the message a recall would take back, or the sentence saying
// why it cannot. A re-edit asks the same questions because a recall is what it
// runs; verb and done name the action in the two answers that mention it.
func (m Model) recallable(verb, done string) (store.Message, string) {
	if m.onForwardedChild() {
		return store.Message{}, "a forwarded message belongs to its own chat"
	}
	x, ok := m.selected()
	if !ok {
		return store.Message{}, "select a message to " + verb
	}
	if x.SenderID != m.deps.Self {
		return store.Message{}, "only your own messages can be " + done
	}
	if x.Deleted {
		return store.Message{}, "that message is already recalled"
	}
	if m.outboxAt(x.MessageID) != nil {
		return store.Message{}, "that message has not reached Feishu yet"
	}
	return x, ""
}

// askRecall arms the confirmation. A recall is visible to everybody who was in
// the chat and cannot be undone, so it is the one action here that asks first —
// the client asks too.
func (m Model) askRecall() (tea.Model, tea.Cmd) {
	x, bad := m.recallable("recall", "recalled")
	if bad != "" {
		return m.notify(bad, true), nil
	}
	m.confirm = confirmation{kind: confirmRecall, messageID: x.MessageID}
	return m.notify("recall this message? y/n", false), nil
}

// confirmKind is what a pending confirmation will do when it is answered.
type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmRecall
	confirmReEdit
	// confirmDeleteAI drops an assistant session and its turns.
	confirmDeleteAI
	// confirmAISend puts one of the assistant's cards on the wire.
	confirmAISend
	// confirmAIStream asks a question whose answer streams into the chat.
	confirmAIStream
)

// confirmation is an action waiting on y or n. A zero value is nothing
// pending, so the ordinary key path is untouched while none is.
type confirmation struct {
	kind      confirmKind
	messageID string
	// aiSession is the assistant session a confirmDeleteAI names.
	aiSession string
	// aiSend is the card a confirmAISend is about. When its text names a
	// local file, n is an answer too: send without the upload.
	aiSend *aiSendPending
	// aiStream is the question a confirmAIStream asks, held because the box
	// it was typed in can move on before y lands.
	aiStream string
}

// answerConfirm handles the key a pending confirmation is waiting on. ok is
// false when nothing was pending, which leaves the key to the ordinary path.
func (m Model) answerConfirm(key string) (tea.Model, tea.Cmd, bool) {
	if m.confirm.kind == confirmNone {
		return m, nil, false
	}
	pending := m.confirm
	m.confirm = confirmation{}
	// Anything but y cancels, rather than only n: these are the answers
	// where a slip costs something no undo reaches. The one exception is a
	// card whose text names a local file — there n says send it without the
	// upload, and only Esc says don't send.
	if key != "y" && !(pending.kind == confirmAISend && pending.aiSend.file && key == "n") {
		m.reEdit = nil
		return m.notify("", false), nil, true
	}
	switch pending.kind {
	case confirmRecall, confirmReEdit:
		// One call for both: a re-edit is a recall whose answer also refills
		// the composer, which the recall's own reply is what triggers.
		return m.notify("recalling…", false), recallCmd(m.deps, pending.messageID), true
	case confirmDeleteAI:
		next, cmd := m.deleteAI(pending.aiSession)
		return next, cmd, true
	case confirmAISend:
		withFile := key == "y"
		next, cmd := m.aiSendCard(*pending.aiSend, withFile)
		return next, cmd, true
	case confirmAIStream:
		next, cmd := m.askAI(pending.aiStream, "", true)
		return next, cmd, true
	}
	return m, nil, true
}
