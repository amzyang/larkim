package tui

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

const (
	chatRowHeight = 2 // avatar height, and the two text lines beside it
	avatarWidth   = 4 // 4x2 cells is about square on a terminal grid
	avatarGap     = 1
	minTitleWidth = 2 // a title never shrinks past one glyph plus the ellipsis
)

// chatRow is one chat's two rendered lines. The avatar is kept apart from the
// text because a selection must not repaint it: it stands for a picture, and
// once it is one there is nothing to tint.
type chatRow struct {
	avatarTop, avatarBottom string
	top, bottom             string // text only, already fitted to textWidth
}

// chatTextWidth is how much of a w-wide pane the text half of a row gets.
func chatTextWidth(w int) int { return max(minTitleWidth, w-avatarWidth-avatarGap) }

// chatHash is a chat's stable colour seed, so it always looks the same.
func chatHash(chatID string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(chatID))
	return h.Sum32()
}

// avatarPalette are the ANSI colours a chat's block can take. White on any of
// these reads on every terminal theme, which a computed shade would not.
var avatarPalette = []string{"1", "2", "3", "4", "5", "6"}

// avatarBlock is the text stand-in for a chat's picture: a two-line colour
// block carrying the chat's initial, shaded from the chat id so a chat always
// looks the same.
func avatarBlock(c store.Chat) (string, string) {
	st := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color(avatarPalette[int(chatHash(c.ChatID))%len(avatarPalette)]))

	initial := "?"
	for _, r := range flatten(c.Name) {
		initial = string(r)
		break
	}
	pad := avatarWidth - lipgloss.Width(initial)
	if pad < 0 {
		initial, pad = "?", avatarWidth-1
	}
	left := pad / 2
	return st.Render(strings.Repeat(" ", left) + initial + strings.Repeat(" ", pad-left)),
		st.Render(strings.Repeat(" ", avatarWidth))
}

// chatTime buckets a timestamp the way the Feishu client does: the closer it
// is, the more precise the label.
func chatTime(ms int64, now time.Time) string {
	if ms == 0 {
		return ""
	}
	t := time.UnixMilli(ms).Local()
	day := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location())
	}
	days := int(day(now).Sub(day(t)).Hours() / 24)
	switch {
	case days <= 0:
		return t.Format("15:04")
	case days == 1:
		return "昨天"
	case days < 7:
		return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[t.Weekday()]
	case t.Year() == now.Year():
		return t.Format("01-02")
	default:
		return t.Format("2006-01-02")
	}
}

// msgTypeLabel stands in for a message whose rendering carries no text.
func msgTypeLabel(msgType string) string {
	switch msgType {
	case "image":
		return "[图片]"
	case "file":
		return "[文件]"
	case "audio":
		return "[语音]"
	case "media":
		return "[视频]"
	case "sticker":
		return "[表情]"
	case "interactive":
		return "[卡片]"
	case "share_chat":
		return "[群名片]"
	case "share_user":
		return "[个人名片]"
	case "merge_forward":
		return "[合并转发]"
	case "system":
		return "[系统消息]"
	case "":
		return ""
	default:
		return "[" + msgType + "]"
	}
}

// chatTitle is the chat's name, plus the account suffix that tells same-named
// colleagues apart.
func chatTitle(c store.Chat) (name, suffix string) {
	name = flatten(c.Name)
	if name == "" {
		name = c.ChatID
	}
	if s := c.PeerSuffix(); s != "" {
		suffix = " (" + s + ")"
	}
	return name, suffix
}

// isBotChat reports whether the BOT badge applies: for a p2p chat that is a
// property of the peer, for a group it follows whoever spoke last.
func isBotChat(c store.Chat) bool {
	if c.ChatMode == "p2p" {
		return c.P2PTargetType == "bot"
	}
	return c.LastSenderType == "app"
}

// chatSummary is the second line's text: who said what, or why there is
// nothing to show.
func chatSummary(c store.Chat, self string) string {
	if c.LastMessageID == "" {
		if c.SyncError != "" {
			return stDim.Render("history unavailable")
		}
		return stDim.Render("New chat")
	}
	sender := flatten(c.LastSenderName)
	if sender == "" {
		sender = c.LastSenderID
	}
	if c.LastDeleted {
		return stDim.Render(sender + "撤回了一条消息")
	}

	body := flatten(c.LastContent)
	switch {
	case body != "":
	case c.LastRenderedAt == 0:
		return stDim.Render("…")
	default:
		body = msgTypeLabel(c.LastMsgType)
	}

	// p2p names the peer in the title already, so only the user's own turn
	// needs a prefix there.
	prefix := ""
	switch {
	case c.LastSenderID == self:
		prefix = "你: "
	case c.ChatMode != "p2p":
		prefix = sender + ": "
	}
	return stDim.Render(prefix + body)
}

// renderChatRow lays one chat out over two lines of w columns, avatar
// included. Right-aligned fields are placed first and the title absorbs what
// is left, so the right edge stays aligned however long a name is.
func renderChatRow(av avatars, c store.Chat, unread int64, self string, now time.Time, w int) chatRow {
	avatarTop, avatarBottom := av.cells(c)
	textWidth := chatTextWidth(w)

	badge := ""
	if unread > 0 {
		badge = stAccent.Render(fmt.Sprintf("%d", unread))
	}
	right := strings.TrimSpace(badge + " " + stDim.Render(chatTime(c.LastMessageMs, now)))

	bot := ""
	if isBotChat(c) {
		bot = " " + stAccent.Render("BOT")
	}
	name, suffix := chatTitle(c)
	room := textWidth - lipgloss.Width(right) - lipgloss.Width(bot) - lipgloss.Width(suffix) - 1
	title := stBold.Render(truncate(name, max(minTitleWidth, room))) + suffix + bot

	return chatRow{
		avatarTop:    avatarTop,
		avatarBottom: avatarBottom,
		top:          padBetween(title, right, textWidth),
		bottom:       fit(chatSummary(c, self), textWidth),
	}
}

// padBetween pushes right to the far edge of w, cutting left if they collide.
func padBetween(left, right string, w int) string {
	rw := lipgloss.Width(right)
	if rw >= w {
		return fit(right, w)
	}
	left = lipgloss.NewStyle().MaxWidth(w - rw - 1).Inline(true).Render(left)
	return left + strings.Repeat(" ", w-rw-lipgloss.Width(left)) + right
}
