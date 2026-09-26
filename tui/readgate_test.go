package tui

import (
	"context"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// scrolledBack is a chat whose page does not fit the pane, opened, read, and
// then wheeled back into its history. The applink the visit fired is dropped,
// so what a test records is what happened after the reader scrolled away.
func scrolledBack(t *testing.T) (Model, *store.Store, *[]openCall) {
	t.Helper()
	m, st, calls := badgeModel(t)
	m.height = 16
	for i := range 20 {
		lands(t, st, fmt.Sprintf("om_%d", i), "ou_x", "孙琪", "排期确认一下", int64(200+i))
	}
	m.focus, m.pendingChat = paneMessages, "oc_a"
	m = arrive(t, m, st, "oc_a")
	require.Greater(t, len(m.msgRows), m.msgListHeight(), "the page has to overflow the pane for the fold to exist")
	require.Zero(t, unreadOf(t, st, "oc_a"), "the visit read what it opened on")
	*calls = nil
	return wheel(t, m, tea.MouseWheelUp, 20), st, calls
}

// wheel scrolls the message pane through Update, which is where a viewport
// reaching the tail is taken as read.
func wheel(t *testing.T, m Model, b tea.MouseButton, n int) Model {
	t.Helper()
	for range n {
		next, cmd := m.Update(tea.MouseWheelMsg{X: chatsWidth + 5, Y: 5, Button: b})
		collect(cmd)
		m = next.(Model)
	}
	return m
}

// unreadOf is the badge the chat list would draw for one chat.
func unreadOf(t *testing.T, st *store.Store, chatID string) int64 {
	t.Helper()
	chats, err := st.ListChats(context.Background(), store.ChatQuery{})
	require.NoError(t, err)
	for _, c := range chats {
		if c.ChatID == chatID {
			return c.UnreadCount
		}
	}
	t.Fatalf("chat %s is not in the list", chatID)
	return 0
}

func TestUpdate_AMessageLandingBelowTheFoldLeavesTheChatUnread(t *testing.T) {
	m, st, calls := scrolledBack(t)
	lands(t, st, "om_new", "ou_b", "李四", "刚发现一个问题", 900)

	m = arrive(t, m, st, "oc_a")

	require.EqualValues(t, 1, unreadOf(t, st, "oc_a"), "what landed out of sight is still waiting")
	require.Empty(t, *calls, "the Feishu dot stands until the reader reaches the message")
	require.True(t, m.dots["om_new"], "and it wears a marker for when they do")
}

func TestUpdate_ScrollingBackToTheTailTakesWhatWasWaiting(t *testing.T) {
	m, st, calls := scrolledBack(t)
	lands(t, st, "om_new", "ou_b", "李四", "刚发现一个问题", 900)
	m = arrive(t, m, st, "oc_a")

	wheel(t, m, tea.MouseWheelDown, 20)

	require.Zero(t, unreadOf(t, st, "oc_a"), "reaching the message reads it")
	require.Equal(t, []openCall{opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_a", true)}, *calls,
		"and settles the client's dot in the same breath")
}

func TestUpdate_ScrollingWithinTheHistoryTakesNothing(t *testing.T) {
	m, st, calls := scrolledBack(t)
	lands(t, st, "om_new", "ou_b", "李四", "刚发现一个问题", 900)
	m = arrive(t, m, st, "oc_a")

	// Down a notch, then back up: the tail is never reached.
	m = wheel(t, m, tea.MouseWheelDown, 1)
	wheel(t, m, tea.MouseWheelUp, 1)

	require.EqualValues(t, 1, unreadOf(t, st, "oc_a"), "moving inside the history is not arriving at the message")
	require.Empty(t, *calls)
}

func TestUpdate_LeavingTheTailAndComingBackFiresOneApplink(t *testing.T) {
	m, st, calls := scrolledBack(t)
	lands(t, st, "om_new", "ou_b", "李四", "刚发现一个问题", 900)
	m = arrive(t, m, st, "oc_a")

	m = wheel(t, m, tea.MouseWheelDown, 20)
	m = wheel(t, m, tea.MouseWheelUp, 5)
	wheel(t, m, tea.MouseWheelDown, 20)

	require.Len(t, *calls, 1, "the reader reached one message, so the client is walked onto the chat once")
}

func TestUpdate_TheReadFlagLandingLateStillTakesTheChatRead(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")
	*calls = nil

	// Sync brings the message in; the read poller writes its flag on a pass of
	// its own, so the page that lights the badge carries no new message.
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: "om_late", ChatID: "oc_a", MsgType: "text",
		SenderID: "ou_b", SenderName: "李四", ContentRaw: `{"text":"排期确认一下"}`, CreateMs: 400, UpdateMs: 400}}, 400)
	require.NoError(t, err)
	m = arrive(t, m, st, "oc_a")
	require.Empty(t, *calls, "a message with no read flag yet has nothing to settle")

	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_late", &unread, 400, 0))
	arrive(t, m, st, "oc_a")

	require.Zero(t, unreadOf(t, st, "oc_a"))
	require.Len(t, *calls, 1, "the flag arriving is what makes the page worth settling")
}

func TestUpdate_ABlurredTerminalLeavesTheChatUnread(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"
	m = watching(t, m, st)
	*calls = nil

	away, _ := m.Update(tea.BlurMsg{})
	m = away.(Model)
	lands(t, st, "om_b", "ou_b", "李四", "改到下午", 300)
	m = arrive(t, m, st, "oc_a")

	require.EqualValues(t, 1, unreadOf(t, st, "oc_a"), "the reader is at another window")
	require.Empty(t, *calls)

	back, cmd := m.Update(tea.FocusMsg{})
	collect(cmd)
	m = back.(Model)

	require.Zero(t, unreadOf(t, st, "oc_a"), "coming back to the window reads what is on screen")
	require.Len(t, *calls, 1)
}

func TestUpdate_TheHelpOverlayLeavesTheChatUnread(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"
	m = watching(t, m, st)
	*calls = nil
	m.help.open = true

	lands(t, st, "om_b", "ou_b", "李四", "改到下午", 300)
	m = arrive(t, m, st, "oc_a")
	require.EqualValues(t, 1, unreadOf(t, st, "oc_a"), "the overlay covers the panes whole")

	m.help.open = false
	_, cmd := m.Update(tea.FocusMsg{})
	collect(cmd)

	require.Zero(t, unreadOf(t, st, "oc_a"), "closing it puts the page back in front of the reader")
	require.Len(t, *calls, 1)
}

func TestReadKey_IsEmptyWhileTheSearchPanelIsOpen(t *testing.T) {
	m, st, _ := badgeModel(t)
	m.pendingChat = "oc_a"
	lands(t, st, "om_b", "ou_b", "李四", "改到下午", 300)
	m = watching(t, m, st)
	m.msgsBase[0].LocalReadAt = 0
	require.NotEmpty(t, m.readKey(true), "the chat's own page is what the pane draws")

	// :mentions borrows the search panel whole, so both set this one flag.
	m.searching = true

	require.Empty(t, m.readKey(true), "hits run across chats, so msgTop says nothing about this one")
}

func TestUpdate_AReadFlagOnAnOlderMessageStillTakesTheChatRead(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")
	*calls = nil

	// Two messages arrive together and the read poller reaches them out of
	// order, so the second flag lands behind a message already settled.
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_older", ChatID: "oc_a", MsgType: "text", SenderID: "ou_b", SenderName: "李四",
			ContentRaw: `{"text":"排期确认一下"}`, CreateMs: 300, UpdateMs: 300},
		{MessageID: "om_newer", ChatID: "oc_a", MsgType: "text", SenderID: "ou_b", SenderName: "李四",
			ContentRaw: `{"text":"顺便问下进度"}`, CreateMs: 400, UpdateMs: 400},
	}, 400)
	require.NoError(t, err)

	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_newer", &unread, 400, 0))
	m = arrive(t, m, st, "oc_a")
	require.Len(t, *calls, 1, "the newest message is settled on its own flag")

	require.NoError(t, st.SetReadStatus(ctx, "om_older", &unread, 300, 0))
	arrive(t, m, st, "oc_a")

	require.Zero(t, unreadOf(t, st, "oc_a"), "the older message must not be stranded behind the newer one")
	require.Len(t, *calls, 2)
}

func TestUpdate_AFoldedAwayMessagePaneLeavesTheChatUnread(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"
	m = watching(t, m, st)
	*calls = nil

	// Under three columns the thread pane takes the messages pane's place, so
	// the chat's own page is not drawn at all.
	m.width, m.rightKind, m.threadID = chatsWidth+minMessagesWidth+threadWidth-1, rightThread, "omt_1"
	m.layout()
	require.True(t, m.foldRight(), "the fixture has to be narrow enough to fold")

	lands(t, st, "om_b", "ou_b", "李四", "改到下午", 300)
	m = arrive(t, m, st, "oc_a")
	require.EqualValues(t, 1, unreadOf(t, st, "oc_a"), "a pane that is not drawn shows nobody anything")
	require.Empty(t, *calls)

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 160, Height: m.height})
	collect(cmd)

	require.Zero(t, unreadOf(t, st, "oc_a"), "widening the terminal unfolds the pane onto the message")
	require.Len(t, *calls, 1)
}

func TestUpdate_AJumpIntoHistoryLeavesWhatIsBelowItUnread(t *testing.T) {
	m, st, calls := scrolledBack(t)
	lands(t, st, "om_new", "ou_b", "李四", "刚发现一个问题", 900)
	m = arrive(t, m, st, "oc_a")
	*calls = nil

	// A search hit inside the open chat: the page comes back with the cursor
	// asked for an old message rather than for the newest.
	m.pendingSelect = pendingJump{id: "om_0"}
	m = arrive(t, m, st, "oc_a")

	require.EqualValues(t, 1, unreadOf(t, st, "oc_a"), "landing on an old hit is not reading what is under it")
	require.Empty(t, *calls)

	wheel(t, m, tea.MouseWheelDown, 30)

	require.Zero(t, unreadOf(t, st, "oc_a"), "scrolling down to it is")
}
