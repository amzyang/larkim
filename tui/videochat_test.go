package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// liveCall and endedCall are the two shapes a video_chat body takes: the
// invite as it arrives, and the same message once Feishu closes the call.
const (
	liveCall  = `{"topic":"项目协作群 - 张三的视频会议","meet_number":"100000000","start_time":"1000"}`
	endedCall = `{"topic":"项目协作群 - 张三的视频会议","meet_number":"100000000","start_time":"1000","end_time":"33000"}`
)

func callMessage(contentRaw string, renderedAt int64) []store.Message {
	return []store.Message{{MessageID: "om_1", SenderName: "张三", MsgType: "video_chat",
		ContentRaw: contentRaw, Content: "[Video call]", CreateMs: msgAt(23, 9, 0), RenderedAt: renderedAt}}
}

func TestBodyRows_ALiveCallCardsTheMeetingWithAJoinButton(t *testing.T) {
	rows := renderRows(callMessage(liveCall, 1), baseStyle())
	out := rowText(rows)
	require.Contains(t, out, "项目协作群 - 张三的视频会议")
	require.Contains(t, out, "Meeting ID: 100 000 000", "the number is grouped the way the client spells it")
	require.Contains(t, out, "live")
	require.Contains(t, out, "Join")
	require.NotContains(t, out, "[Video call]", "the card replaces the placeholder, it does not repeat it")

	join, ok := zoneRow(rows)
	require.True(t, ok, "a live call carries a join target: %q", out)
	z := firstZone(join)
	require.Equal(t, []string{"lark://vc.feishu.cn/j/100000000"}, z.urls)
	require.Equal(t, leadWidth, z.x0, "the target starts where the button is drawn")
	require.Equal(t, z.x0+lipgloss.Width(" Join "), z.x1)
}

func TestBodyRows_AnEndedCallShowsItsLengthAndNoWayIn(t *testing.T) {
	rows := renderRows(callMessage(endedCall, 1), baseStyle())
	out := rowText(rows)
	require.Contains(t, out, "项目协作群 - 张三的视频会议")
	require.Contains(t, out, "32s", "the span stands where the live marker was")
	require.NotContains(t, out, "Join")
	require.NotContains(t, out, "live")
	_, ok := zoneRow(rows)
	require.False(t, ok, "a call that has ended is not joinable")
}

func TestBodyRows_ACallCardsBeforeItsRenderingLands(t *testing.T) {
	// The body carries the whole invite, so the card costs no render call —
	// which matters, because a call is worth joining in its first seconds.
	rows := renderRows(callMessage(liveCall, 0), baseStyle())
	out := rowText(rows)
	require.Contains(t, out, "Meeting ID: 100 000 000")
	_, ok := zoneRow(rows)
	require.True(t, ok)
}

func TestBodyRows_ACallBodyLarkimCannotReadKeepsTheRendering(t *testing.T) {
	rows := renderRows(callMessage("not json", 1), baseStyle())
	require.Contains(t, rowText(rows), "[Video call]")
	_, ok := zoneRow(rows)
	require.False(t, ok)
}

func TestBodyRows_ACallWithoutAMeetingNumberOffersNoJoin(t *testing.T) {
	rows := renderRows(callMessage(`{"topic":"项目协作群 - 张三的视频会议","start_time":"1000"}`, 1), baseStyle())
	out := rowText(rows)
	require.Contains(t, out, "项目协作群 - 张三的视频会议")
	require.NotContains(t, out, "Meeting ID")
	_, ok := zoneRow(rows)
	require.False(t, ok, "nothing to dial")
}

func TestBodyRows_ALongTopicWrapsInsideTheCard(t *testing.T) {
	topic := strings.Repeat("长", 80)
	rows := renderRows(callMessage(`{"topic":"`+topic+`","meet_number":"100000000","start_time":"1000"}`, 1), baseStyle())
	out := rowText(rows)
	require.Contains(t, out, strings.Repeat("长", 10), "the topic is kept, not cut to one line")
	for _, r := range rows {
		require.LessOrEqual(t, lipgloss.Width(r.text), baseStyle().width, "a card row never outgrows the pane")
	}
}

func TestSpacedMeetNumber_GroupsInThrees(t *testing.T) {
	require.Equal(t, "100 000 000", spacedMeetNumber("100000000"))
	require.Equal(t, "1 000", spacedMeetNumber("1000"))
	require.Equal(t, "10", spacedMeetNumber("10"))
}

func TestFeishuMeetingLink_ReachesTheClientWithoutABrowser(t *testing.T) {
	require.Equal(t, "lark://vc.feishu.cn/j/100000000", applink.MeetingLink("100000000"))
}

// callPage is a chat holding one call, with the opener replaced so the links
// a click or a keypress fires are recorded instead of reaching macOS.
func callPage(t *testing.T, contentRaw string) (Model, *[]openCall) {
	t.Helper()
	var calls []openCall
	m := New(Deps{Self: "ou_me", OpenURL: func(targets []string, background bool) error {
		calls = append(calls, openCall{targets, background})
		return nil
	}})
	m.width, m.height = 120, 36
	m.chatID = "oc_a"
	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_a", SenderID: "ou_x", SenderName: "张三",
		MsgType: "video_chat", ContentRaw: contentRaw, Content: "[Video call]",
		CreateMs: msgAt(23, 9, 0), MessagePosition: 227, RenderedAt: 1}}
	m.applyOutbox()
	m.layout()
	m.focus, m.msgIdx = paneMessages, 0
	m.rebuildMessages()
	return m, &calls
}

// clickAt presses the left button at a column of a row of the messages pane,
// in the same coordinates the terminal reports.
func clickAt(m Model, row, x int) tea.Cmd {
	_, cmd := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: chatsWidth + 1 + x, Y: row + 1 + msgHeaderHeight})
	return cmd
}

func joinRowIndex(t *testing.T, m Model) int {
	t.Helper()
	for i, r := range m.msgRows {
		if len(r.zones) > 0 {
			return i
		}
	}
	t.Fatal("no join button on the page")
	return -1
}

func TestOnClick_TheJoinButtonEntersTheMeeting(t *testing.T) {
	m, calls := callPage(t, liveCall)
	i := joinRowIndex(t, m)
	collect(clickAt(m, i, firstZone(m.msgRows[i]).x0))
	require.Equal(t, []openCall{opened("lark://vc.feishu.cn/j/100000000", false)}, *calls,
		"joining takes the screen, unlike the applink that clears a badge")
}

func TestOnClick_OnlyTheButtonItselfJoins(t *testing.T) {
	m, calls := callPage(t, liveCall)
	i := joinRowIndex(t, m)
	z := firstZone(m.msgRows[i])
	collect(clickAt(m, i, z.x0-1))
	collect(clickAt(m, i, z.x1))
	collect(clickAt(m, i-1, z.x0))
	require.Empty(t, *calls, "a click beside the button selects the message like any other")
}

func TestOnNormalKey_OOpensALiveCallByJoiningIt(t *testing.T) {
	m, calls := callPage(t, liveCall)
	collect(mustCmd(m.onNormalKey("o")))
	require.Equal(t, []openCall{opened("lark://vc.feishu.cn/j/100000000", false)}, *calls)
}

func TestOnNormalKey_OOnAnEndedCallOpensTheMessage(t *testing.T) {
	m, calls := callPage(t, endedCall)
	collect(mustCmd(m.onNormalKey("o")))
	require.Equal(t, []openCall{opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_a&position=227", false)}, *calls,
		"once the call is over there is nothing to join")
}

func mustCmd(_ tea.Model, cmd tea.Cmd) tea.Cmd { return cmd }
