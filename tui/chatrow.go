package tui

import (
	"cmp"
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

// enSpace is the second cell every Nerd Font glyph below carries. Those glyphs
// advance a full em against the text font's 0.6, and the terminal draws one at
// its own size only when the cell after it is blank — otherwise it shrinks the
// drawing to fit a single cell, so the same badge comes out at two sizes. A
// plain space does not settle it: the renderer collapses a run of spaces into
// an erase, and an erased cell is not the space character the terminal looks
// for. An en-space is never collapsed and counts as blank, so a glyph paired
// with one is always drawn full size and always measures two columns.
const enSpace = "\u2002"

// botBadge marks a machine: the peer a p2p chat's title names, or a speaker
// named anywhere a turn is quoted. The robot head comes from the Nerd Font the
// terminal maps the private use area to, so it takes the colour of the name it
// rides.
const botBadge = "\ueb08" + enSpace

// botMark badges a speaker whose turn came from an app. It rides the name
// rather than a chat's title because it states what one message is, not what
// the chat is: the next human turn takes it away again.
func botMark(senderType string) string {
	if senderType != "app" {
		return ""
	}
	return botBadge
}

// todoBoxDone and todoBoxOpen are a task's checkbox (FA check-circle and
// circle-o): the filled circle carries the check a completed task draws, the
// hollow one the task still open. Like the other Nerd glyphs they pair with
// an en-space to hold their two columns, and take the colour the card gives
// them. They are the TUI's spelling alone — what a rendering stores stays the
// plain box, so nothing private-use lands in the database.
const (
	todoBoxDone = "\uf058" + enSpace
	todoBoxOpen = "\uf10c" + enSpace
)

// bellUnmuteGlyph is the outline bell (FA bell-o): the Unmute action in the
// chat context menu. Like muteGlyph it comes from the Nerd Font PUA block so
// it takes the colour it is given.
const bellUnmuteGlyph = "\uf0a2" + enSpace

// muteGlyph is the outline bell with slash (FA bell-slash): muted chats in the
// list and the Mute action. Unlike the emoji bell it takes the colour it is
// given, which is what lets it sit dim behind the summary.
const muteGlyph = "\uf1f6" + enSpace

// draftPencil is the icon alone, so the marker can carry its underline on the
// pencil and leave the cell after it plain: the style splits per cluster, and
// a continuation cell re-asserts the underline straight — a line under the
// gap, one cell off the pencil it belongs to.
const draftPencil = "\uea73"

// draftGlyph fills the row's own slot: what the reader left unsent in this
// chat. The pencil comes from the Codicons block botBadge is from, so it takes
// the colour it is given. A send Feishu refused is not drawn here — it keeps
// its place in the message list as a (failed) bubble the outbox can resend.
const draftGlyph = draftPencil + enSpace

// candGlyph is the row's own slot for replies someone else drafted: the
// lightbulb says a suggestion is waiting, where the pencil above says the
// reader's own words are. It comes from the block muteGlyph is from, so it
// takes the colour it is given.
const candGlyph = "\uf0eb" + enSpace

// candMark is what a chat with pending lark-watch drafts draws. The count is
// the picker's first line, not the row's: one glyph is enough to say drafts
// are waiting, and a number beside the unread one would be read as theirs.
func candMark(n int) string {
	if n <= 0 {
		return ""
	}
	return stDim.Render(candGlyph)
}

// selfMark is the row's own slot: the draft waiting in this chat. The glyph
// carries the cell that parts it from the summary, so a chat with nothing
// unsent gives the whole line to the summary rather than an indent that means
// nothing.
func selfMark(d store.Draft) string {
	if d.Empty() {
		return ""
	}
	return stDraft.Render(draftPencil) + enSpace
}

// atMeMark is the other half of the row's marker pair: somebody in this chat
// is waiting on the reader by name. It wears the same filled blue badge the
// message list paints the reader's own mention with, so the chat list and the
// message behind it read as one signal.
//
// It outranks the reaction chips and takes their slot, which is what the
// client does: being named is the louder of the two, and at 38 columns only
// one of them fits in front of the summary.
func atMeMark(named bool) string {
	if !named {
		return ""
	}
	return stMentionMe.Render("@")
}

// markAllGlyph is the button that takes every chat as read: the double check
// an IM draws for "seen". It comes from the Codicons block of the Nerd Font
// the terminal maps the private use area to, the same block botBadge is from,
// so it takes the colour it is given. The Material Design double check is the
// better drawing and is unusable here: its plane-15 codepoint falls outside
// every range the terminal maps, so it would be left to font fallback and
// could come back double width.
const markAllGlyph = "\uebb1" + enSpace

// markAllCol is the content column the button occupies in a w-wide header.
// The strip to its right is a fixed three columns whether or not the muted
// dot is drawn, because a click target that moves when some other chat is
// silenced is a click target the reader has to look for.
func markAllCol(w int) int { return w - 3 }

// inMarkAll answers whether a content column presses the button. The whole
// strip answers, not the glyph's own cell: a one-column target in a header is
// missed more often than it is hit, and the two columns beside the glyph
// carry nothing of their own to press.
func inMarkAll(col, w int) bool { return col >= markAllCol(w) && col < w }

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

// avatarSilenceBlock is the text stand-in for a silenced message's avatar: a
// dotted circle centred in the column, where Kitty graphics would draw a dashed rim.
func avatarSilenceBlock(w int) string {
	glyph := "◌"
	pad := w - lipgloss.Width(glyph)
	left := pad / 2
	return stDim.Render(strings.Repeat(" ", left) + glyph + strings.Repeat(" ", pad-left))
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
		return "[Image]"
	case "file":
		return "[File]"
	case "folder":
		return "[Folder]"
	case "audio":
		return "[Audio]"
	case "media", "video":
		return "[Video]"
	case "location":
		return "[Location]"
	case "todo":
		return "[Todo]"
	case "vote":
		return "[Vote]"
	case "hongbao":
		return "[Red Packet]"
	case "sticker":
		return "[Sticker]"
	case "interactive":
		return "[Card]"
	case "video_chat":
		return "[Video Call]"
	case "share_chat":
		return "[Group Card]"
	case "share_user":
		return "[Contact Card]"
	case "merge_forward":
		return "[Chat History]"
	case "post":
		return "[Rich Text]"
	case "calendar":
		return "[Event]"
	case "share_calendar_event":
		return "[Shared Event]"
	case "general_calendar":
		return "[Event]"
	case "system":
		return "[System Message]"
	case "":
		return ""
	default:
		return "[" + msgType + "]"
	}
}

// chatTitle is the chat's name, plus the account suffix that tells same-named
// colleagues apart.
func chatTitle(c store.Chat) (name, suffix string) {
	return cmp.Or(flatten(c.Name), c.ChatID), c.PeerSuffix()
}

// isBotChat reports whether the title carries the badge. Only a p2p peer is a
// property of the chat itself; a group holds whoever its members invite, so a
// bot's turn there is marked on the summary line that quotes it.
func isBotChat(c store.Chat) bool { return c.P2PTargetType == "bot" }

// chatSummary is the second line's text: who said what, or why there is
// nothing to show. It comes back in pieces when the message spells an official
// emoji no character carries, which the client draws as a picture here too.
func chatSummary(c store.Chat, self string, pics emojiPics) (string, []rowSeg) {
	if c.LastMessageID == "" {
		if c.SyncError != "" {
			return stDim.Render("history unavailable"), nil
		}
		return stDim.Render("New chat"), nil
	}
	sender := personName(cmp.Or(flatten(c.LastSenderName), c.LastSenderID), c.LastSenderSuffix())
	if c.LastDeleted {
		return stDim.Render(sender + " recalled a message"), nil
	}

	body := lastMessageSummary(c)
	switch {
	case body != "":
	case c.LastRenderedAt == 0:
		body = pendingSummary(c)
	default:
		body = msgTypeLabel(c.LastMsgType)
	}
	// A todo's rendering leads with the box its task state keeps; the line the
	// gist draws stands the same circles in for it the pane below does, read
	// from the state the row carries rather than the glyph it spells. The
	// gate keeps a ballot box a text message merely types as its own words.
	lead := ""
	if c.LastMsgType == "todo" {
		lead, body = todoGist(c.LastTodoDone, body)
	}

	// p2p names the peer in the title already, so only the user's own turn
	// needs a prefix there. A system message is nobody's turn.
	prefix := ""
	switch {
	case sender == "":
	case c.LastSenderID == self:
		prefix = "You: "
	case c.ChatMode != "p2p":
		prefix = sender + botMark(c.LastSenderType) + ": "
	}
	// The line is dim as a whole, so an @ that reaches the reader is the one
	// thing on it that still carries a colour.
	ms := mentionsIn(c.LastMentionsJSON, self).on(stDim).spelling(spellOf(c.LastMsgType))
	if lead == "" {
		if segs := ms.segs(prefix+body, pics.gist); segs != nil {
			return "", segs
		}
		return ms.render(prefix + body), nil
	}
	// The box sits between the sender and the task: the prefix is the list's
	// chrome, the box the message's own content.
	if segs := ms.segs(body, pics.gist); segs != nil {
		return "", append([]rowSeg{{text: ms.render(prefix) + lead}}, segs...)
	}
	return ms.render(prefix) + lead + ms.render(body), nil
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
	// A forward's body is the whole of another chat pressed into one string;
	// the client names it rather than quoting it, and so does this line.
	if c.LastMsgType == "merge_forward" {
		return msgTypeLabel(c.LastMsgType)
	}
	// A picture, a clip and a file each render into markup naming their key,
	// which is fifty characters the words beside them would be crowded off the
	// line by. Each is named here instead, where it was written: the line says
	// what the message holds, and the pane below draws it.
	return flatten(gistBody(c.LastContent))
}

// chatChipLimit is how many reactions a chat row shows. Past three the icons
// crowd out the message they sit in front of.
const chatChipLimit = 3

// chatChipCols is how wide one reaction picture is drawn in the chat list.
// The text half of the pane is narrow, so a picture gets half of what it gets
// beside a message.
const chatChipCols = 2

// chatChipGap parts one badge from the next. Two caps meeting read as a
// single badge pinched at the waist, so a cell goes between them. One cell
// rather than the message strip's two: a badge here holds an icon alone, with
// no count for the gap to be read as part of.
const chatChipGap = " "

// chatChips are the reactions on a chat's newest message, as the icons alone:
// who reacted and how many did is the message pane's to say, where there is
// room for names. A recall takes them with the body, the way the client does.
//
// A badge stands for one reaction, so each icon wears its own, the way the
// client draws them and the way the message strip already does.
//
// A terminal that draws no pictures gets no badges: the row is icons-only by
// design, and a name spelled out would cost more room than the message behind
// it. An emoji this build knows nothing of is left out for the same reason.
func chatChips(c store.Chat, pics emojiPics) []rowSeg {
	if c.LastDeleted {
		return nil
	}
	var out []rowSeg
	shown := 0
	for _, chip := range emoji.Summary(c.LastReactionsJSON, "") {
		if shown == chatChipLimit {
			break
		}
		e, known := emoji.ByKey(chip.Key)
		if !known {
			continue
		}
		pic := pics.chip(e.Key, chatChipCols)
		if pic.cols == 0 {
			continue
		}
		if shown > 0 {
			out = append(out, rowSeg{text: chatChipGap})
		}
		out = append(out,
			rowSeg{text: stChipEdge.Render(chipLeft)},
			rowSeg{pic: pic},
			rowSeg{text: stChipEdge.Render(chipRight)})
		shown++
	}
	return out
}

// chatSummaryLine is the row's second line: what someone suggested and what
// the reader left unsent, then the reactions the chat collected, then who said
// what, then the mute mark at the far edge. It comes back in pieces only when a reaction is a picture — a
// line of characters stays one string, which is what lets a selection tint it.
func chatSummaryLine(c store.Chat, d store.Draft, cands int, g rowGist, w int) (string, []rowSeg) {
	mine := candMark(cands) + selfMark(d)
	chips := g.chips
	// A message that wants the reader outranks the reactions on it, so the
	// mention badge stands in their place rather than beside them.
	if at := atMeMark(c.UnreadMention); at != "" {
		chips = []rowSeg{{text: at}}
	}
	room := w - segsWidth(chips) - lipgloss.Width(mine)
	if len(chips) > 0 {
		room -= len(chatChipGap)
	}
	body := []rowSeg{{text: padBetween(g.text, muteMark(c.Muted), max(0, room))}}
	if g.summary != nil {
		body = padSegs(g.summary, muteMark(c.Muted), max(0, room))
	}

	var segs []rowSeg
	if mine != "" {
		segs = append(segs, rowSeg{text: mine})
	}
	segs = append(segs, chips...)
	if len(chips) > 0 {
		segs = append(segs, rowSeg{text: chatChipGap})
	}
	return oneLineOr(append(segs, body...))
}

// oneLineOr keeps a line of characters alone as one string, which is what
// lets a selection tint it; only a picture in it forces the pieces on the
// pane, which draws each of them itself.
func oneLineOr(segs []rowSeg) (string, []rowSeg) {
	if slices.ContainsFunc(segs, func(s rowSeg) bool { return s.pic.cols > 0 }) {
		return "", segs
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String(), nil
}

// markName styles a name with the runes a filter landed on marked. The mark
// is dotted rather than straight: a straight underline is what a link and a
// post's <u> wear, and this one is larkim's own doing, not the text's. It is
// folded into the base style rather than nested inside it: an inner reset
// would end the bold for the rest of the line. Runs are rendered whole so a
// name costs a handful of escapes rather than one per rune.
func markName(s string, pos []int, base lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(s)
	}
	at := make(map[int]bool, len(pos))
	for _, p := range pos {
		at[p] = true
	}
	under := base.UnderlineStyle(lipgloss.UnderlineDotted).UnderlineColor(colAccent)
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
func renderChatRow(av avatars, r listRow, d store.Draft, unread int64, cands int, g rowGist, now time.Time, w int, mark []int) chatRow {
	c := r.chat
	avatarTop, avatarBottom, badged := av.cells(r, unread)
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

	bottom, segs := chatSummaryLine(c, d, cands, g, textWidth)
	return chatRow{
		avatarTop:    avatarTop,
		avatarBottom: avatarBottom,
		top:          padBetween(title, right, textWidth),
		bottom:       bottom,
		segs:         segs,
	}
}

// renderUnreadRow lays the Unread row out: what the panel would gather, over
// the same two lines every other row takes. It draws no time, the page it
// opens spanning whatever stretch the backlog does.
//
// It carries no badge. Every message it would count is already badged on the
// row of the chat it is waiting in, and those rows are on the same screen, so
// a number here would be the same backlog counted twice; how many chats are
// waiting is what the row has to say that they do not.
func renderUnreadRow(av avatars, r listRow, rows []listRow, unread map[string]int64, w int) chatRow {
	textWidth := chatTextWidth(w)
	avatarTop, avatarBottom, _ := av.cells(r, 0)
	chats := 0
	for _, row := range rows {
		if feedWaiting(row, unread) {
			chats++
		}
	}
	title, bottom := stBold.Render(unreadLabel), plural(chats, "chat", "chats")+" waiting"
	if chats == 0 {
		title, bottom = stDim.Render(unreadLabel), "nothing waiting"
	}
	return chatRow{
		avatarTop:    avatarTop,
		avatarBottom: avatarBottom,
		top:          fit(title, textWidth),
		bottom:       fit(stDim.Render(bottom), textWidth),
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
func muteMark(muted bool) string {
	if !muted {
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
// messages waiting, then the build's version, and, at the far right, the dot
// that says the do-not-disturb chats have something too. The dot sits in the
// column every row's mute mark is right-aligned to, so the setting reads down
// a single column.
func chatsHeader(rows []listRow, unread map[string]int64, filter, version string, w int) string {
	n, muted := unreadMessages(rows, unread)
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
	// The button stands whether or not anything is waiting. Neither signal
	// beside it answers for the whole list — the count leaves muted chats
	// out and the badge query leaves silenced ones out — so hiding the
	// button on either would take it away in the state that most wants it.
	// A press with nothing waiting says so.
	right := stDim.Render(markAllGlyph) + cmp.Or(dot, " ")
	room := w - lipgloss.Width(count) - lipgloss.Width(right) - 1
	left := stBold.Render(truncate(title, room)) + count
	// The client names its version only under Settings › About, which the TUI
	// has no counterpart for. It is the first thing to go: shown whole after a
	// title that fits, or not at all.
	if tag := versionTag(version); tag != "" && lipgloss.Width(title)+1+lipgloss.Width(tag) <= room {
		left += " " + stDim.Render(tag)
	}
	return padBetween(left, right, w)
}

// versionTag is how the header spells a version: a release reads v0.21.7, a
// local build's dev-<commit> as it is.
func versionTag(version string) string {
	if version != "" && version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

// unreadMessages sums the messages waiting for an answer and reports whether
// any row on do-not-disturb is among them. The sum is what the badges below
// add up to, so the header and the rows state the same quantity — a thread's
// replies among them, since the thread carries a badge of its own here.
// Muted chats stay out of it: the reader asked not to be counted at for them,
// and the dot is all the header says about them. The filter is not applied —
// hiding rows is a lens on the list, not a change to what is waiting.
func unreadMessages(rows []listRow, unread map[string]int64) (n int64, muted bool) {
	for _, r := range rows {
		switch {
		case r.unread(unread) <= 0:
		case r.chat.Muted:
			muted = true
		default:
			n += r.unread(unread)
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
