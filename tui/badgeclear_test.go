package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// openCall is one hand-over to the desktop: the targets opened together, and
// whether the screen was left to the terminal.
type openCall struct {
	targets    []string
	background bool
}

// opened is a hand-over of one target, which is every call but a message's
// pictures going over as a set.
func opened(url string, background bool) openCall {
	return openCall{[]string{url}, background}
}

// drain runs a command tree and feeds every message it yields back through
// Update, until nothing is left. It is what walks the applink queue to its
// end: the chain advances on a tick, so a test that only ran the first
// command would see the write and none of the applinks behind it.
//
// Each step costs one applink.Pace of real time, the way the chat poll's
// tests pay for their own tick. The package has no clock to fake and the
// alternative is a seam that exists for nothing but this.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; i < len(queue); i++ {
		require.Less(t, i, 500, "the command tree never settled")
		if queue[i] == nil {
			continue
		}
		msg := queue[i]()
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		if msg == nil {
			continue
		}
		next, out := m.Update(msg)
		m = next.(Model)
		queue = append(queue, out)
	}
	return m
}

// badgeModel is readModel with the opener replaced, so the applinks a visit
// would fire are recorded instead of reaching macOS.
func badgeModel(t *testing.T) (Model, *store.Store, *[]openCall) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_a", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_a", ChatID: "oc_a", MsgType: "text",
		SenderID: "ou_x", SenderName: "孙琪", ContentRaw: `{"text":"在吗"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_a", &unread, 100, 0))

	var calls []openCall
	m := New(Deps{Store: st, Self: "ou_me", Client: larkcli.NewFake(), OpenURL: func(targets []string, background bool) error {
		calls = append(calls, openCall{targets, background})
		return nil
	}})
	m.width, m.height = 120, 36
	return m, st, &calls
}

// unreadPage is a page carrying one main-flow message Feishu still reports as
// unseen — what the chat badge counts.
func unreadPage() []store.Message {
	unread := false
	return []store.Message{{MessageID: "om_a", ChatID: "oc_a", IsReadRemote: &unread}}
}

func TestUpdate_OpeningAChatWithUnreadClearsTheFeishuBadge(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"

	arrive(t, m, st, "oc_a")

	require.Equal(t, []openCall{opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_a", true)}, *calls,
		"the client is walked onto the chat without taking the screen")
}

func TestTakeRead_SendsNoApplinkWhenNothingWasWaiting(t *testing.T) {
	m, _, calls := badgeModel(t)
	read := true
	next, cmd := m.takeRead("oc_a", []store.Message{{MessageID: "om_a", IsReadRemote: &read}})
	drain(t, next, cmd)

	require.Empty(t, *calls, "a chat with no badge here has none in Feishu either")
}

func TestTakeRead_SendsNoApplinkForAPageAlreadyReadHere(t *testing.T) {
	m, _, calls := badgeModel(t)
	unread := false
	page := []store.Message{{MessageID: "om_a", IsReadRemote: &unread, LocalReadAt: 900}}
	next, cmd := m.takeRead("oc_a", page)
	drain(t, next, cmd)

	require.Empty(t, *calls, "the reload a visit causes must not fire a second applink")
}

func TestTakeRead_ClearsAgainForAMessageLandingInTheOpenChat(t *testing.T) {
	m, _, calls := badgeModel(t)
	next, cmd := m.takeRead("oc_a", unreadPage())
	m = drain(t, next, cmd)
	next, cmd = m.takeRead("oc_a", unreadPage())
	drain(t, next, cmd)

	require.Len(t, *calls, 2, "the client relights its dot per message, so clearing is not a one-shot")
}

func TestTakeRead_IgnoresUnreadTheChatBadgeLeavesOut(t *testing.T) {
	m, _, calls := badgeModel(t)
	unread := false
	next, cmd := m.takeRead("oc_a", []store.Message{
		{MessageID: "om_thread", IsReadRemote: &unread, MessagePosition: -3},
		{MessageID: "om_gone", IsReadRemote: &unread, Deleted: true},
	})
	drain(t, next, cmd)

	require.Empty(t, *calls, "markChatRead never settles these, so firing for them would never stop")
}

func TestOpenInFeishu_TakesTheScreen(t *testing.T) {
	m, _, calls := badgeModel(t)
	collect(openInFeishu(m.deps, "oc_a", 227))

	require.Equal(t, []openCall{opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_a&position=227", false)}, *calls)
}

func TestUpdate_AMessageLandingInTheOpenChatClearsTheBadgeAgain(t *testing.T) {
	m, st, calls := badgeModel(t)
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")
	require.Len(t, *calls, 1)

	// A second message lands while the reader sits in the chat, and the read
	// poller reports it unseen — the write that reloads the pane.
	ctx := context.Background()
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: "om_b", ChatID: "oc_a", MsgType: "text",
		SenderID: "ou_x", SenderName: "孙琪", ContentRaw: `{"text":"还在吗"}`, CreateMs: 200, UpdateMs: 200}}, 2)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_b", &unread, 200, 0))

	arrive(t, m, st, "oc_a")

	require.Len(t, *calls, 2, "the client lit its dot again, so it has to be cleared again")
}

func TestUpdate_EscapeLeavesTheReadGatesOwnApplinkQueued(t *testing.T) {
	m, _, calls := badgeModel(t)
	next, cmd := m.takeRead("oc_a", unreadPage())

	// markChatRead has already landed, so the chat is in neither
	// ChatsWithUnread nor ReadStatusProbes: an applink dropped here is a dot
	// nothing ever goes back for.
	stopped, _ := next.onNormalKey("esc")
	m = drain(t, stopped.(Model), cmd)

	require.Equal(t, []openCall{opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_a", true)}, *calls,
		"esc backs out of a mark-all, not of the reader's own navigation")
}
