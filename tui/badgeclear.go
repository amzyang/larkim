package tui

import (
	"slices"

	"github.com/amzyang/larkim/store"
)

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
func unreadWaiting(msgs []store.Message) bool {
	return slices.ContainsFunc(msgs, func(m store.Message) bool {
		return isUnread(m) && m.MessagePosition >= 0 && !m.Deleted
	})
}

func isUnread(m store.Message) bool {
	return m.IsReadRemote != nil && !*m.IsReadRemote && m.LocalReadAt == 0
}
