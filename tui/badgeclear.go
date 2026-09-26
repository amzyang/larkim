package tui

import "github.com/amzyang/larkim/store"

// unreadWaiting reports whether a page carries a message the chat badge
// counts. It has to be read against the page as it was queried, before
// takeRead's own write lands, or the reader's visit would erase the very
// evidence that the Feishu client has something to clear.
//
// The predicate is store.unreadBadge, down to the thread replies a chat page
// carries but the badge leaves out. Matching it exactly is what bounds the
// applinks: every message this fires for is one markChatRead takes in the
// same breath, so the page that comes back says no. A looser reading would
// keep saying yes to something no write ever settles, and walk the client
// onto the chat on every reload for the rest of the session.
// The position it answers with is the newest such message's, which is what
// lands the client at the tail. Opened without one it stops on its own unread
// divider, and for a chat with a deep backlog that divider is somewhere in
// the middle of the history — the same landing pageShown refuses to call read.
func unreadWaiting(msgs []store.Message) (position int64, waiting bool) {
	for _, m := range msgs {
		if isUnread(m) && m.MessagePosition >= 0 && !m.Deleted {
			position, waiting = max(position, m.MessagePosition), true
		}
	}
	return position, waiting
}

func isUnread(m store.Message) bool {
	return m.IsReadRemote != nil && !*m.IsReadRemote && m.LocalReadAt == 0
}
