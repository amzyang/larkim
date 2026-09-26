package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

// forwardBar is the card's left edge, the rule quoteRow already draws a
// quote with: a merged forward is another conversation quoted whole.
const forwardBar = "▏"

// forwardTitle names a bundle after the conversation it came from, the way
// the client titles the card — a group, the two people in a direct chat, or
// the reader alone in their own.
//
// The bare word is the fallback, and it is the client's own wording for a
// bundle drawn from several chats. It covers the unnamed source too: larkim
// has no row for a chat it was never in, and asking Feishu for one answers
// without a name or a mode, so there is nothing further to ask.
func forwardTitle(g store.ForwardGist, self, selfName string) string {
	const suffix = "'s Chat History"
	if g.Sources != 1 || g.SourceChatMode == "" || selfName == "" {
		return "Chat History"
	}
	if g.SourceChatMode != "p2p" {
		return "Group Chat History"
	}
	// The reader's chat with themselves has one name in it, not two.
	if g.SourcePeerID == self || g.SourceChatName == "" {
		return selfName + suffix
	}
	return selfName + " and " + flatten(g.SourceChatName) + suffix
}

// forwardSummary is the card a merged forward takes in a list: the
// conversation it came from, the first few messages of it, and an ellipsis
// when there are more. The whole card leads into the frame that lists them.
//
// It is the bundle's body, not an addition to it. The bundle's own body says
// only "Merged and Forwarded Message", and the tree lark-cli renders it into
// runs to thousands of characters of tags and ISO timestamps — a message
// whose text is in another chat is not something a list can print.
//
// The card carries no count. The client's does not either, and the ellipsis
// already says the frame holds more than the four lines standing here.
func forwardSummary(x store.Message, root string, idx int, st msgStyle, g *leads) ([]msgRow, bool) {
	if x.MsgType != "merge_forward" || x.Deleted {
		return nil, false
	}
	gist := st.forwards[x.MessageID]
	if root == "" {
		root = x.MessageID
	}
	title := forwardTitle(gist, st.self, st.selfName)
	if gist.Refused {
		title += " · cannot be expanded"
	}
	inner := st.inner() - lipgloss.Width(forwardBar)
	row := func(s string, style lipgloss.Style) msgRow {
		lead := g.take()
		x0 := lead.cols()
		text := stAccent.Render(forwardBar) + style.Render(truncate(s, inner))
		return msgRow{lead: lead, text: text, idx: idx, zones: []clickZone{{
			x0: x0, x1: x0 + lipgloss.Width(text),
			open: x.MessageID, openRoot: root, openKind: rightForward, openName: title,
		}}}
	}
	rows := []msgRow{row(title, stBold)}
	for _, c := range gist.Preview {
		who := displaySender(store.Message{SenderID: c.SenderID, SenderName: c.SenderName},
			st.self, st.suffix[c.SenderID])
		rows = append(rows, row(who+": "+replyGist(store.Message{MsgType: c.MsgType, ContentRaw: c.ContentRaw}), stDim))
	}
	if gist.ChildCount > len(gist.Preview) {
		rows = append(rows, row("…", stDim))
	}
	return rows, true
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
	head := "⤷ No replies yet"
	tail := ""
	if gist.Replies > 0 {
		head = "⤷ " + plural(gist.Replies, "reply", "replies")
		last := gist.Last()
		tail = " · " + displaySender(last, st.self, st.suffix[gist.SenderID]) + ": " + replyGist(last)
	}
	lead := g.take()
	if gist.Waiting {
		// The dot is set here rather than through leadFor, which only reaches
		// a message's first row: by the time this line is drawn the sender
		// line and any quote have already spent it. It is derived from the
		// read flags, not from the dots this visit gathered, so walking the
		// cursor past the root cannot wipe a reply nobody has seen.
		lead.mark = stAccent.Render("●")
	}
	x0 := lead.cols()
	text := stAccent.Render(head) + stDim.Render(truncate(tail, st.inner()-lipgloss.Width(head)))
	return msgRow{lead: lead, text: text, idx: idx, zones: []clickZone{{
		x0: x0, x1: x0 + lipgloss.Width(text),
		open: x.ThreadID, openKind: rightThread,
	}}}, true
}
