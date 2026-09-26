package tui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// threadGlyph marks a row as a conversation inside a chat rather than a chat.
// It is the same one the collapsed thread line carries in the message pane, so
// the two places that stand for a thread read as one thing.
const threadGlyph = "⤷"

// renderThreadRow lays one thread out over two lines of w columns: the root
// titles it, the newest reply says where it stands, and the clock is that
// reply's. Right-aligned fields are placed first and the title absorbs what is
// left, the way a chat's row does.
//
// The avatar column is the client's own thread mark carrying the chat's
// picture, or — where no picture can be drawn — the glyph beside the chat's
// colour block. Either way it is the avatars renderer's to draw, keyed by the
// thread rather than by the chat, so the two rows of one chat can carry two
// different counters.
func renderThreadRow(av avatars, r listRow, self string, now time.Time, w int, pics emojiPics) chatRow {
	t := r.thread
	textWidth := chatTextWidth(w)
	avatarTop, avatarBottom, badged := av.cells(r, t.Unread)

	badge := ""
	if t.Unread > 0 && !badged {
		badge = counterStyle(r.chat).Render(strconv.FormatInt(t.Unread, 10))
	}
	right := strings.TrimSpace(badge + " " + stDim.Render(chatTime(t.Last.CreateMs, now)))

	title := displaySender(t.Root, self, "") + ": " + replyGist(t.Root)
	room := textWidth - lipgloss.Width(right) - 1
	top := padBetween(stBold.Render(truncate(title, max(minTitleWidth, room))), right, textWidth)

	text, segs := threadRowLine(r, self, pics, textWidth)
	return chatRow{
		avatarTop:    avatarTop,
		avatarBottom: avatarBottom,
		top:          top,
		bottom:       text,
		segs:         segs,
	}
}

// threadRowLine is the thread row's second line: the mention mark the
// unread replies earned, then the last of them, then the mute mark at the far
// edge. The reader's own slot stays empty — a draft belongs to the chat, and
// the chat's own row is where it shows.
func threadRowLine(r listRow, self string, pics emojiPics, w int) (string, []rowSeg) {
	at := ""
	if r.thread.NamesSelf {
		at = stMentionMe.Render("@") + " "
	}
	room := max(0, w-lipgloss.Width(at))
	text, summary := threadRowGist(r, self, pics)
	body := []rowSeg{{text: padBetween(text, muteMark(r.chat), room)}}
	if summary != nil {
		body = padSegs(summary, muteMark(r.chat), room)
	}
	var segs []rowSeg
	if at != "" {
		segs = append(segs, rowSeg{text: at})
	}
	return oneLineOr(append(segs, body...))
}

// threadRowGist is who answered last and what they said. A chat of two names
// nobody, the way its own row does: the two are the reader and the peer, and
// the title above already names whoever started the thread.
func threadRowGist(r listRow, self string, pics emojiPics) (string, []rowSeg) {
	x := r.thread.Last
	sender := senderLabel(x, "")
	if x.Deleted {
		return stDim.Render(sender + " recalled a message"), nil
	}
	prefix := ""
	switch {
	case x.SenderID == self:
		prefix = "You: "
	case r.chat.ChatMode != "p2p":
		prefix = sender + botMark(x.SenderType) + ": "
	}
	// The line is dim as a whole, so an @ that reaches the reader is the one
	// thing on it that still carries a colour.
	ms := mentionsIn(x.MentionsJSON, self).on(stDim)
	if segs := ms.segs(prefix+replyGist(x), pics.gist); segs != nil {
		return "", segs
	}
	return ms.render(prefix + replyGist(x)), nil
}
