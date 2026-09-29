package tui

import (
	"cmp"

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
	root = cmp.Or(root, x.MessageID)
	title := forwardTitle(gist, st.self, st.selfName)
	if gist.Refused {
		title += " · cannot be expanded"
	}
	inner := st.inner() - lipgloss.Width(forwardBar)
	bar := stAccent.Render(forwardBar)
	row := func(s string, style lipgloss.Style) msgRow {
		lead := g.take()
		x0 := lead.cols()
		text, segs := "", gistSegs(bar, s, st.inner(), style, st.emojiGist)
		if segs == nil {
			text = bar + style.Render(truncate(s, inner))
		}
		return msgRow{lead: lead, text: text, segs: segs, idx: idx, zones: []clickZone{{
			x0: x0, x1: x0 + lipgloss.Width(text) + segsWidth(segs),
			open: x.MessageID, openRoot: root, openKind: rightForward, openName: title,
		}}}
	}
	rows := []msgRow{row(title, stBold)}
	for i, c := range gist.Preview {
		who := displaySender(store.Message{SenderID: c.SenderID, SenderName: c.SenderName},
			st.self, st.suffix[c.SenderID])
		line := who + ": " + replyGist(store.Message{MsgType: c.MsgType, ContentRaw: c.ContentRaw})
		// The ellipsis trails the last preview rather than standing on a line
		// of its own: a line holding nothing but it reads as a fifth child.
		if i == len(gist.Preview)-1 && gist.ChildCount > len(gist.Preview) {
			line += "…"
		}
		rows = append(rows, row(line, stDim))
	}
	return rows, true
}

// threadRoot reports whether a message opens a thread the chat's own flow has
// to summarise. Inside the thread's pane the replies are right below the root,
// so counting them again there says nothing.
func threadRoot(x store.Message, st msgStyle) bool {
	return !st.inFrame && x.ThreadID != "" && x.MessagePosition >= 0 && !x.Deleted
}

// threadRows are the lines a thread root takes in the chat: a head saying what
// is not on screen, then the thread's newest replies, all of them leading into
// the pane that lists the whole of it.
//
// The head is the client's own: it names what is hidden rather than what is
// shown, so a thread whose tail is all of it counts its replies instead. A
// root nobody has answered still draws one, because otherwise it looks like
// any other message and the reader has no way to tell that Enter opens a
// thread there rather than answering it.
//
// The tail runs oldest first, the way the chat itself does, so the newest
// reply is the line nearest the next message.
//
// A replier's face stands in their line rather than in the lead column, the
// way the client marks who is talking in a thread: the column belongs to the
// root, and a face out at the margin names somebody the eye has to travel back
// for. Standing against the name, it is the parting mark too, so the line needs
// no dot before who answered.
func threadRows(x store.Message, idx int, st msgStyle, g *leads) ([]msgRow, bool) {
	if !threadRoot(x, st) {
		return nil, false
	}
	gist := st.threads[x.ThreadID]
	head := g.take()
	if gist.Waiting {
		// The dot is set here rather than through leadFor, which only reaches
		// a message's first row: by the time this line is drawn the sender
		// line and any quote have already spent it. It is derived from the
		// read flags, not from the dots this visit gathered, so walking the
		// cursor past the root cannot wipe a reply nobody has seen.
		head.mark = stAccent.Render("●")
	}
	label := "⤷ No replies yet"
	if hidden := gist.Replies - len(gist.Tail); hidden > 0 {
		label = "⤷ View earlier " + plural(hidden, "reply", "replies")
	} else if gist.Replies > 0 {
		label = "⤷ " + plural(gist.Replies, "reply", "replies")
	}
	rows := []msgRow{threadRow(x, idx, head, stAccent.Render(label), nil)}
	for _, r := range gist.Tail {
		line := " " + displaySender(r, st.self, st.suffix[r.SenderID]) + ": " + replyGist(r)
		rows = append(rows, threadRow(x, idx, g.take(), "",
			faceSegs("", replierFace(r, st), line, st.inner(), stDim, st.emojiGist)))
	}
	return rows, true
}

// threadRow is one line of the block, whole-line clickable: every part of a
// thread's fold leads into the same pane, the head no differently from the
// reply under it.
func threadRow(x store.Message, idx int, lead lead, text string, segs []rowSeg) msgRow {
	x0 := lead.cols()
	return msgRow{lead: lead, text: text, segs: segs, idx: idx, zones: []clickZone{{
		x0: x0, x1: x0 + lipgloss.Width(text) + segsWidth(segs),
		open: x.ThreadID, openKind: rightThread,
	}}}
}

// replierFace is a replier's picture as one piece of their line: their avatar
// at the narrow size a summary line gives a picture, or the colour block
// standing in for it where the terminal draws none.
func replierFace(r store.Message, st msgStyle) rowSeg {
	name := senderLabel(r, "")
	if st.disc != nil {
		if pic := st.disc(st.avatars[r.SenderID], r.SenderID, name, gistCols, 1); pic.cols > 0 {
			return rowSeg{pic: pic}
		}
	}
	return rowSeg{text: avatarBlock(r.SenderID, name, gistCols)}
}

// replySummary is the line a message carries when answers hang under it: how
// many, and the way into the frame that lists them. It is the client's own
// "5 replies", and like the client it counts the whole tree — an answer to an
// answer is still part of the conversation the message started.
//
// The line carries no representative reply, unlike a thread's: a reply is an
// ordinary message of the chat and stays in the flow below its target, quote
// row and all, so the replies themselves are already on screen.
// replyGlyph is the speech bubble the client marks a reply count with, from
// the Nerd Font the terminal maps the private use area to: it takes the
// colour it is given. The plain bubble beside it
// in that font is spoken for — the header draws that one for a topic chat —
// and so is "↩", which marks the message an open draft answers.
const replyGlyph = "\uf27a" + enSpace

func replySummary(x store.Message, idx int, st msgStyle, g *leads) (msgRow, bool) {
	// Inside a frame the answers stand right below the root, so counting them
	// again says nothing. A thread reply is read in its thread's own frame.
	gist := st.replies[x.MessageID]
	if st.inFrame || gist.Root != x.MessageID || gist.Replies == 0 || x.ThreadID != "" {
		return msgRow{}, false
	}
	lead := g.take()
	x0 := lead.cols()
	text := stAccent.Render(replyGlyph + plural(gist.Replies, "reply", "replies"))
	return msgRow{lead: lead, text: text, idx: idx, zones: []clickZone{{
		x0: x0, x1: x0 + lipgloss.Width(text),
		open: gist.Root, openKind: rightReply, openName: replyGist(x),
	}}}, true
}
