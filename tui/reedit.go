package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// reEditTypes are the types the composer can take back. Feishu's own edit
// covers the same two; a picture, an attachment or a card has no source the
// composer could hold.
var reEditTypes = []string{"text", "post"}

// reEditPending is what the composer is refilled with once Feishu has taken
// the message back. The text is captured before the call because a recall
// drops the body, and the rendering goes with it.
type reEditPending struct {
	messageID string
	text      string
	replyTo   *store.Message
	inThread  bool
}

// askReEdit arms the confirmation. Feishu's edit API takes bot identity only
// and demands the caller be the sender, so a message the reader sent can be
// reached by neither identity and cannot be rewritten in place; recalling it
// and writing the text again is the client's own correction path.
//
// The guards are recall's, because the recall is what runs, plus the two the
// refill needs.
func (m Model) askReEdit() (tea.Model, tea.Cmd) {
	x, bad := m.recallable("re-edit", "re-edited")
	if bad != "" {
		return m.notify(bad, true), nil
	}
	if !slices.Contains(reEditTypes, x.MsgType) {
		return m.notify("only text and post messages can be re-edited", true), nil
	}
	// The rendering is what the composer takes: the raw body spells mentions
	// as @_user_1 placeholders, and a post as style runs rather than markdown.
	if x.RenderedAt == 0 || x.Content == "" {
		return m.notify("that message has no text to take back yet", true), nil
	}
	m.confirm = confirmation{kind: confirmReEdit, messageID: x.MessageID}
	m.reEdit = &reEditPending{messageID: x.MessageID, text: x.Content,
		replyTo: m.answered(x), inThread: x.ThreadID != "" && x.MessagePosition < 0}
	return m.notify("recall and re-edit? y/n", false), nil
}

// answered is the message the one being re-edited replied to, so the resend
// lands where it was. Only the loaded page is searched — a parent further back
// is not worth a call, and a recalled one is nothing left to quote, so either
// way the text goes to the chat instead.
func (m Model) answered(x store.Message) *store.Message {
	if x.ReplyTo == "" {
		return nil
	}
	i := indexOfID(m.msgs, x.ReplyTo)
	if i < 0 || m.msgs[i].Deleted {
		return nil
	}
	return new(m.msgs[i])
}

// fillReEdit puts the recalled text back in the composer, aimed where the
// message was, so the correction is typed over the original rather than from
// scratch. A post comes back as the renderer's markdown rather than the
// source that was sent, which is why it lands here to be read before it goes
// out again.
func (m Model) fillReEdit(p reEditPending) (tea.Model, tea.Cmd) {
	next, insert := m.startInsert(p.replyTo, p.inThread)
	m = next.(Model)
	m.areap().SetValue(p.text)
	m.areap().CursorEnd()
	m.replan()
	m.layout()
	reload := m.reloadCurrent()
	return m, tea.Batch(insert, reload)
}
