package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// drain runs a command tree and feeds every message it yields back through
// Update, until nothing is left. It is what walks the clear queue to its end:
// the chain advances on a tick, so a test that only ran the first command
// would see the write and none of the clears behind it.
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

// badgeModel is readModel with the clear lever replaced, so the chats a visit
// would clear are recorded instead of reaching the gateway.
func badgeModel(t *testing.T) (Model, *store.Store, *[]store.ChatUnread) {
	t.Helper()
	st, err := storetest.OpenSeed(t, filepath.Join(t.TempDir(), "t.db"), "tui.unreadone", seedUnreadOne)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	var cleared []store.ChatUnread
	m := New(Deps{Store: st, Self: "ou_me", Client: larkcli.NewFake(),
		ClearBadge: func(_ context.Context, c store.ChatUnread) error {
			cleared = append(cleared, c)
			return nil
		}})
	m.width, m.height = 120, 36
	return m, st, &cleared
}

// unreadPage is a page carrying one main-flow message Feishu still reports as
// unseen — what the chat badge counts.
func unreadPage() []store.Message {
	unread := false
	return []store.Message{{MessageID: "om_a", ChatID: "oc_a", IsReadRemote: &unread}}
}

func TestUpdate_OpeningAChatWithUnreadClearsTheFeishuBadge(t *testing.T) {
	t.Parallel()
	m, st, cleared := badgeModel(t)
	m.pendingChat = "oc_a"

	arrive(t, m, st, "oc_a")

	require.Equal(t, []store.ChatUnread{{ChatID: "oc_a"}}, *cleared)
}

func TestTakeRead_ClearsNothingWhenNothingWasWaiting(t *testing.T) {
	t.Parallel()
	m, _, cleared := badgeModel(t)
	read := true
	next, cmd := m.takeRead("oc_a", []store.Message{{MessageID: "om_a", IsReadRemote: &read}})
	drain(t, next, cmd)

	require.Empty(t, *cleared, "a chat with no badge here has none in Feishu either")
}

func TestTakeRead_ClearsNothingForAPageAlreadyReadHere(t *testing.T) {
	t.Parallel()
	m, _, cleared := badgeModel(t)
	unread := false
	page := []store.Message{{MessageID: "om_a", IsReadRemote: &unread, LocalReadAt: 900}}
	next, cmd := m.takeRead("oc_a", page)
	drain(t, next, cmd)

	require.Empty(t, *cleared, "the reload a visit causes must not fire a second clear")
}

func TestTakeRead_StaysOffAChatTheSweepWouldStillWalk(t *testing.T) {
	t.Parallel()
	m, st, cleared := badgeModel(t)
	require.NoError(t, st.MarkChatRead(t.Context(), "oc_a", 900))

	next, cmd := m.takeRead("oc_a", []store.Message{{MessageID: "om_a", IsReadRemote: new(false), LocalReadAt: 900}})
	drain(t, next, cmd)

	require.Empty(t, *cleared)
	require.Len(t, waiting(t, st), 1,
		"the two gates disagree on purpose: the sweep is bounded by the receipt and may clear this chat, "+
			"the read gate is bounded by markChatRead's write so that a reload cannot fire per visit")
}

func TestTakeRead_ClearsAgainForAMessageLandingInTheOpenChat(t *testing.T) {
	t.Parallel()
	m, _, cleared := badgeModel(t)
	next, cmd := m.takeRead("oc_a", unreadPage())
	m = drain(t, next, cmd)
	next, cmd = m.takeRead("oc_a", unreadPage())
	drain(t, next, cmd)

	require.Len(t, *cleared, 2, "the client relights its dot per message, so clearing is not a one-shot")
}

func TestTakeRead_IgnoresUnreadTheChatBadgeLeavesOut(t *testing.T) {
	t.Parallel()
	m, _, cleared := badgeModel(t)
	unread := false
	next, cmd := m.takeRead("oc_a", []store.Message{
		{MessageID: "om_thread", IsReadRemote: &unread, MessagePosition: -3},
		{MessageID: "om_gone", IsReadRemote: &unread, Deleted: true},
	})
	drain(t, next, cmd)

	require.Empty(t, *cleared, "markChatRead never settles these, so firing for them would never stop")
}

func TestOpenInFeishu_HandsTheChatLinkToTheDesktop(t *testing.T) {
	t.Parallel()
	var calls [][]string
	m := New(Deps{Log: discardLog, OpenURL: func(targets []string) error {
		calls = append(calls, targets)
		return nil
	}})
	collect(openInFeishu(m.deps, "oc_a", "", 227))

	require.Equal(t, [][]string{{"lark://applink.feishu.cn/client/chat/open?openChatId=oc_a&position=227"}}, calls)
}

func TestUpdate_AMessageLandingInTheOpenChatClearsTheBadgeAgain(t *testing.T) {
	t.Parallel()
	m, st, cleared := badgeModel(t)
	m.pendingChat = "oc_a"
	m = arrive(t, m, st, "oc_a")
	require.Len(t, *cleared, 1)

	ctx := t.Context()
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: "om_b", ChatID: "oc_a", MsgType: "text",
		SenderID: "ou_x", SenderName: "孙琪", ContentRaw: `{"text":"还在吗"}`, CreateMs: 200, UpdateMs: 200}}, 2)
	require.NoError(t, err)
	unread := false
	require.NoError(t, st.SetReadStatus(ctx, "om_b", &unread, 200, 0))

	arrive(t, m, st, "oc_a")

	require.Len(t, *cleared, 2, "the client lit its dot again, so it has to be cleared again")
}

func TestUpdate_EscapeLeavesTheReadGatesOwnClearQueued(t *testing.T) {
	t.Parallel()
	m, _, cleared := badgeModel(t)
	next, cmd := m.takeRead("oc_a", unreadPage())

	stopped, _ := next.onNormalKey("esc")
	m = drain(t, stopped.(Model), cmd)

	require.Equal(t, []store.ChatUnread{{ChatID: "oc_a"}}, *cleared,
		"esc backs out of a mark-all, not of the reader's own navigation")
}
