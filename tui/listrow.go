package tui

import (
	"strings"

	"github.com/amzyang/larkim/store"
)

// listRow is one line-pair of the chats pane: a chat, or a thread standing
// beside the chat it happens in, the way the client's own list stands both
// there. A thread row carries that chat as well — it is drawn in the chat's
// colours and opening it opens the chat first.
//
// A reply does not move the chat it lands in, which is the whole point of a
// thread; a place of its own is how the thread says it has something new
// without taking the chat's.
type listRow struct {
	chat   store.Chat
	thread store.ThreadFeed
}

// isThread reports which of the two a row is.
func (r listRow) isThread() bool { return r.thread.ThreadID != "" }

// chatID is the chat the row leads into, which a thread row has too.
func (r listRow) chatID() string { return r.chat.ChatID }

// key identifies the row across a reload, which is what carries the cursor
// and the viewport over one. A thread and its chat are two rows, so the
// thread's own id is what tells them apart.
func (r listRow) key() string {
	if r.isThread() {
		return r.thread.ThreadID
	}
	return r.chat.ChatID
}

// unread is the number the row's badge carries. A chat's is kept apart from
// the chats themselves because it is reloaded on its own; a thread's arrives
// with the thread.
func (r listRow) unread(unread map[string]int64) int64 {
	if r.isThread() {
		return r.thread.Unread
	}
	return unread[r.chat.ChatID]
}

// listRows interleaves the chats with the reader's threads, each list already
// newest first. Equal timestamps put the chat first: a thread's root is a
// message of the chat, so the chat is where it came from.
//
// It is derived on every pass rather than kept beside the two lists it merges:
// a third stored order is a third truth to hold in step, and this one is a
// walk down two slices already in the order it wants.
func listRows(chats []store.Chat, threads []store.ThreadFeed) []listRow {
	rows := make([]listRow, 0, len(chats)+len(threads))
	owner := owningChats(chats, threads)
	i := 0
	for _, t := range threads {
		for ; i < len(chats) && chats[i].LastUnsilencedMs >= t.Last.CreateMs; i++ {
			rows = append(rows, listRow{chat: chats[i]})
		}
		rows = append(rows, listRow{chat: owner[t.ChatID], thread: t})
	}
	for ; i < len(chats); i++ {
		rows = append(rows, listRow{chat: chats[i]})
	}
	return rows
}

// owningChats is the chat each thread happens in, taken from the listing
// rather than rebuilt from the thread: the listing's row is where a chat's
// picture is — the group's, or the peer's through the contacts join — and a
// second answer assembled here would be a second truth about how one chat
// looks. A thread past the end of the listing falls back to the little the
// thread itself carries, which is enough to draw the row and to open it.
func owningChats(chats []store.Chat, threads []store.ThreadFeed) map[string]store.Chat {
	if len(threads) == 0 {
		return nil
	}
	want := make(map[string]store.Chat, len(threads))
	for _, t := range threads {
		want[t.ChatID] = t.Chat()
	}
	for _, c := range chats {
		if _, ok := want[c.ChatID]; ok {
			want[c.ChatID] = c
		}
	}
	return want
}

// rowKeyAt names the row at idx, or "" when the list does not reach it.
func rowKeyAt(rows []listRow, idx int) string {
	if idx < 0 || idx >= len(rows) {
		return ""
	}
	return rows[idx].key()
}

// indexOfRow finds a row by the key rowKeyAt hands out.
func indexOfRow(rows []listRow, key string) int {
	for i, r := range rows {
		if r.key() == key {
			return i
		}
	}
	return -1
}

// indexOfChatRow finds the chat's own row, walking past the threads that
// happen in it: opening a chat pins the chat, not a conversation inside it.
func indexOfChatRow(rows []listRow, chatID string) int {
	for i, r := range rows {
		if !r.isThread() && r.chat.ChatID == chatID {
			return i
		}
	}
	return -1
}

// containsFold is the plain substring a thread's own words answer with. The
// chat index is for names — pinyin, initials, the rune positions it hands
// back to underline — and a root's body has none of that to offer.
func containsFold(s, query string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(strings.TrimSpace(query)))
}
