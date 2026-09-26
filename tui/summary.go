package tui

import (
	"strconv"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

// forwardSummary is the one line a merged forward takes in a list: how many
// messages it holds and the first of them, with the whole line leading into
// the frame that lists them.
//
// It is the bundle's body, not an addition to it. The bundle's own body says
// only "Merged and Forwarded Message", and the tree lark-cli renders it into
// runs to thousands of characters of tags and ISO timestamps — a message
// whose text is in another chat is not something a list can print.
//
// The count outlives the gist under a narrow pane, the way a reaction chip
// keeps its number: how much is in there is the thing a reader is deciding
// on, and the first line of it is only a hint at what.
func forwardSummary(x store.Message, root string, idx int, st msgStyle, g *leads) (msgRow, bool) {
	if x.MsgType != "merge_forward" || x.Deleted {
		return msgRow{}, false
	}
	gist := st.forwards[x.MessageID]
	head := "[合并转发]"
	if gist.ChildCount > 0 {
		head += " " + strconv.Itoa(gist.ChildCount) + " 条"
	}
	tail := ""
	switch {
	case gist.Refused:
		tail = " · 无法展开"
	case gist.MsgType != "":
		who := displaySender(store.Message{SenderID: gist.SenderID, SenderName: gist.SenderName},
			st.self, st.suffix[gist.SenderID])
		tail = " · " + who + ": " + replyGist(store.Message{MsgType: gist.MsgType, ContentRaw: gist.ContentRaw})
	}
	lead := g.take()
	x0 := lead.cols()
	text := stAccent.Render(head) + stDim.Render(truncate(tail, st.inner()-lipgloss.Width(head)))
	if root == "" {
		root = x.MessageID
	}
	return msgRow{lead: lead, text: text, idx: idx, zones: []clickZone{{
		x0: x0, x1: x0 + lipgloss.Width(text),
		open: x.MessageID, openRoot: root, openKind: rightForward,
	}}}, true
}

// threadSummary is the one line a thread root takes in the chat: how many
// replies are under it and the last of them, with the whole line leading into
// the pane that lists them.
//
// It is drawn whether or not anybody has answered. A root with no reply looks
// like any other message otherwise, and the reader would have no way to tell
// that Enter opens a thread there rather than answering the message.
//
// The representative reply is the newest, which is the opposite of a forward:
// a thread is alive, and the last word is where it stands.
func threadSummary(x store.Message, idx int, st msgStyle, g *leads) (msgRow, bool) {
	// Only in the chat's own flow. Inside the thread's pane the replies are
	// right below the root, so counting them again there says nothing.
	if st.inFrame || x.ThreadID == "" || x.MessagePosition < 0 || x.Deleted {
		return msgRow{}, false
	}
	gist := st.threads[x.ThreadID]
	head := "⤷ 还没有回复"
	tail := ""
	if gist.Replies > 0 {
		head = "⤷ " + strconv.Itoa(gist.Replies) + " 条回复"
		last := gist.Last()
		tail = " · " + displaySender(last, st.self, st.suffix[gist.SenderID]) + ": " + replyGist(last)
	}
	lead := g.take()
	x0 := lead.cols()
	text := stAccent.Render(head) + stDim.Render(truncate(tail, st.inner()-lipgloss.Width(head)))
	return msgRow{lead: lead, text: text, idx: idx, zones: []clickZone{{
		x0: x0, x1: x0 + lipgloss.Width(text),
		open: x.ThreadID, openKind: rightThread,
	}}}, true
}
