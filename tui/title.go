package tui

import "github.com/amzyang/larkim/store"

// windowTitle is what the terminal tab carries, so a glance at the tab bar
// says whether anything is waiting. The count leads because a tab bar
// truncates from the end, and a badge hung off the name is the first thing a
// crowded one drops. Muted chats stay out: the pane header says they have
// something, but a tab-bar marker is the pull that muting asked not to happen.
func windowTitle(chats []store.Chat, unread map[string]int64) string {
	n, _ := unreadMessages(chats, unread)
	if label := badgeLabel(n); label != "" {
		return "(" + label + ") larkim"
	}
	return "larkim"
}
