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

// recallModel is a chat holding one message the reader sent and one somebody
// else did, with the cursor on the reader's own.
func recallModel(t *testing.T) (Model, *larkcli.Fake) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_group", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_mine", ChatID: "oc_group", MsgType: "text", SenderID: "ou_me",
			SenderName: "林岚", ContentRaw: `{"text":"发错了"}`, CreateMs: 100, UpdateMs: 100},
		{MessageID: "om_theirs", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a",
			SenderName: "张三", ContentRaw: `{"text":"收到"}`, CreateMs: 200, UpdateMs: 200},
	}, 1)
	require.NoError(t, err)

	f := larkcli.NewFake()
	f.Messages["om_mine"] = larkcli.RawMessage{MessageID: "om_mine", ChatID: "oc_group"}
	m := New(Deps{Store: st, Self: "ou_me", Client: f})
	m.width, m.height = 120, 36
	m.chatID = "oc_group"
	m.focus = paneMessages
	msgs, err := st.ListMessages(ctx, store.MessageQuery{ChatID: "oc_group"})
	require.NoError(t, err)
	m.msgs, m.msgsBase = msgs, msgs
	m.msgIdx = 0
	return m, f
}

// A recall is visible to everybody who was in the chat and cannot be undone,
// so it asks first — the client asks too.
func TestAskRecall_AsksBeforeTakingAnythingBack(t *testing.T) {
	m, f := recallModel(t)

	next, cmd := m.askRecall()
	m = next.(Model)

	assert.Equal(t, confirmRecall, m.confirm.kind)
	assert.Equal(t, "om_mine", m.confirm.messageID)
	assert.Contains(t, m.notice, "recall this message?")
	assert.Nil(t, cmd, "nothing goes out until it is answered")
	assert.Empty(t, f.Recalled)
}

func TestAnswerConfirm_YesRecalls(t *testing.T) {
	m, f := recallModel(t)
	next, _ := m.askRecall()
	m = next.(Model)

	_, cmd, answered := m.answerConfirm("y")
	require.True(t, answered)
	require.NotNil(t, cmd)
	cmd()

	assert.Equal(t, []string{"om_mine"}, f.Recalled)
}

// Anything but y cancels, not only n: this is the answer where a slip costs
// something everybody in the chat can see.
func TestAnswerConfirm_AnythingButYesCancels(t *testing.T) {
	for _, key := range []string{"n", "esc", "j", "q"} {
		m, f := recallModel(t)
		next, _ := m.askRecall()
		m = next.(Model)

		next2, cmd, answered := m.answerConfirm(key)
		require.True(t, answered, key)

		assert.Nil(t, cmd, key)
		assert.Empty(t, f.Recalled, key)
		assert.Equal(t, confirmNone, next2.(Model).confirm.kind, key)
	}
}

// With nothing pending the key belongs to its ordinary binding.
func TestAnswerConfirm_NothingPendingLeavesTheKeyAlone(t *testing.T) {
	m, _ := recallModel(t)

	_, _, answered := m.answerConfirm("j")

	assert.False(t, answered)
}

func TestAskRecall_RefusesSomebodyElsesMessage(t *testing.T) {
	m, f := recallModel(t)
	m.msgIdx = 1 // 张三's

	next, _ := m.askRecall()
	m = next.(Model)

	assert.Equal(t, confirmNone, m.confirm.kind)
	assert.Contains(t, m.notice, "only your own")
	assert.Empty(t, f.Recalled)
}

func TestAskRecall_RefusesAnAlreadyRecalledMessage(t *testing.T) {
	m, _ := recallModel(t)
	m.msgs[0].Deleted = true

	next, _ := m.askRecall()
	m = next.(Model)

	assert.Equal(t, confirmNone, m.confirm.kind)
	assert.Contains(t, m.notice, "already recalled")
}

// A send still on its way carries a local id Feishu has never seen.
func TestAskRecall_RefusesASendStillInFlight(t *testing.T) {
	m, _ := recallModel(t)
	m.outbox = []outboxItem{{localID: "om_mine", chatID: "oc_group"}}

	next, _ := m.askRecall()
	m = next.(Model)

	assert.Equal(t, confirmNone, m.confirm.kind)
	assert.Contains(t, m.notice, "has not reached Feishu")
}

// Feishu decides whether the window has closed, and its refusal is what the
// reader is told.
func TestRecall_ReportsFeishusRefusal(t *testing.T) {
	m, _ := recallModel(t)

	// Whether the window has closed is Feishu's to answer; whatever it says
	// comes back to the reader rather than being guessed at locally.
	msg := recallCmd(m.deps, "om_gone")().(recalledMsg)
	next, _ := m.update(msg)

	require.Error(t, msg.err)
	assert.Contains(t, next.(Model).notice, "recall:")
}

// A pending confirmation owns the next key, so one press cannot both answer it
// and do something else.
func TestOnKey_PendingConfirmationSwallowsTheNextKey(t *testing.T) {
	m, _ := recallModel(t)
	next, _ := m.askRecall()
	m = next.(Model)
	before := m.msgIdx

	next2, _ := m.onKey(tea.KeyPressMsg{Code: 'j', Text: "j"})

	assert.Equal(t, before, next2.(Model).msgIdx, "j answered the question, it did not move the cursor")
}
