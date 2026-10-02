package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"github.com/amzyang/larkim/store"
)

// composerSide names which of the writing areas the keys go to: the one
// under the message panes, the one the right column carries, or the assistant
// panel's own. The client gives its Thread sidebar an input of its own so a
// chat's half-written message and an answer inside a thread can both exist;
// the assistant's box is that again, with one band height shared between the
// boxes so the panes above them end on the same row.
type composerSide int

const (
	sideMain composerSide = iota
	sideRight
	sideAI
)

// threadPrompt is the client's own placeholder for the Thread sidebar's input.
// It stands in the box only while there is nothing to draw over it, which is
// also the only time the box is answering the thread rather than a message in
// it — the quote takes the row otherwise.
const threadPrompt = "Reply to thread"

// replyPrompt heads the same placeholder in a reply tree, which the client has
// no input for at all. The sender's name follows: an answer written here lands
// in the chat's flow beside the message the tree grew under, and unlike a
// thread there is no one word for where that is.
const replyPrompt = "Reply to "

// newComposer is the writing area both boxes are built from. bubbles' own
// default is the dark palette with a black cursor line, so the theme's
// composer styles go on at birth: the assistant's box is built long after the
// background was learned, and without this it would keep painting black.
func newComposer(dark bool) textarea.Model {
	ta := textarea.New()
	ta.SetStyles(composerStyles(dark))
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	ta.SetHeight(inputHeight)
	// The terminal's own cursor carries the mode, so bubbles must stop drawing
	// its reverse-video stand-in: a virtual cursor has no shape to change.
	ta.SetVirtualCursor(false)
	return ta
}

// area is the writing area the keys go to.
func (m Model) area() textarea.Model {
	switch m.side {
	case sideRight:
		return m.rightInput
	case sideAI:
		if m.aiP != nil {
			return m.aiP.input
		}
	}
	return m.input
}

// areap is area for the callers that write into it.
func (m *Model) areap() *textarea.Model {
	switch m.side {
	case sideRight:
		return &m.rightInput
	case sideAI:
		if m.aiP != nil {
			return &m.aiP.input
		}
	}
	return &m.input
}

// rightHasComposer says the frame in the right column is one an answer can be
// written into. The assistant panel over the column carries its own box
// instead, so while it is open the frame's is not on screen — hidden, not
// closed, and its draft waits under it. The client only puts an input in its
// Thread sidebar; its reply Details pane has none, and larkim gives that frame
// one anyway because the Details frame is where a reply tree is read, and an
// answer written from it lands in the chat's flow beside the message it
// answers. A forwarded bundle belongs to another chat and is refused outright.
func (m Model) rightHasComposer() bool {
	return !m.aiOpen() && (m.rightKind == rightThread || m.rightKind == rightReply)
}

// frameRoot is the message the visible frame's conversation started from: a
// thread's root, or the message a reply tree grew under.
func (m Model) frameRoot() (store.Message, bool) {
	switch m.rightKind {
	case rightThread:
		// The root is the one member of a thread that is not a reply; the
		// sign of the position is the only thing that says so.
		for _, x := range m.thread {
			if x.MessagePosition >= 0 {
				return x, true
			}
		}
	case rightReply:
		if i := indexOfID(m.thread, m.threadID); i >= 0 {
			return m.thread[i], true
		}
	}
	if len(m.thread) > 0 {
		return m.thread[0], true
	}
	return store.Message{}, false
}

// quotedOn is the message a box draws a quote for: the one the reader aimed it
// at, and none until they do. Where an unaimed box sends is its frame's
// business, which rightTarget answers and the placeholder says out loud; a bar
// the reader never asked for would be one ^r cannot take back. The assistant's
// box answers no message: what it is about is the panel's context strip.
func (m Model) quotedOn(s composerSide) (store.Message, bool) {
	switch s {
	case sideRight:
		if m.rightReply == nil {
			return store.Message{}, false
		}
		return *m.rightReply, true
	case sideAI:
		return store.Message{}, false
	}
	if m.replyTo == nil {
		return store.Message{}, false
	}
	return *m.replyTo, true
}

// rightTarget is the message an answer written in the right box replies to.
// Unlike quotedOn it never comes back empty for a thread: the reply has to name
// a message even when the reader only meant the thread.
func (m Model) rightTarget() (store.Message, bool) {
	if m.rightReply != nil {
		return *m.rightReply, true
	}
	return m.frameRoot()
}

// inThreadOn says an answer written in this box lands inside a thread.
func (m Model) inThreadOn(s composerSide) bool {
	switch s {
	case sideRight:
		return m.rightKind == rightThread
	case sideAI:
		return false
	}
	return m.inThrd
}

// splitBand reports that more than one box is drawn, which is the only time
// the reader can see one while writing in another.
func (m Model) splitBand() bool { return (m.aiOpen() || m.rightHasComposer()) && !m.foldRight() }

// bandWidth is the width of one box, borders included. A box is exactly as
// wide as the pane it stands under.
func (m Model) bandWidth(s composerSide) int {
	if s == sideRight || s == sideAI || m.foldRight() {
		return m.rightWidth()
	}
	return m.messagesWidth()
}

// bandLeft is the screen column a box starts at.
func (m Model) bandLeft(s composerSide) int {
	if s == sideRight || s == sideAI {
		return m.width - m.rightWidth()
	}
	return chatsWidth
}

// bandAt names the box drawn at a screen column, and whether one is drawn
// there at all: the chats pane has none, and neither has a right column
// showing something that carries no box of its own.
func (m Model) bandAt(x int) (composerSide, bool) {
	switch {
	case x < chatsWidth:
		return sideMain, false
	case m.rightOpen() && x >= m.width-m.rightWidth():
		switch {
		case m.aiOpen():
			return sideAI, true
		case m.rightHasComposer():
			return sideRight, true
		}
		// Folded, the right column stands where the messages pane was, so the
		// box under it is the chat's.
		return sideMain, m.foldRight()
	}
	return sideMain, true
}

// cmdSide is the box the : line, the / filter and the ⌕ search stand in. They
// speak for the whole program rather than for a conversation, so they take the
// box under the messages pane — or, where the right column has taken that
// pane's place, the only box on screen.
func (m Model) cmdSide() composerSide {
	switch {
	case m.aiOpen():
		if m.foldRight() {
			return sideAI
		}
	case m.foldRight() && m.rightHasComposer():
		return sideRight
	}
	return sideMain
}

// pickSide is which box i, r and Enter write into: the one under the column
// the reader is in. A press that comes from a box keeps that box.
func (m *Model) pickSide() {
	switch {
	case m.focus == paneThread && m.aiOpen():
		m.side = sideAI
	case m.focus == paneThread && m.rightHasComposer():
		m.side = sideRight
	case m.focus != paneInput:
		m.side = sideMain
	}
}

// holdSide sends the keys back to a box that is on screen. A frame with no
// input of its own leaves only the chat's box, and a folded column covers the
// chat's box with its own. The assistant panel is a column of its own: its box
// is on screen while it is open, and the frame it covers loses its own for
// that while.
func (m *Model) holdSide() {
	switch {
	case m.side == sideAI && !m.aiOpen():
		m.side = sideMain
	case m.aiOpen():
		switch {
		case m.side == sideRight:
			m.side = sideMain
		case m.foldRight() && m.side == sideMain:
			m.side = sideAI
		}
	case !m.rightHasComposer():
		m.side = sideMain
	case m.foldRight() && m.side == sideMain:
		m.side = sideRight
	}
}

// setQuote points the box being written in at the message it answers, nil for
// none.
func (m *Model) setQuote(replyTo *store.Message, inThread bool) {
	m.setQuoteOn(m.side, replyTo, inThread)
}

// setQuoteOn is setQuote with the box named, for the callers that mean the
// chat's box whatever has the keys. The quote takes a row of its own, so every
// pane above it is re-laid out. The assistant's box takes no quote at all;
// ctrl+r there drops a context chip instead.
func (m *Model) setQuoteOn(s composerSide, replyTo *store.Message, inThread bool) {
	switch s {
	case sideRight:
		m.rightReply = replyTo
	case sideAI:
		return
	default:
		m.replyTo, m.inThrd = replyTo, inThread
	}
	m.layout()
}
