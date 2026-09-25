package tui

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
)

const (
	avatarWidth   = 4 // 4x2 cells is about square on a terminal grid
	avatarHeight  = 2
	chatRowHeight = avatarHeight // the avatar, and the two text lines beside it
	chatRowGap    = 1            // blank line holding one chat's two lines off the next
	chatRowStride = chatRowHeight + chatRowGap
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

// draftGlyph fills the row's own slot: what the reader left unsent in this
// chat. It comes from the Nerd Font, so it takes the colour it is given and
// holds to a single column. A send Feishu refused is not drawn here — it keeps
// its place in the message list as a (failed) bubble the outbox can resend.
const draftGlyph = ""

// selfMark is the row's own slot: the draft waiting in this chat. It carries
// its trailing space, so a chat with nothing unsent gives the whole line to
// the summary rather than an indent that means nothing.
func selfMark(d store.Draft) string {
	if d.Empty() {
		return ""
	}
	return stDim.Render(draftGlyph) + " "
}

// atMeMark is the other half of the row's marker pair: somebody in this chat
// is waiting on the reader by name. It wears the same filled blue badge the
// message list paints the reader's own mention with, so the chat list and the
// message behind it read as one signal.
//
// It outranks the reaction chips and takes their slot, which is what the
// client does: being named is the louder of the two, and at 38 columns only
// one of them fits in front of the summary.
func atMeMark(c store.Chat) string {
	if !c.UnreadMention {
		return ""
	}
	return stMentionMe.Render("@")
}

// mutedDot stands for the do-not-disturb chats that have something waiting.
// It carries no number: a chat the reader silenced is not one to be counted
// at. A filled circle is the smallest glyph that still reads alone at the
// pane's right edge — a bullet or a middle dot disappears there.
const mutedDot = "●"

// idHash is a chat's or a person's stable colour seed, so the same one always
// looks the same.
func idHash(id string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(id))
	return h.Sum32()
}

// avatarPalette are the ANSI colours a chat's block can take. White on any of
// these reads on every terminal theme, which a computed shade would not.
var avatarPalette = []string{"1", "2", "3", "4", "5", "6"}

// avatarStyle is the colour a block takes, shaded from an id so the same chat
// or the same person always looks the same.
func avatarStyle(id string) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color(avatarPalette[int(idHash(id))%len(avatarPalette)]))
}

// avatarBlock is the text stand-in for a picture: a w-wide colour block
// carrying the first character of a name. It is one line, because the two
// lists that draw it give the column different heights.
func avatarBlock(id, name string, w int) string {
	initial := "?"
	for _, r := range flatten(name) {
		initial = string(r)
		break
	}
	pad := w - lipgloss.Width(initial)
	if pad < 0 {
		initial, pad = "?", w-1
	}
	left := pad / 2
	return avatarStyle(id).Render(strings.Repeat(" ", left) + initial + strings.Repeat(" ", pad-left))
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
	case "post":
		return "[富文本]"
	case "calendar":
		return "[日程]"
	case "share_calendar_event":
		return "[日程分享]"
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
// card is named by its title: the document below it is a whole screen of
// content that says nothing at this width. Emoji are left for the styling
// pass, which splits the line at its mentions first.
func lastMessageSummary(c store.Chat) string {
	if k, ok := card.Parse(c.LastContentRaw); ok {
		return flatten(cardGist(k))
	}
	// An attachment renders into markup naming its keys, which says nothing
	// on a summary line; the body behind it names the file itself.
	if a, ok := attachmentOf(c.LastMsgType, c.LastContentRaw); ok {
		return attachGist(a)
	}
	// A picture renders into a reference naming its key, which is fifty
	// characters of markup that would crowd the words beside it off the line.
	// The pane draws the picture itself; here it is only worth naming when
	// there are no words to show instead.
	keys, rest := splitImages(c.LastContent)
	if text := flatten(rest); text != "" {
		return text
	}
	if len(keys) > 0 {
		return "[图片]"
	}
	return ""
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

// chatSummaryLine is the row's second line: what the reader left unsent, then
// the reactions the chat collected, then who said what, then the mute mark at
// the far edge. It comes back in pieces only when a reaction is a picture — a
// line of characters stays one string, which is what lets a selection tint it.
func chatSummaryLine(c store.Chat, d store.Draft, self string, pics emojiPics, w int) (string, []rowSeg) {
	mine := selfMark(d)
	chips := chatChips(c, pics)
	if at := atMeMark(c); at != "" {
		chips = []rowSeg{{text: at}}
	}
	room := w - segsWidth(chips) - lipgloss.Width(mine)
	if len(chips) > 0 {
		room-- // the space that keeps the summary clear of the icons
	}
	body := padBetween(chatSummary(c, self), muteMark(c), max(0, room))
	if len(chips) == 0 {
		return mine + body, nil
	}
	if !slices.ContainsFunc(chips, func(s rowSeg) bool { return s.pic.cols > 0 }) {
		var b strings.Builder
		for _, s := range chips {
			b.WriteString(s.text)
		}
		return mine + b.String() + " " + body, nil
	}
	segs := append(chips, rowSeg{text: " " + body})
	if mine == "" {
		return "", segs
	}
	return "", append([]rowSeg{{text: mine}}, segs...)
}

// markName styles a name with the runes a filter landed on underlined. The
// underline is folded into the base style rather than nested inside it: an
// inner reset would end the bold for the rest of the line. Runs are rendered
// whole so a name costs a handful of escapes rather than one per rune.
func markName(s string, pos []int, base lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(s)
	}
	at := make(map[int]bool, len(pos))
	for _, p := range pos {
		at[p] = true
	}
	under := base.Underline(true)
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); {
		j, on := i, at[i]
		for j < len(r) && at[j] == on {
			j++
		}
		style := base
		if on {
			style = under
		}
		b.WriteString(style.Render(string(r[i:j])))
		i = j
	}
	return b.String()
}

// renderChatRow lays one chat out over two lines of w columns, avatar
// included. Right-aligned fields are placed first and the title absorbs what
// is left, so the right edge stays aligned however long a name is. mark is
// the runes of the name the filter landed on, empty when there is no filter
// or the hit came through pinyin.
func renderChatRow(av avatars, c store.Chat, d store.Draft, unread int64, self string, now time.Time, w int, pics emojiPics, mark []int) chatRow {
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
	title := markName(personName(truncate(name, max(minTitleWidth, room)), suffix), mark, stBold) + bot

	bottom, segs := chatSummaryLine(c, d, self, pics, textWidth)
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
	left = cut(lipgloss.NewStyle().Inline(true).Render(left), w-rw-gap)
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
