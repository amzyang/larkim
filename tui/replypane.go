package tui

import (
	tea "charm.land/bubbletea/v2"
)

// onReplyLoaded puts a reply tree in the right pane: the message a
// conversation started from and every live answer under it, flat and in the
// chat's own order, the way the client's Details pane lists them.
//
// Nothing here settles unread state. A reply is an ordinary message of the
// chat and was already in the flow the reader opened, so it was marked there;
// this pane gathers what is scattered, it does not uncover it.
func (m Model) onReplyLoaded(msg replyLoadedMsg) (tea.Model, tea.Cmd) {
	if m.rightKind != rightReply || msg.root != m.threadID {
		return m, nil
	}
	wasOn, anchor, tailed := m.rightLanded(msg.msgs)
	m.threadBase, m.threadMeta = msg.msgs, msg.meta
	m.applyOutbox()
	// The frame is titled by its root, which the reader only had in hand when
	// they opened it from the root itself.
	if i := indexOfID(m.thread, msg.root); i >= 0 {
		m.rightName = replyGist(m.thread[i])
	}
	m.threadIdx = clamp(m.threadIdx, 0, max(0, len(m.thread)-1))
	m.repinSelection(wasOn)
	m.rebuildThread()
	m.threadTop = holdTop(m.threadRows, m.thread, anchor, tailed, m.threadTop, m.listHeight())
	return m, nil
}
