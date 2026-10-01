package tui

import (
	"cmp"
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
	rightReply
)

// rightFrame is a frame the stack holds under the one on screen. Nothing
// measured is kept: rows and a top line number are counted against a width
// the terminal may have changed while the frame was buried, and both lists
// come back from SQLite in a millisecond.
type rightFrame struct {
	kind rightKind
	// id is what the frame was opened on: omt_… for a thread, for a forward
	// the message whose children it lists — the bundle itself at the top
	// level, a nested bundle below — and for a reply tree the message the
	// conversation started from. Held by id rather than index, like
	// filterPin: a sync tick replaces the list under a suspended frame.
	id string
	// root is the bundle every level of a forward belongs to, which is where
	// its children are stored and where its pictures are registered. Empty
	// for a thread.
	root string
	// name titles the frame. It is carried rather than recomputed because
	// whoever opened the frame had already drawn it on the summary, so the
	// header is right from the first paint instead of after the list lands.
	name string
	// sel is the message the frame should stand on and top the line the
	// viewport should start at, so a pop lands where the reader left rather
	// than at the tail. A frame opened on a message the reader named carries
	// sel alone: it says where to stand, not which lines were on screen under
	// a width and a list it never saw.
	sel string
	top lineAnchor
}

// rightLanding is where a list that has just arrived puts the cursor and the
// viewport. It is decided before the list is assigned, from the state of the
// frame being replaced, and applied after — the outbox is spliced in between,
// so the newest row is not known until then.
type rightLanding struct {
	// cursor is the message to stand on, "" when the landing names none.
	cursor string
	// anchor is the line the viewport held and tail whether it was at the
	// bottom, which together are what keeps a reload under the reader's hand
	// from moving.
	anchor lineAnchor
	tail   bool
	// atEnd asks for the newest row rather than a named one. It cannot be
	// expressed as a cursor id: m.thread is the loaded list plus whatever the
	// outbox still holds, so the newest row may be a send the landing never
	// saw.
	atEnd bool
	// reveal centres the pane on the cursor. A frame that named a message but
	// no line has a viewport still at 0, where minimal scrolling would leave
	// the message against the bottom edge with the answers under it off
	// screen.
	reveal bool
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
		return rightForward, x.MessageID, cmp.Or(root, x.MessageID)
	}
	return rightNone, "", ""
}

// frame is the visible frame, packed for the stack.
func (m Model) frame() rightFrame {
	return rightFrame{kind: m.rightKind, id: m.threadID, root: m.rightRoot, name: m.rightName,
		sel: idAt(m.thread, m.threadIdx), top: topAnchor(m.threadRows, m.thread, m.threadTop)}
}

// openRight shows a container as the column's only frame. This is the way in
// from the message pane: what stands in the column there is the container's
// sibling, not the thing that holds it, so the column starts over. Stacking
// siblings would turn Esc into a visit history — five unrelated threads, five
// presses — which is the one thing the column is not.
func (m Model) openRight(f rightFrame) (Model, tea.Cmd) {
	return m.openRightIn(f, paneThread)
}

// openRightIn is openRight with the pane the reader comes away in named. A
// thread the chats cursor walked onto opens beside a reader still reading the
// list, the way walking onto a chat opens its page without leaving the list;
// only a container the reader asked for opens around them.
func (m Model) openRightIn(f rightFrame, p pane) (Model, tea.Cmd) {
	// The assistant column hides rather than stopping: its answers belong to
	// their sessions and go on landing in them.
	if m.aiP != nil {
		m.aiP.open = false
	}
	m.infoOpen = false
	m.rightStack = nil
	m.focus = p
	cmd := m.showRight(f)
	return m, cmd
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
	cmd := m.showRight(f)
	return m, cmd
}

// popRight uncovers the frame beneath; on the last one the column closes.
func (m Model) popRight() (Model, tea.Cmd) {
	if len(m.rightStack) == 0 {
		return m.leaveRight()
	}
	back := m.rightStack[len(m.rightStack)-1]
	m.rightStack = slices.Clone(m.rightStack[:len(m.rightStack)-1])
	cmd := m.showRight(back)
	return m, cmd
}

// closeRight empties the column: the frame on screen and every one under it.
// Focus is the caller's business — Esc walks back to the messages pane, while
// a root view keeps the pane it is taking.
func (m *Model) closeRight() tea.Cmd {
	keep := m.saveRightBox()
	m.rightKind, m.threadID, m.rightRoot = rightNone, "", ""
	m.thread, m.threadBase, m.threadRows, m.threadMeta = nil, nil, nil, msgMeta{}
	m.threadIdx, m.threadTop = 0, 0
	m.rightStack, m.rightPin, m.rightNote, m.rightName = nil, rightFrame{}, "", ""
	m.emptyRightBox()
	m.layout()
	return keep
}

// emptyRightBox clears the box the column carries and arms it for the frame
// about to stand there. A draft belongs to the frame it was written under, so
// it neither follows the reader into the next frame nor waits in a column
// showing another conversation.
func (m *Model) emptyRightBox() {
	m.rightInput.Reset()
	m.rightReply = nil
	m.rightDraftPin = true
}

// saveRightBox writes the column's box back under the frame standing in it. A
// frame that holds no box — a forwarded bundle, the assistant, the chat's own
// card — has nothing to write.
func (m Model) saveRightBox() tea.Cmd {
	if !m.rightHasComposer() || m.chatID == "" {
		return nil
	}
	replyTo := ""
	if m.rightReply != nil {
		replyTo = m.rightReply.MessageID
	}
	return saveDraft(m.deps, store.Draft{ChatID: m.chatID, FrameID: m.threadID,
		Text: m.rightInput.Value(), ReplyTo: replyTo, InThread: m.rightKind == rightThread})
}

// restoreRightDraft fills the column's box from the frame being entered. The
// quote is put back with the text, since a draft that answers something is
// only that draft while it still says what it answers.
func (m *Model) restoreRightDraft(d store.Draft, msgs []store.Message) {
	m.rightInput.SetValue(d.Text)
	m.rightInput.MoveToEnd()
	m.rightReply = nil
	if d.ReplyTo != "" {
		if i := indexOfID(msgs, d.ReplyTo); i >= 0 {
			m.rightReply = new(msgs[i])
		}
	}
	m.layout()
}

// takeRightDraft spends the pin the frame was opened with. A reload has to
// leave the box alone: the reader may have cleared it since, and a draft is
// only deleted when it is written back.
func (m *Model) takeRightDraft(d store.Draft, msgs []store.Message) {
	if !m.rightDraftPin {
		return
	}
	m.rightDraftPin = false
	m.restoreRightDraft(d, msgs)
}

// showRight loads a container into the unpacked fields. The old list goes
// first: a frame drawn from the list of the one it replaced would show
// another container's messages under this one's title.
func (m *Model) showRight(f rightFrame) tea.Cmd {
	// First, while the fields still name the frame being left: after they are
	// overwritten there is nothing left to say which frame the box belonged to.
	keep := m.saveRightBox()
	m.rightKind, m.threadID, m.rightRoot = f.kind, f.id, f.root
	m.thread, m.threadBase, m.threadRows, m.threadMeta = nil, nil, nil, msgMeta{}
	m.threadIdx, m.threadTop, m.rightNote, m.rightName = 0, 0, "", f.name
	m.emptyRightBox()
	// The pin marks a frame that has not landed yet, which is what lets the
	// first list to arrive decide where to sit — on the message the frame
	// names through sel, and on the place its kind is read from otherwise.
	// Without it the frame would inherit the zeroed cursor above, which is the
	// root of a conversation the reader opened to see the end of.
	m.rightPin = f
	m.layout()
	return tea.Batch(keep, m.loadRight())
}

// loadRight fetches the visible frame's list.
func (m Model) loadRight() tea.Cmd {
	switch m.rightKind {
	case rightThread:
		return loadThread(m.deps, m.chatID, m.threadID)
	case rightForward:
		return loadForward(m.deps, m.rightRoot, m.threadID)
	case rightReply:
		return loadReplies(m.deps, m.chatID, m.threadID)
	}
	return nil
}

// rightLanded says where a list that has just arrived should sit. Ordinarily
// that is where the frame already is — the cursor's message and the line the
// viewport starts at, so a reload under the reader's hand moves nothing. A
// frame that has not landed yet has no such place, so its pin supplies one.
//
// The pin is spent on the first list with rows in it. An empty one is not an
// answer: a forward's level arrives empty while Feishu is still being asked
// for its children, and the reload that follows is the list the frame was
// opened to show.
func (m *Model) rightLanded(msgs []store.Message) rightLanding {
	if m.rightPin.kind != m.rightKind || m.rightPin.id != m.threadID || len(msgs) == 0 {
		return rightLanding{
			cursor: idAt(m.thread, m.threadIdx),
			anchor: topAnchor(m.threadRows, m.thread, m.threadTop),
			tail:   atTail(m.threadRows, m.threadTop, m.listHeight()),
		}
	}
	pin := m.rightPin
	m.rightPin = rightFrame{}
	if indexOfID(msgs, pin.sel) >= 0 {
		return rightLanding{cursor: pin.sel, anchor: pin.top, reveal: pin.top == lineAnchor{}}
	}
	// A bundle is a record of somebody else's conversation, read from its
	// start; a thread and a reply tree are conversations of this chat, opened
	// to see what has been said since.
	if m.rightKind == rightForward {
		return rightLanding{}
	}
	return rightLanding{atEnd: true, tail: true}
}

// placeRightCursor stands the cursor where the landing asked. It runs after
// the outbox has been spliced in, so the newest row counts a send still on its
// way and the cursor is re-found by id rather than kept as an index a reload
// may have renumbered.
func (m *Model) placeRightCursor(l rightLanding) {
	if l.atEnd {
		m.threadIdx = newestSelectable(m.thread)
		return
	}
	m.threadIdx = clamp(m.threadIdx, 0, max(0, len(m.thread)-1))
	if i := indexOfID(m.thread, l.cursor); i >= 0 {
		m.threadIdx = i
	}
}

// settleRight puts the viewport where the landing asked, once the rows are
// built.
func (m *Model) settleRight(l rightLanding) {
	if l.reveal {
		m.centerThreadOnSelection()
		return
	}
	m.threadTop = holdTop(m.threadRows, m.thread, l.anchor, l.tail, m.threadTop, m.listHeight())
}

// openerSel is the message a frame opened from x should stand on, and "" when
// x names no row inside it. A container opened from its own root is opened
// whole — the reader is asking for the conversation, not for the message they
// are already looking at — so it lands where its kind says instead.
func openerSel(kind rightKind, id string, x store.Message) string {
	switch kind {
	case rightThread:
		// Only the sign says a message is a reply; the sentinel is Feishu's.
		if x.MessagePosition < 0 {
			return x.MessageID
		}
	case rightReply:
		if x.MessageID != id {
			return x.MessageID
		}
	}
	return ""
}

// toggleRight is the t key: open the container under the cursor, or close the
// one it names when that is already what the column shows.
func (m Model) toggleRight() (tea.Model, tea.Cmd) {
	f, ok := m.containerAtCursor()
	if !ok {
		f, ok = m.detailsAtCursor()
	}
	switch {
	case ok && m.rightKind == f.kind && m.threadID == f.id:
		next, cmd := m.leaveRight()
		return next, cmd
	case ok:
		return m.openContainer(m.focus, f)
	case m.threadOpen():
		// The cursor is on nothing to open, so t is read as the other half of
		// its own toggle rather than as an error: without this the only way
		// out of the column is to focus it first.
		next, cmd := m.leaveRight()
		return next, cmd
	}
	return m.notify("selected message opens no thread, forward or replies", true), nil
}

// detailsAtCursor names the reply tree the selected message belongs to, which
// is the client's Details pane. Any member of the tree opens it, not the root
// alone: the message the conversation started from is often further up than
// the page reaches, and the reader asking about the one in front of them
// means the same conversation either way.
func (m Model) detailsAtCursor() (rightFrame, bool) {
	sel, ok := m.selected()
	if !ok {
		return rightFrame{}, false
	}
	g, ok := m.metaFor(m.focus).replies[sel.MessageID]
	if !ok {
		return rightFrame{}, false
	}
	f := rightFrame{kind: rightReply, id: g.Root, sel: openerSel(rightReply, g.Root, sel)}
	// The title is the root's own gist, which the cursor only has when it is
	// standing on the root. From anywhere else the frame opens untitled and
	// the list fills it in, the way a forward's card does.
	if g.Root == sel.MessageID {
		f.name = replyGist(sel)
	}
	return f, true
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
	f := rightFrame{kind: kind, id: id, root: bundle, sel: openerSel(kind, id, sel)}
	if kind == rightForward {
		f.name = forwardTitle(m.gistOf(id), m.deps.Self, m.selfName)
	}
	return f, kind != rightNone
}

// gistOf reads a bundle's collapsed card from whichever pane loaded it, so
// the keyboard opens a frame under the same title the mouse would.
func (m Model) gistOf(bundleID string) store.ForwardGist {
	if g, ok := m.meta.forwards[bundleID]; ok {
		return g
	}
	return m.threadMeta.forwards[bundleID]
}

// onForwardedChild reports that the cursor is on a message of another chat:
// a forwarded child, which this chat has no standing to answer, react to or
// recall. Feishu would take some of it and put the result where the reader
// is not looking — a reaction on somebody else's conversation.
func (m Model) onForwardedChild() bool {
	return m.focus == paneThread && m.rightKind == rightForward
}

// leaveRight closes the column and takes the focus back with it.
func (m Model) leaveRight() (Model, tea.Cmd) {
	cmd := m.closeRight()
	if m.focus == paneThread {
		m.focus = paneMessages
	}
	return m, cmd
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
