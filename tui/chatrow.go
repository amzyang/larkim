package tui

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
)

const (
	chatRowHeight = 2 // avatar height, and the two text lines beside it
	chatRowGap    = 1 // blank line holding one chat's two lines off the next
	chatRowStride = chatRowHeight + chatRowGap
	avatarWidth   = 4 // 4x2 cells is about square on a terminal grid
	avatarGap     = 1
	minTitleWidth = 2 // a title never shrinks past one glyph plus the ellipsis
)

// chatsThatFit is how many whole chats h lines of pane body hold. The last
// chat carries no separator, so n of them take n*chatRowStride - chatRowGap.
func chatsThatFit(h int) int { return max(0, (h+chatRowGap)/chatRowStride) }

// chatRow is one chat's two rendered lines. The avatar is kept apart from the
// text because a selection must not repaint it: it stands for a picture, and
// once it is one there is nothing to tint.
type chatRow struct {
	avatarTop, avatarBottom string
	top, bottom             string // text only, already fitted to textWidth
	// segs, when set, is the bottom line in pieces, because a reaction on it
	// is a picture the terminal fills in rather than a character. It stands
	// in for bottom, which is then empty.
	segs []rowSeg
}

// chatTextWidth is how much of a w-wide pane the text half of a row gets.
func chatTextWidth(w int) int { return max(minTitleWidth, w-avatarWidth-avatarGap) }

// botBadge marks a machine: the peer a p2p chat's title names, or a speaker
// named anywhere a turn is quoted. The robot head comes from the Nerd Font the
// terminal maps the private use area to, so it takes the colour of the name it
// rides and holds to a single column.
const botBadge = ""

// botMark badges a speaker whose turn came from an app. It rides the name
// rather than a chat's title because it states what one message is, not what
// the chat is: the next human turn takes it away again.
func botMark(senderType string) string {
	if senderType != "app" {
		return ""
	}
	return botBadge
}

// muteGlyph is the crossed-out bell, from the Nerd Font the terminal maps the
// private use area to. Unlike the emoji bell it takes the colour it is given,
// which is what lets it sit dim behind the summary.
const muteGlyph = ""

// mutedDot stands for the do-not-disturb chats that have something waiting.
// It carries no number: a chat the reader silenced is not one to be counted
// at. A filled circle is the smallest glyph that still reads alone at the
// pane's right edge — a bullet or a middle dot disappears there.
const mutedDot = "●"

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
	if daysApart(t, now) <= 0 {
		return t.Format("15:04")
	}
	return msgDay(ms, now)
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
	case "video_chat":
		return "[视频会议]"
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
	return name, c.PeerSuffix()
}

// isBotChat reports whether the title carries the badge. Only a p2p peer is a
// property of the chat itself; a group holds whoever its members invite, so a
// bot's turn there is marked on the summary line that quotes it.
func isBotChat(c store.Chat) bool { return c.P2PTargetType == "bot" }

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

	body := lastMessageSummary(c)
	switch {
	case body != "":
	case c.LastRenderedAt == 0:
		body = pendingSummary(c)
	default:
		body = msgTypeLabel(c.LastMsgType)
	}

	// p2p names the peer in the title already, so only the user's own turn
	// needs a prefix there. A system message is nobody's turn.
	prefix := ""
	switch {
	case sender == "":
	case c.LastSenderID == self:
		prefix = "你: "
	case c.ChatMode != "p2p":
		prefix = sender + botMark(c.LastSenderType) + ": "
	}
	// The line is dim as a whole, so an @ that reaches the reader is the one
	// thing on it that still carries a colour.
	return mentionsIn(c.LastMentionsJSON, self).on(stDim).render(prefix + body)
}

// lastMessageSummary is the chat's newest message pressed onto one line. A
// card is named by its title: the DSL below it is a whole screen of markup
// that says nothing at this width. Emoji are left for the styling pass, which
// splits the line at its mentions first.
func lastMessageSummary(c store.Chat) string {
	if card, ok := parseCard(c.LastContent); ok {
		return flatten(strings.TrimSpace(card.title + " " + card.tags))
	}
	return flatten(c.LastContent)
}

// chatChipLimit is how many reactions a chat row shows. Past three the icons
// crowd out the message they sit in front of.
const chatChipLimit = 3

// chatChipCols is how wide one reaction picture is drawn in the chat list.
// The text half of the pane is narrow, so a picture gets half of what it gets
// beside a message.
const chatChipCols = 2

// chatChips are the reactions on a p2p chat's newest message, as the icons
// alone: in a chat of two, who reacted and how many did is not in question. A
// group's stay in the message pane, where there is room to say whose they
// are. A recall takes them with the body, the way the client does.
//
// The icons share one chip rather than wearing one each: at this width a cap
// between every pair would cost more room than the icons themselves.
//
// An emoji this terminal can draw neither as a character nor as a picture is
// left out rather than spelled: its name at the head of the line would cost
// more room than the message behind it.
func chatChips(c store.Chat, pics emojiPics) []rowSeg {
	if c.ChatMode != "p2p" || c.LastDeleted {
		return nil
	}
	var out []rowSeg
	shown := 0
	for _, chip := range emoji.Summary(c.LastReactionsJSON, "") {
		if shown == chatChipLimit {
			break
		}
		e, known := emoji.ByKey(chip.Key)
		var seg rowSeg
		switch {
		case !known:
			continue
		case e.Glyph != "":
			seg = rowSeg{text: stChip.Render(e.Glyph)}
		default:
			pic := pics.chip(e.Key, chatChipCols)
			if pic.cols == 0 {
				continue
			}
			seg = rowSeg{pic: pic}
		}
		if shown > 0 {
			out = append(out, rowSeg{text: stChip.Render(" ")})
		}
		out = append(out, seg)
		shown++
	}
	if len(out) == 0 {
		return nil
	}
	out = append(out, rowSeg{text: stChipEdge.Render(chipRight)})
	return append([]rowSeg{{text: stChipEdge.Render(chipLeft)}}, out...)
}

// chatSummaryLine is the row's second line: the reactions the chat collected,
// then who said what, then the mute mark at the far edge. It comes back in
// pieces only when a reaction is a picture — a line of characters stays one
// string, which is what lets a selection tint it.
func chatSummaryLine(c store.Chat, self string, pics emojiPics, w int) (string, []rowSeg) {
	chips := chatChips(c, pics)
	room := w - segsWidth(chips)
	if len(chips) > 0 {
		room-- // the space that keeps the summary clear of the icons
	}
	body := padBetween(chatSummary(c, self), muteMark(c), max(0, room))
	if len(chips) == 0 {
		return body, nil
	}
	if !slices.ContainsFunc(chips, func(s rowSeg) bool { return s.pic.cols > 0 }) {
		var b strings.Builder
		for _, s := range chips {
			b.WriteString(s.text)
		}
		return b.String() + " " + body, nil
	}
	return "", append(chips, rowSeg{text: " " + body})
}

// renderChatRow lays one chat out over two lines of w columns, avatar
// included. Right-aligned fields are placed first and the title absorbs what
// is left, so the right edge stays aligned however long a name is.
func renderChatRow(av avatars, c store.Chat, unread int64, self string, now time.Time, w int, pics emojiPics) chatRow {
	avatarTop, avatarBottom, badged := av.cells(c, unread)
	textWidth := chatTextWidth(w)

	badge := ""
	if unread > 0 && !badged {
		badge = counterStyle(c).Render(strconv.FormatInt(unread, 10))
	}
	right := strings.TrimSpace(badge + " " + stDim.Render(chatTime(c.LastMessageMs, now)))

	bot := ""
	if isBotChat(c) {
		bot = botBadge
	}
	name, suffix := chatTitle(c)
	room := textWidth - lipgloss.Width(right) - lipgloss.Width(bot) - lipgloss.Width(suffix) - 1
	title := stBold.Render(personName(truncate(name, max(minTitleWidth, room)), suffix)) + bot

	bottom, segs := chatSummaryLine(c, self, pics, textWidth)
	return chatRow{
		avatarTop:    avatarTop,
		avatarBottom: avatarBottom,
		top:          padBetween(title, right, textWidth),
		bottom:       bottom,
		segs:         segs,
	}
}

// counterStyle shades the unread count the way the avatar's own badge is
// shaded. A picture that could not be built leaves its chat on the text
// fallback while its neighbours keep their discs, so the two have to agree on
// the colour as well as on what do-not-disturb looks like.
func counterStyle(c store.Chat) lipgloss.Style {
	if c.Muted {
		return stDim
	}
	return stUnread
}

// muteMark tells a do-not-disturb chat apart. The counter on the avatar goes
// grey for the same reason, but a muted chat with nothing unread has no
// counter to grey, so this is the only place the setting always shows.
func muteMark(c store.Chat) string {
	if !c.Muted {
		return ""
	}
	return stDim.Render(muteGlyph)
}

// padBetween pushes right to the far edge of w, cutting left if they collide.
// An empty right gives the whole width to left: there is nothing to keep it
// clear of.
func padBetween(left, right string, w int) string {
	rw := lipgloss.Width(right)
	if rw >= w {
		return fit(right, w)
	}
	gap := 1
	if rw == 0 {
		gap = 0
	}
	left = lipgloss.NewStyle().MaxWidth(w - rw - gap).Inline(true).Render(left)
	return left + strings.Repeat(" ", w-rw-lipgloss.Width(left)) + right
}

// chatsHeader is the list's title row: the pane's name carrying the number of
// messages waiting, and, at the far right, the dot that says the
// do-not-disturb chats have something too. The dot sits in the column every row's mute mark
// is right-aligned to, so the setting reads down a single column.
func chatsHeader(chats []store.Chat, unread map[string]int64, filter string, w int) string {
	n, muted := unreadMessages(chats, unread)
	count, dot := "", ""
	if label := badgeLabel(n); label != "" {
		count = stUnread.Render(superscript(label))
	}
	if muted {
		dot = stDim.Render(mutedDot)
	}
	title := "Chats"
	if filter != "" {
		title = "Chats /" + filter
	}
	// The gap follows padBetween's rule: without a dot there is nothing for
	// the title to keep clear of, so it gets that column too.
	gap := 0
	if dot != "" {
		gap = 1
	}
	room := w - lipgloss.Width(count) - lipgloss.Width(dot) - gap
	return padBetween(stBold.Render(truncate(title, room))+count, dot, w)
}

// unreadMessages sums the messages waiting for an answer and reports whether
// any chat on do-not-disturb is among them. The sum is what the avatar badges
// add up to, so the header and the rows below it state the same quantity.
// Muted chats stay out of it: the reader asked not to be counted at for them,
// and the dot is all the header says about them. The filter is not applied —
// hiding rows is a lens on the list, not a change to what is waiting.
func unreadMessages(chats []store.Chat, unread map[string]int64) (n int64, muted bool) {
	for _, c := range chats {
		switch {
		case unread[c.ChatID] <= 0:
		case c.Muted:
			muted = true
		default:
			n += unread[c.ChatID]
		}
	}
	return n, muted
}

// superDigits are the superscript forms of 0-9, in order. Unicode scatters
// them over three blocks and only 4-9 run consecutively, so the table spells
// them out rather than offsetting from a base rune.
var superDigits = []rune("⁰¹²³⁴⁵⁶⁷⁸⁹")

// superscript raises a counter's digits so the header reads as one word with
// a number hung off it rather than as two fields. Every rune it emits is one
// cell wide, which is what lets the row keep its column budget.
func superscript(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return superDigits[r-'0']
		case r == '+':
			return '⁺'
		default:
			return -1
		}
	}, s)
}
