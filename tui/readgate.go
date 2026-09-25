package tui

import "slices"

// readKey names the newest thing waiting on the page the reader has in front
// of them, or "" when nothing is waiting or the page is not being shown at
// all. Reading is the moment this changes, not the state itself: firing on the
// state would put one markChatRead — and one applink behind it — on every
// screen beat for as long as the reader sat still.
//
// Keyed on the newest unread rather than the newest message, because unread
// arrives two ways and only this covers both. A new message brings its own id.
// A read flag lands on a message that is already on the page: read_state rows
// are written by a pass of their own (sync.checkReadStatus, riding along every
// other chat beat), so the page that carried the message in had nothing to
// settle, and the page that lights the badge carries no new id. The same holds
// for a flag landing on a message older than the newest.
//
// The predicate mirrors store.unreadInPane — thread replies included — the set
// markChatRead settles. Narrowing it to the chat badge's own set would leave a
// chat whose only unread is a reply permanently unsettled, redrawing its
// marker on every visit. The applink is gated separately, by unreadWaiting.
func (m Model) readKey(tailed bool) string {
	if !m.pageShown(tailed) {
		return ""
	}
	for _, x := range slices.Backward(m.msgsBase) {
		if isUnread(x) && !x.Deleted {
			return m.chatID + "|" + x.MessageID
		}
	}
	return ""
}

// pageShown reports whether the chat's own messages are what the reader is
// looking at. Every term names a way the message pane is not on screen: the
// search pane draws its hits through the same rows and msgTop, the help
// overlay covers the panes whole, a narrow terminal folds the pane away for
// the thread beside it, and below the minimum View draws neither pane.
//
// tailed belongs to the caller. markDots asks about the viewport a message
// arrived into, readKey about the one the update left behind.
func (m Model) pageShown(tailed bool) bool {
	return m.chatID != "" && m.focused && !m.searching && !m.help.open && !m.foldRight() &&
		m.width >= minWidth && m.height >= minHeight && tailed
}
