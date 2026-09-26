package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// rightKind says what the unpacked thread* fields hold. The assistant and the
// chat's card are root views with fields of their own: they speak of the whole
// chat rather than of a container, so they never cover anything and never
// enter the stack.
type rightKind int

const (
	rightNone rightKind = iota
	rightThread
	rightForward
)

// rightFrame is a frame the stack holds under the one on screen. Nothing
// measured is kept: rows and a top line number are counted against a width
// the terminal may have changed while the frame was buried, and both lists
// come back from SQLite in a millisecond.
type rightFrame struct {
	kind rightKind
	// id is what the frame was opened on: omt_… for a thread, and for a
	// forward the message whose children it lists — the bundle itself at the
	// top level, a nested bundle below. Held by id rather than index, like
	// filterPin: a sync tick replaces the list under a suspended frame.
	id string
	// root is the bundle every level of a forward belongs to, which is where
	// its children are stored and where its pictures are registered. Empty
	// for a thread.
	root string
	// sel is the message the cursor was on and top the line the viewport
	// started at, so a pop lands where the reader left rather than at the
	// tail.
	sel string
	top lineAnchor
}

// threadOpen reports that the right column is showing a message-list frame,
// whichever kind. Every pane, rebuild and scroll asks this question and no
// finer one.
func (m Model) threadOpen() bool { return m.rightKind != rightNone }

// containerOf says which frame a message opens, and rightNone for a message
// that opens none. A thread wins over a forward: a forward somebody started a
// topic on is read in the topic, where the forward is one row that opens in
// turn.
//
// root is the bundle the frame's rows are stored under. Opening a bundle from
// a chat makes it its own root; opening a nested one keeps the root it was
// found in, which the caller supplies.
func containerOf(x store.Message, root string) (kind rightKind, id, bundle string) {
	switch {
	case x.ThreadID != "":
		return rightThread, x.ThreadID, ""
	case x.MsgType == "merge_forward":
		if root == "" {
			root = x.MessageID
		}
		return rightForward, x.MessageID, root
	}
	return rightNone, "", ""
}

// frame is the visible frame, packed for the stack.
func (m Model) frame() rightFrame {
	return rightFrame{kind: m.rightKind, id: m.threadID, root: m.rightRoot,
		sel: idAt(m.thread, m.threadIdx), top: topAnchor(m.threadRows, m.thread, m.threadTop)}
}

// openRight shows a container as the column's only frame. This is the way in
// from the message pane: what stands in the column there is the container's
// sibling, not the thing that holds it, so the column starts over. Stacking
// siblings would turn Esc into a visit history — five unrelated threads, five
// presses — which is the one thing the column is not.
func (m Model) openRight(f rightFrame) (Model, tea.Cmd) {
	m.aiOpen, m.aiChan, m.infoOpen = false, nil, false
	m.rightStack = nil
	m.focus = paneThread
	return m, m.showRight(f)
}

// pushRight opens a container over the one on screen, which is what following
// a summary inside the right pane means: the frame below is what holds it.
func (m Model) pushRight(f rightFrame) (Model, tea.Cmd) {
	// Pressing the same summary twice is one frame. A reaction chip is
	// deliberately not deduplicated, because a second press takes the
	// reaction back; a second press here asks for the place it is already at.
	if m.rightKind == f.kind && m.threadID == f.id {
		m.focus = paneThread
		return m, nil
	}
	if !m.threadOpen() {
		return m.openRight(f)
	}
	// Model is a value, so the stack is cloned before it grows: appending in
	// place would let a model that was never returned write through to the
	// one that was.
	m.rightStack = append(slices.Clone(m.rightStack), m.frame())
	m.focus = paneThread
	return m, m.showRight(f)
}

// popRight uncovers the frame beneath; on the last one the column closes.
func (m Model) popRight() (Model, tea.Cmd) {
	if len(m.rightStack) == 0 {
		m.closeRight()
		if m.focus == paneThread {
			m.focus = paneMessages
		}
		return m, nil
	}
	back := m.rightStack[len(m.rightStack)-1]
	m.rightStack = slices.Clone(m.rightStack[:len(m.rightStack)-1])
	return m, m.showRight(back)
}

// closeRight empties the column: the frame on screen and every one under it.
// Focus is the caller's business — Esc walks back to the messages pane, while
// a root view keeps the pane it is taking.
func (m *Model) closeRight() {
	m.rightKind, m.threadID, m.rightRoot = rightNone, "", ""
	m.thread, m.threadBase, m.threadRows, m.threadMeta = nil, nil, nil, msgMeta{}
	m.threadIdx, m.threadTop = 0, 0
	m.rightStack, m.rightPin, m.rightNote = nil, rightFrame{}, ""
	m.layout()
}

// showRight loads a container into the unpacked fields. The old list goes
// first: a frame drawn from the list of the one it replaced would show
// another container's messages under this one's title.
func (m *Model) showRight(f rightFrame) tea.Cmd {
	m.rightKind, m.threadID, m.rightRoot = f.kind, f.id, f.root
	m.thread, m.threadBase, m.threadRows, m.threadMeta = nil, nil, nil, msgMeta{}
	m.threadIdx, m.threadTop, m.rightNote = 0, 0, ""
	// A frame that names where it wants to land — one being uncovered, or one
	// a search hit opened — says so through sel; the pin is spent on the
	// first list to arrive under it.
	m.rightPin = rightFrame{}
	if f.sel != "" {
		m.rightPin = f
	}
	m.layout()
	return m.loadRight()
}

// loadRight fetches the visible frame's list.
func (m Model) loadRight() tea.Cmd {
	switch m.rightKind {
	case rightThread:
		return loadThread(m.deps, m.threadID)
	case rightForward:
		return loadForward(m.deps, m.rightRoot, m.threadID)
	}
	return nil
}

// rightLanded says where a list that has just arrived should sit. Ordinarily
// that is where the frame already is — the cursor's message and the line the
// viewport starts at, so a reload under the reader's hand moves nothing. A
// frame just uncovered has no such place yet, so its pin supplies one, spent
// on the first list to land under it.
func (m *Model) rightLanded(msgs []store.Message) (cursor string, anchor lineAnchor, tailed bool) {
	if m.rightPin.kind == m.rightKind && m.rightPin.id == m.threadID {
		pin := m.rightPin
		m.rightPin = rightFrame{}
		m.threadIdx = max(0, indexOfID(msgs, pin.sel))
		return pin.sel, pin.top, false
	}
	return idAt(m.thread, m.threadIdx), topAnchor(m.threadRows, m.thread, m.threadTop),
		atTail(m.threadRows, m.threadTop, m.listHeight())
}

// toggleRight is the t key: open the container under the cursor, or close the
// one it names when that is already what the column shows.
func (m Model) toggleRight() (tea.Model, tea.Cmd) {
	f, ok := m.containerAtCursor()
	switch {
	case ok && m.rightKind == f.kind && m.threadID == f.id:
		return m.leaveRight(), nil
	case ok:
		return m.openContainer(m.focus, f)
	case m.threadOpen():
		// The cursor is on nothing to open, so t is read as the other half of
		// its own toggle rather than as an error: without this the only way
		// out of the column is to focus it first.
		return m.leaveRight(), nil
	}
	return m.notify("selected message opens no thread or forward", true), nil
}

// containerAtCursor names the frame the selected message opens.
func (m Model) containerAtCursor() (rightFrame, bool) {
	sel, ok := m.selected()
	if !ok {
		return rightFrame{}, false
	}
	root := ""
	if m.focus == paneThread {
		root = m.rightRoot
	}
	kind, id, bundle := containerOf(sel, root)
	return rightFrame{kind: kind, id: id, root: bundle}, kind != rightNone
}

// onForwardedChild reports that the cursor is on a message of another chat:
// a forwarded child, which this chat has no standing to answer, react to or
// recall. Feishu would take some of it and put the result where the reader
// is not looking — a reaction on somebody else's conversation.
func (m Model) onForwardedChild() bool {
	return m.focus == paneThread && m.rightKind == rightForward
}

// leaveRight closes the column and takes the focus back with it.
func (m Model) leaveRight() Model {
	m.closeRight()
	if m.focus == paneThread {
		m.focus = paneMessages
	}
	return m
}

// openContainer opens a container from the pane the reader reached it in,
// which is what decides whether the column deepens or starts over.
func (m Model) openContainer(p pane, f rightFrame) (tea.Model, tea.Cmd) {
	if p == paneThread {
		return m.pushRight(f)
	}
	return m.openRight(f)
}

// openThreadID names the thread being read, "" when no thread is open. A
// forward opened inside a thread covers it without closing it, and the thread
// underneath still has replies to fetch.
func (m Model) openThreadID() string {
	if m.rightKind == rightThread {
		return m.threadID
	}
	for _, f := range slices.Backward(m.rightStack) {
		if f.kind == rightThread {
			return f.id
		}
	}
	return ""
}
