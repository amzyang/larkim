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
	r := renderChatRow(textAvatars{}, c, unread, "ou_me", testNow, w)
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
		LastSenderID: "ou_them", LastSenderName: "蒋贝乐", LastContent: "收到", LastRenderedAt: 1}

	p2p := base
	p2p.Name, p2p.ChatMode = "蒋贝乐", "p2p"
	_, bottom := plainRow(p2p, 0, 31)
	require.Contains(t, bottom, "收到")
	require.NotContains(t, bottom, "蒋贝乐:", "p2p names the peer in the title already")

	group := base
	group.Name, group.ChatMode = "灵创告警群", "group"
	_, bottom = plainRow(group, 0, 31)
	require.Contains(t, bottom, "蒋贝乐: 收到")
}

func TestRenderChatRow_PrefixesTheUsersOwnTurn(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "陈建伟", ChatMode: "p2p", LastMessageID: "om_1",
		LastSenderID: "ou_me", LastSenderName: "邹洋", LastContent: "好的", LastRenderedAt: 1}
	_, bottom := plainRow(c, 0, 31)
	require.Contains(t, bottom, "你: 好的", "p2p still marks the user's own turn")

	c.ChatMode, c.Name = "group", "灵创告警群"
	_, bottom = plainRow(c, 0, 31)
	require.Contains(t, bottom, "你: 好的")
}

func TestRenderChatRow_EmptyStatesReadDifferently(t *testing.T) {
	for _, tc := range []struct {
		name string
		chat store.Chat
		want string
	}{
		{"no messages", store.Chat{ChatID: "oc_1", Name: "新群"}, "New chat"},
		{"restricted chat", store.Chat{ChatID: "oc_1", Name: "受限群", SyncError: "forbidden"}, "history unavailable"},
		{"rendering pending", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1", LastRenderedAt: 0}, "…"},
		{"renders to nothing", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1", LastMsgType: "system", LastRenderedAt: 5}, "[系统消息]"},
		{"recalled", store.Chat{ChatID: "oc_1", Name: "群", LastMessageID: "om_1", LastSenderName: "王将", LastDeleted: true, LastRenderedAt: 5}, "王将撤回了一条消息"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, bottom := plainRow(tc.chat, 0, 31)
			require.Contains(t, bottom, tc.want)
		})
	}
}

func TestRenderChatRow_AppendsTheAccountSuffix(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "陈建伟", ChatMode: "p2p", PeerAccount: "chenjianwei01@gaotu.cn"}
	top, _ := plainRow(c, 0, 31)
	require.Contains(t, top, "陈建伟 (01)")

	c.PeerAccount = "wangjiang@gaotu.cn"
	c.Name = "王将"
	top, _ = plainRow(c, 0, 31)
	require.Contains(t, top, "王将")
	require.NotContains(t, top, "(", "an account with no number is not a disambiguator")
}

func TestRenderChatRow_BotBadgeFollowsTheChatKind(t *testing.T) {
	bot := store.Chat{ChatID: "oc_1", Name: "天眼告警", ChatMode: "p2p", P2PTargetType: "bot"}
	top, _ := plainRow(bot, 0, 31)
	require.Contains(t, top, botBadge)

	person := store.Chat{ChatID: "oc_2", Name: "王将", ChatMode: "p2p", P2PTargetType: "user"}
	top, _ = plainRow(person, 0, 31)
	require.NotContains(t, top, botBadge)

	group := store.Chat{ChatID: "oc_3", Name: "流程中心", ChatMode: "group", LastSenderType: "app"}
	top, _ = plainRow(group, 0, 31)
	require.Contains(t, top, botBadge, "a group is flagged by whoever spoke last")
}

func TestRenderChatRow_KeepsTheRightEdgeAlignedAndBothLinesInWidth(t *testing.T) {
	const w = 31
	long := store.Chat{
		ChatID: "oc_1", Name: "邹洋's AI Assistant 名字非常非常长会被截断", ChatMode: "p2p",
		P2PTargetType: "bot", PeerAccount: "zouyang01@gaotu.cn",
		LastMessageID: "om_1", LastMessageMs: at(-30 * time.Hour),
		LastSenderName: "邹洋", LastContent: strings.Repeat("很长的内容", 20), LastRenderedAt: 1,
	}
	r := renderChatRow(textAvatars{}, long, 12, "ou_me", testNow, w)
	require.Equal(t, chatTextWidth(w), lipgloss.Width(r.top), "the text half fills its column exactly")
	require.Equal(t, chatTextWidth(w), lipgloss.Width(r.bottom))
	require.Equal(t, avatarWidth, lipgloss.Width(r.avatarTop), "and the avatar keeps its own")
	require.Equal(t, avatarWidth, lipgloss.Width(r.avatarBottom))

	top := ansi.Strip(r.top)
	require.True(t, strings.HasSuffix(strings.TrimRight(top, " "), "昨天"), "the timestamp keeps the right edge, got %q", top)
	require.Contains(t, top, "12", "the unread count survives truncation")
	require.Contains(t, top, "(01)", "so does the account suffix")
	require.Contains(t, top, "…", "the name is what gives way")
}

func TestRenderChats_LeavesTheOddLineBlankRatherThanHalveAChat(t *testing.T) {
	m := sized(130, 30)
	require.Equal(t, 1, m.listHeight()%chatRowHeight, "this size is the interesting one: an odd body")
	out := ansi.Strip(m.renderChats(m.bodyHeight()))

	fit := m.chatListHeight()
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
	require.Equal(t, 1, blank, "exactly the leftover line stays empty")
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
