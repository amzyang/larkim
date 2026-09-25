package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fwdModel is a model holding one message, two chats and a colleague larkim
// has no chat with.
func fwdModel(t *testing.T) (Model, *larkcli.Fake) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_group", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_a", ChatID: "oc_group",
		MsgType: "text", SenderID: "ou_a", SenderName: "张三",
		ContentRaw: `{"text":"发布计划"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)

	f := larkcli.NewFake()
	f.Messages["om_a"] = larkcli.RawMessage{MessageID: "om_a", ChatID: "oc_group",
		Body: larkcli.RawBody{Content: `{"text":"发布计划"}`}}
	m := New(Deps{Store: st, Self: "ou_me", Client: f})
	m.width, m.height = 120, 36
	m.chatID = "oc_group"
	m.focus = paneMessages
	m.chats = []store.Chat{
		{ChatID: "oc_group", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a"},
	}
	m.contacts = []store.Contact{
		{OpenID: "ou_a", Name: "张三"},
		{OpenID: "ou_b", Name: "李四"},
		{OpenID: "ou_me", Name: "林岚"},
	}
	msgs, err := st.ListMessages(ctx, store.MessageQuery{ChatID: "oc_group"})
	require.NoError(t, err)
	m.msgs, m.msgsBase = msgs, msgs
	return m, f
}

func fwdNames(hits []fwdTarget) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

// Chats first, then the people no chat reaches yet.
func TestFwdSearch_OffersChatsThenPeople(t *testing.T) {
	m, _ := fwdModel(t)

	assert.Equal(t, []string{"平台组", "张三", "李四"}, fwdNames(m.fwdSearch("")))
}

// Somebody already reachable as a chat is not offered twice.
func TestFwdSearch_DoesNotOfferAPersonTwice(t *testing.T) {
	m, _ := fwdModel(t)

	got := fwdNames(m.fwdSearch("张三"))

	assert.Equal(t, []string{"张三"}, got)
}

func TestFwdSearch_LeavesTheReaderOut(t *testing.T) {
	m, _ := fwdModel(t)

	assert.NotContains(t, fwdNames(m.fwdSearch("")), "林岚")
}

func TestFwdSearch_ReachesAChineseNameThroughPinyin(t *testing.T) {
	m, _ := fwdModel(t)

	assert.Equal(t, []string{"李四"}, fwdNames(m.fwdSearch("lisi")))
}

func TestOpenForward_RefusesARecalledMessage(t *testing.T) {
	m, _ := fwdModel(t)
	m.msgs[0].Deleted = true

	next, _ := m.openForward()
	m = next.(Model)

	assert.NotEqual(t, modeForward, m.mode)
	assert.Contains(t, m.notice, "recalled")
}

func TestOpenForward_RefusesASendStillInFlight(t *testing.T) {
	m, _ := fwdModel(t)
	m.outbox = []outboxItem{{localID: "om_a", chatID: "oc_group"}}

	next, _ := m.openForward()
	m = next.(Model)

	assert.NotEqual(t, modeForward, m.mode)
	assert.Contains(t, m.notice, "has not reached Feishu")
}

func TestChooseForward_SendsToTheChosenChat(t *testing.T) {
	m, f := fwdModel(t)
	next, _ := m.openForward()
	m = next.(Model)
	m.fwd.hits = m.fwdSearch("平台组")

	next, cmd := m.chooseForward()
	m = next.(Model)
	require.NotNil(t, cmd)
	cmd()

	assert.Equal(t, []string{"om_a->oc_group"}, f.Forwarded)
	assert.Equal(t, modeNormal, m.mode, "the composer comes back")
}

// A colleague with no chat yet is still somewhere a message can go.
func TestChooseForward_SendsToAPersonWithNoChatYet(t *testing.T) {
	m, f := fwdModel(t)
	next, _ := m.openForward()
	m = next.(Model)
	m.fwd.hits = m.fwdSearch("李四")

	_, cmd := m.chooseForward()
	cmd()

	assert.Equal(t, []string{"om_a->ou_b"}, f.Forwarded)
}

func TestOnForwardKey_EscSendsNothing(t *testing.T) {
	m, f := fwdModel(t)
	next, _ := m.openForward()
	m = next.(Model)

	next, _ = m.onForwardKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	assert.Equal(t, modeNormal, m.mode)
	assert.Empty(t, f.Forwarded)
}

func TestForward_ReportsFeishusRefusal(t *testing.T) {
	m, _ := fwdModel(t)

	msg := forwardCmd(m.deps, "om_gone", larkcli.Target{ChatID: "oc_group"})().(forwardedMsg)
	next, _ := m.update(msg)

	require.Error(t, msg.err)
	assert.Contains(t, next.(Model).notice, "forward:")
}

// The window budgets fwdRows destinations, so the box has to draw that many:
// one row short and the cursor vanishes off the bottom, and enter would then
// send to a destination the reader never saw selected.
func TestRenderForward_KeepsTheCursorVisibleOnEveryDestination(t *testing.T) {
	m, _ := fwdModel(t)
	next, _ := m.openForward()
	m = next.(Model)
	require.GreaterOrEqual(t, len(m.fwd.hits), m.fwdRows(), "the window has to be full")

	for i := range len(m.fwd.hits) {
		assert.Contains(t, m.renderForward(), "▸", "no cursor at destination %d", i)
		m.fwd.move(1, m.fwdRows())
	}
}
