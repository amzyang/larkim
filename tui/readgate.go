package tui

import "slices"

// readKey names the newest thing waiting on the page the reader has in front
// of them, or "" when nothing is waiting or the page is not being shown at
// all. Reading is the moment this changes, not the state itself: firing on the
// state would put one clear on every screen beat for as long as the reader
// sat still.
//
// Keyed on the newest unread rather than the newest message, because unread
// arrives two ways and only this covers both. A new message brings its own id,
// and its flag with it when it lands as an arrival. Any other flag lands on a
// message that is already on the page: those read_state rows are written by a
// pass of their own (sync.checkReadStatus, riding along every other chat
// beat), so the page that carried the message in had nothing to settle, and
// the page that lights the badge carries no new id. The same holds for a flag
// landing on a message older than the newest.
//
// The page folds thread replies into their root's line, so every unread it
// carries is a main-flow message the watermark settles: the set AcceptRemoteRead
// flips, which is the chat badge's own. The clear is gated separately, by
// unreadWaiting.
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
// search pane draws its hits through the same rows and msgTop, the Unread
// panel draws every waiting chat through them, the help overlay covers the
// panes whole, a narrow terminal folds the pane away for the thread beside
// it, and below the minimum View draws neither pane.
//
// The Unread panel is out although m.chatID names a real chat throughout it:
// what is in front of the reader is a page of many chats, and being shown one
// is not reading it. The panel reads a chat by its own rule, when the reader
// lands on or acts on one of its messages (readFeedChat).
//
// tailed belongs to the caller. markDots asks about the viewport a message
// arrived into, readKey about the one the update left behind.
func (m Model) pageShown(tailed bool) bool {
	return m.chatID != "" && m.focused && !m.searching && m.feed == nil && !m.help.open && !m.config.open && !m.foldRight() &&
		m.width >= minWidth && m.height >= minHeight && tailed
}
