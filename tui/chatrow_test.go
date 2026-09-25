package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 9, 23, 10, 0, 0, 0, time.Local)

func at(d time.Duration) int64 { return testNow.Add(d).UnixMilli() }

func plainRow(c store.Chat, unread int64, w int) (string, string) {
	r := renderChatRow(textAvatars{}, c, unread, "ou_me", testNow, w, emojiPics{})
	return ansi.Strip(r.top), ansi.Strip(r.bottom)
}

func TestChatTime_BucketsByCalendarDay(t *testing.T) {
	for _, tc := range []struct {
		name string
		when time.Time
		want string
	}{
		{"today keeps the clock", testNow.Add(-2 * time.Hour), "08:00"},
		{"earlier today", time.Date(2026, 9, 23, 0, 5, 0, 0, time.Local), "00:05"},
		{"yesterday", time.Date(2026, 9, 22, 23, 59, 0, 0, time.Local), "昨天"},
		{"within the week", time.Date(2026, 9, 20, 12, 0, 0, 0, time.Local), "周日"},
		{"older this year", time.Date(2026, 3, 1, 12, 0, 0, 0, time.Local), "03-01"},
		{"another year", time.Date(2025, 12, 31, 12, 0, 0, 0, time.Local), "2025-12-31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, chatTime(tc.when.UnixMilli(), testNow))
		})
	}
	require.Empty(t, chatTime(0, testNow), "a chat with no message has no timestamp")
}

func TestRenderChatRow_ShowsSenderOnlyForGroups(t *testing.T) {
	base := store.Chat{ChatID: "oc_1", LastMessageID: "om_1", LastMessageMs: at(-time.Hour),
		LastSenderID: "ou_them", LastSenderName: "周舟", LastContent: "收到", LastRenderedAt: 1}

	p2p := base
	p2p.Name, p2p.ChatMode = "周舟", "p2p"
	_, bottom := plainRow(p2p, 0, 31)
	require.Contains(t, bottom, "收到")
	require.NotContains(t, bottom, "周舟:", "p2p names the peer in the title already")

	group := base
	group.Name, group.ChatMode = "示例告警群", "group"
	_, bottom = plainRow(group, 0, 31)
	require.Contains(t, bottom, "周舟: 收到")
}

func TestRenderChatRow_PrefixesTheUsersOwnTurn(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "李明", ChatMode: "p2p", LastMessageID: "om_1",
		LastSenderID: "ou_me", LastSenderName: "林岚", LastContent: "好的", LastRenderedAt: 1}
	_, bottom := plainRow(c, 0, 31)
	require.Contains(t, bottom, "你: 好的", "p2p still marks the user's own turn")

	c.ChatMode, c.Name = "group", "示例告警群"
	_, bottom = plainRow(c, 0, 31)
	require.Contains(t, bottom, "你: 好的")
}

func TestRenderChatRow_SystemMessageNamesNoSender(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "抖音评论兼职伙伴群", ChatMode: "group", LastMessageID: "om_1",
		LastMessageMs: at(-time.Hour), LastMsgType: "system", LastRenderedAt: 5,
		LastContent: "韩立 invited 柳依依 to the group."}
	_, bottom := plainRow(c, 0, 40)
	require.Equal(t, "韩立 invited 柳依依 to the group.", strings.TrimRight(bottom, " "))
}

func TestRenderChatRow_EmptyStatesReadDifferently(t *testing.T) {
	for _, tc := range []struct {
		name string
		chat store.Chat
		want string
	}{
		{"no messages", store.Chat{ChatID: "oc_1", Name: "新群"}, "New chat"},
		{"restricted chat", store.Chat{ChatID: "oc_1", Name: "受限群", SyncError: "forbidden"}, "history unavailable"},
		{"rendering pending", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1", LastMsgType: "text",
			LastContentRaw: `{"text":"稍后渲染"}`, LastRenderedAt: 0}, "稍后渲染"},
		{"rendering pending, unreadable body", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1",
			LastMsgType: "interactive", LastContentRaw: `{"json_card":"{}"}`, LastRenderedAt: 0}, "[卡片]"},
		{"renders to nothing", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1", LastMsgType: "system", LastRenderedAt: 5}, "[系统消息]"},
		{"recalled", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1", LastSenderName: "孙琪", LastDeleted: true, LastRenderedAt: 5}, "孙琪撤回了一条消息"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, bottom := plainRow(tc.chat, 0, 31)
			require.Contains(t, bottom, tc.want)
		})
	}
}

func TestRenderChatRow_AppendsTheAccountSuffix(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "李明", ChatMode: "p2p", PeerAccount: "liming01@example.com"}
	top, _ := plainRow(c, 0, 31)
	require.Contains(t, top, "李明01")

	c.PeerAccount = "zhouzhou@example.com"
	c.Name = "孙琪"
	top, _ = plainRow(c, 0, 31)
	require.Contains(t, top, "孙琪")
	require.NotContains(t, top, "(", "an account with no number is not a disambiguator")
}

func TestRenderChatRow_TitleBadgesOnlyABotPeer(t *testing.T) {
	bot := store.Chat{ChatID: "oc_1", Name: "监控告警", ChatMode: "p2p", P2PTargetType: "bot"}
	top, _ := plainRow(bot, 0, 31)
	require.Contains(t, top, botBadge)

	person := store.Chat{ChatID: "oc_2", Name: "孙琪", ChatMode: "p2p", P2PTargetType: "user"}
	top, _ = plainRow(person, 0, 31)
	require.NotContains(t, top, botBadge)

	group := store.Chat{ChatID: "oc_3", Name: "流程中心", ChatMode: "group", LastSenderType: "app",
		LastMessageID: "om_1", LastMessageMs: at(-time.Hour), LastSenderID: "ou_bot",
		LastSenderName: "Factory", LastContent: "任务执行日报", LastRenderedAt: 1}
	top, bottom := plainRow(group, 0, 31)
	require.NotContains(t, top, botBadge, "a group holds whoever its members invite")
	require.Contains(t, bottom, "Factory"+botBadge+": ", "the turn it quotes is the machine's")
}

func TestChatSummary_LeavesAHumanTurnUnbadged(t *testing.T) {
	group := store.Chat{ChatID: "oc_1", Name: "流程中心", ChatMode: "group", LastSenderType: "user",
		LastMessageID: "om_1", LastMessageMs: at(-time.Hour), LastSenderID: "ou_them",
		LastSenderName: "周舟", LastContent: "收到", LastRenderedAt: 1}
	_, bottom := plainRow(group, 0, 31)
	require.Contains(t, bottom, "周舟: ")
	require.NotContains(t, bottom, botBadge)
}

func TestRenderChatRow_KeepsTheRightEdgeAlignedAndBothLinesInWidth(t *testing.T) {
	const w = 31
	long := store.Chat{
		ChatID: "oc_1", Name: "林岚's AI Assistant 名字非常非常长会被截断", ChatMode: "p2p",
		P2PTargetType: "bot", PeerAccount: "linlan01@example.com",
		LastMessageID: "om_1", LastMessageMs: at(-30 * time.Hour),
		LastSenderName: "林岚", LastContent: strings.Repeat("很长的内容", 20), LastRenderedAt: 1,
	}
	r := renderChatRow(textAvatars{}, long, 12, "ou_me", testNow, w, emojiPics{})
	require.Equal(t, chatTextWidth(w), lipgloss.Width(r.top), "the text half fills its column exactly")
	require.Equal(t, chatTextWidth(w), lipgloss.Width(r.bottom))
	require.Equal(t, avatarWidth, lipgloss.Width(r.avatarTop), "and the avatar keeps its own")
	require.Equal(t, avatarWidth, lipgloss.Width(r.avatarBottom))

	top := ansi.Strip(r.top)
	require.True(t, strings.HasSuffix(strings.TrimRight(top, " "), "昨天"), "the timestamp keeps the right edge, got %q", top)
	require.Contains(t, top, "12", "the unread count survives truncation")
	require.Contains(t, top, "…01", "so does the account suffix")
	require.Contains(t, top, "…", "the name is what gives way")
}

func TestRenderChats_LeavesTheOddLineBlankRatherThanHalveAChat(t *testing.T) {
	m := sized(130, 30)
	fit := m.chatListHeight()
	require.Equal(t, 1, m.listHeight()-(fit*chatRowStride-chatRowGap),
		"this size is the interesting one: one line over what the chats take")
	out := ansi.Strip(m.renderChats(m.bodyHeight()))

	require.Contains(t, out, "群 0 ", "the first chat is drawn")
	require.Contains(t, out, fmt.Sprintf("群 %d ", fit-1), "so is the last one that fits whole")
	require.NotContains(t, out, fmt.Sprintf("群 %d ", fit), "the chat that would be halved is left out")

	body := strings.Split(out, "\n")[2:]
	blank := 0
	for _, l := range body[:len(body)-1] { // the last line is the pane's bottom border
		if strings.TrimSpace(strings.Trim(l, "│")) == "" {
			blank++
		}
	}
	require.Equal(t, fit, blank, "one separator between each pair of chats, plus the leftover line")
}

func TestRenderChats_SeparatesEachChatFromTheNextButNotFromTheFoot(t *testing.T) {
	m := sized(130, 29)
	fit := m.chatListHeight()
	require.Equal(t, 0, m.listHeight()-(fit*chatRowStride-chatRowGap),
		"this size is the interesting one: the chats take the body exactly")

	body := strings.Split(ansi.Strip(m.renderChats(m.bodyHeight())), "\n")
	body = body[2 : len(body)-1] // past the top border and the title, short of the bottom border
	require.Len(t, body, m.listHeight())
	for i, l := range body {
		blank := strings.TrimSpace(strings.Trim(l, "│")) == ""
		require.Equal(t, i%chatRowStride == chatRowHeight, blank,
			"line %d of the list: %q", i, l)
	}
}

func TestRenderChats_HoldsTogetherAtTheNarrowestSupportedWidth(t *testing.T) {
	m := sized(minWidth, 24)
	require.Equal(t, chatsWidth+minMessagesWidth, minWidth, "minWidth is what the two left panes need")

	out := m.renderChats(m.bodyHeight())
	for _, line := range strings.Split(ansi.Strip(out), "\n") {
		require.Equal(t, chatsWidth, lipgloss.Width(line), "every pane line is exactly the pane's width: %q", line)
	}
	require.NotPanics(t, func() { m.View() })
}

// badgedAvatars stands in for the picture renderer once the picture carries
// the unread counter itself.
type badgedAvatars struct{}

func (badgedAvatars) cells(store.Chat, int64) (string, string, bool) {
	return "····", "····", true
}
func (badgedAvatars) prepare([]store.Chat, map[string]int64) string { return "" }

func TestRenderChatRow_LeavesTheCountToAPictureThatCarriesIt(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "Alpha", ChatMode: "group",
		LastMessageID: "om_1", LastMessageMs: at(-time.Hour), LastSenderName: "Bob", LastContent: "hi"}

	on := ansi.Strip(renderChatRow(badgedAvatars{}, c, 12, "ou_me", testNow, 40, emojiPics{}).top)
	require.NotContains(t, on, "12", "the avatar shows it, so the title line keeps the room")

	off, _ := plainRow(c, 12, 40)
	require.Contains(t, off, "12", "a colour block carries nothing, so the number stays in the text")
}

func TestRenderChatRow_MarksAMutedChat(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "Alpha", ChatMode: "group",
		LastMessageID: "om_1", LastMessageMs: at(-time.Hour), LastSenderName: "Bob", LastContent: "hi"}

	_, bottom := plainRow(c, 0, 40)
	require.NotContains(t, bottom, muteGlyph)

	c.Muted = true
	row := renderChatRow(textAvatars{}, c, 0, "ou_me", testNow, 40, emojiPics{})
	require.Contains(t, ansi.Strip(row.bottom), muteGlyph, "a muted chat with nothing unread still says so")
	require.Equal(t, chatTextWidth(40), lipgloss.Width(row.bottom), "and the mark stays inside the row")
}

func TestChatSummary_NamesACardRatherThanItsContent(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", ChatMode: "group", LastMessageID: "om_1", LastRenderedAt: 1,
		LastSenderName: "构建机器人", LastMsgType: "interactive",
		LastContentRaw: weeklyCard.ContentRaw, LastContent: "<card title=\"旧渲染\">\n待认领账号：13 个\n</card>"}
	_, bottom := plainRow(c, 0, 36)
	require.Contains(t, bottom, "设备版本周报", "the summary is read from the card itself")
	require.NotContains(t, bottom, "旧渲染")
	require.NotContains(t, bottom, "待认领", "the body is a screen of its own, not a summary line")
}

func TestRenderChatRow_GreysTheCounterOfAMutedChat(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "Alpha", ChatMode: "group",
		LastMessageID: "om_1", LastMessageMs: at(-time.Hour), LastSenderName: "Bob", LastContent: "hi"}

	loud := renderChatRow(textAvatars{}, c, 3, "ou_me", testNow, 40, emojiPics{}).top
	c.Muted = true
	quiet := renderChatRow(textAvatars{}, c, 3, "ou_me", testNow, 40, emojiPics{}).top

	require.Contains(t, ansi.Strip(loud), "3")
	require.Contains(t, ansi.Strip(quiet), "3", "the count is still there")
	require.NotEqual(t, loud, quiet, "but do-not-disturb takes the accent off it")
	require.Contains(t, quiet, stDim.Render("3"))
}

func TestChatSummary_KeepsAMentionOfTheReaderVisibleThroughTheDim(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "项目协作群", ChatMode: "group", LastMessageID: "om_1", LastRenderedAt: 1,
		LastSenderName: "孙琪", LastSenderID: "ou_x", LastContent: "@林岚 看下",
		LastMentionsJSON: `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`}
	row := renderChatRow(textAvatars{}, c, 0, "ou_me", testNow, 36, emojiPics{})
	require.Contains(t, row.bottom, stMentionMe.Render("@林岚"))
	require.Contains(t, ansi.Strip(row.bottom), "孙琪: "+chipLeft+"@林岚"+chipRight+" 看下",
		"the badge keeps its caps at summary width")

	c.LastContent, c.LastMentionsJSON = "@_all all", ""
	row = renderChatRow(textAvatars{}, c, 0, "ou_me", testNow, 36, emojiPics{})
	require.Contains(t, ansi.Strip(row.bottom), "孙琪: @All all")
}

func TestChatSummary_LeavesAMentionOfSomebodyElseInTheRowsOwnDim(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "项目协作群", ChatMode: "group", LastMessageID: "om_1", LastRenderedAt: 1,
		LastSenderName: "孙琪", LastSenderID: "ou_x", LastContent: "@李四 看下",
		LastMentionsJSON: `[{"id":"ou_a","key":"@_user_1","name":"李四"}]`}
	row := renderChatRow(textAvatars{}, c, 0, "ou_me", testNow, 36, emojiPics{})
	require.Contains(t, row.bottom, stDim.Render("@李四"))
	require.NotContains(t, row.bottom, stAccent.Render("@李四"),
		"the summary is one line: colouring an @ that is not the reader's says nothing")
}

// reactedP2P is a one-to-one chat whose newest message collected reactions,
// in the order the keys are given.
func reactedP2P(keys ...string) store.Chat {
	var counts, details []string
	for i, k := range keys {
		counts = append(counts, `{"reaction_type":"`+k+`","count":"2"}`)
		details = append(details, fmt.Sprintf(`{"emoji_type":%q,"action_time":"%d","operator":{"operator_id":"ou_a"}}`, k, 1790155040+i))
	}
	return store.Chat{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p",
		LastMessageID: "om_1", LastMessageMs: at(-time.Hour), LastRenderedAt: 1,
		LastSenderID: "ou_me", LastSenderName: "林岚", LastContent: "明天上午的排期",
		LastReactionsJSON: `{"counts":[` + strings.Join(counts, ",") + `],"details":[` + strings.Join(details, ",") + `]}`}
}

func TestChatSummary_LeadsAP2PLineWithTheReactionIcons(t *testing.T) {
	_, bottom := plainRow(reactedP2P("THUMBSUP", "HEART"), 0, 40)
	require.True(t, strings.HasPrefix(bottom, chipLeft+"👍 ❤️"+chipRight+" 你: 明天上午的排期"), "got %q", bottom)
	require.NotContains(t, bottom, "2", "in a chat of two, how many reacted says nothing")
}

func TestChatSummary_LeavesAGroupsReactionsToTheMessagePane(t *testing.T) {
	c := reactedP2P("THUMBSUP")
	c.ChatMode, c.Name = "group", "平台组"
	_, bottom := plainRow(c, 0, 40)
	require.NotContains(t, bottom, "👍", "a group's reactions need the room to say whose they are")
}

func TestChatSummary_DropsTheReactionsOfARecalledMessage(t *testing.T) {
	c := reactedP2P("THUMBSUP")
	c.LastDeleted = true
	_, bottom := plainRow(c, 0, 40)
	require.NotContains(t, bottom, "👍", "the client drops a recalled message's reactions with its body")
	require.Contains(t, bottom, "撤回了一条消息")
}

func TestChatSummary_ShowsNoMoreThanThreeReactions(t *testing.T) {
	_, bottom := plainRow(reactedP2P("THUMBSUP", "HEART", "ROSE", "MUSCLE"), 0, 44)
	require.True(t, strings.HasPrefix(bottom, chipLeft+"👍 ❤️ 🌹"+chipRight+" 你:"), "got %q", bottom)
	require.NotContains(t, bottom, "💪", "past three the icons crowd out the message behind them")
}

func TestChatSummary_LeavesOutAnEmojiItCanDrawNoWayAtAll(t *testing.T) {
	// No graphics here and no pictures cut out, so OK has neither a character
	// nor a picture. Its name at the head of the line would cost more room
	// than the message it sits in front of.
	_, bottom := plainRow(reactedP2P("OK", "THUMBSUP"), 0, 40)
	require.True(t, strings.HasPrefix(bottom, chipLeft+"👍"+chipRight+" 你:"), "got %q", bottom)
	require.NotContains(t, bottom, "[OK]")
}

func TestChatSummary_KeepsTheBodyClearOfTheReactions(t *testing.T) {
	const w = 31
	c := reactedP2P("THUMBSUP", "HEART", "ROSE")
	c.LastContent = strings.Repeat("很长的内容", 20)
	row := renderChatRow(textAvatars{}, c, 0, "ou_me", testNow, w, emojiPics{})
	require.Equal(t, chatTextWidth(w), lipgloss.Width(row.bottom), "the line fills its column exactly")
	require.True(t, strings.HasPrefix(ansi.Strip(row.bottom), chipLeft+"👍 ❤️ 🌹"+chipRight+" "),
		"the icons are what survives, the body gives way")
}

func TestChatSummary_DrawsAReactionNoCharacterCarriesAsAPicture(t *testing.T) {
	dir := t.TempDir()
	writeTestEmoji(t, dir, "OK")
	pics := emojiPics{dir: dir, place: picturesIn(dir).place}

	const w = 40
	row := renderChatRow(textAvatars{}, reactedP2P("OK"), 0, "ou_me", testNow, w, pics)
	require.Empty(t, row.bottom, "a picture in the line is what puts it in pieces")
	require.Equal(t, chipLeft, ansi.Strip(row.segs[0].text), "the chip opens the line")
	require.Positive(t, row.segs[1].pic.cols, "the icon is the client's own picture")
	require.True(t, row.segs[1].pic.chip, "the cells it sits in carry the chip's tint")
	require.Equal(t, 1, row.segs[1].pic.rows, "an icon on a line of text is one row tall")
	require.LessOrEqual(t, row.segs[1].pic.cols, chatChipCols, "narrower here than beside a message")
	require.Equal(t, chatTextWidth(w), segsWidth(row.segs), "the line still fills its column exactly")
	require.Contains(t, ansi.Strip(row.segs[len(row.segs)-1].text), "你: 明天上午的排期")
}
