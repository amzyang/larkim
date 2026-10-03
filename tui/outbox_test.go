package tui

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

func newOutboxModel(t *testing.T) (Model, *larkcli.Fake) {
	t.Helper()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	m := New(Deps{Store: st, Client: f, Self: "ou_me"})
	m.width, m.height = 120, 40
	m.chatID, m.selfName, m.focus = "oc_1", "林岚", paneMessages
	m.layout()
	return m, f
}

func contents(msgs []store.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, x := range msgs {
		out = append(out, x.Content)
	}
	return out
}

func TestSubmit_ShowsTheMessageBeforeFeishuAnswers(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.input.SetValue("hello")

	mm, cmd := m.submit()
	m = mm.(Model)

	require.NotNil(t, cmd)
	require.Len(t, m.msgs, 1)
	require.Equal(t, "hello", m.msgs[0].Content)
	require.Equal(t, "ou_me", m.msgs[0].SenderID)
	require.Equal(t, "林岚", m.msgs[0].SenderName)
	require.NotZero(t, m.msgs[0].RenderedAt, "the body is already text, not something awaiting rendering")
	require.Empty(t, m.input.Value(), "the draft is gone because the bubble now carries it")
	require.Len(t, m.outbox, 1)
	require.Equal(t, outSending, m.outbox[0].state)
}

func TestSubmit_AcceptsASecondMessageWhileTheFirstIsInFlight(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.input.SetValue("one")
	mm, _ := m.submit()
	m = mm.(Model)
	m.input.SetValue("two")
	mm, _ = m.submit()
	m = mm.(Model)

	require.Len(t, m.outbox, 2)
	require.Equal(t, []string{"one", "two"}, contents(m.msgs))
}

func TestApplyOutbox_DropsTheRowOnceTheRealMessageLands(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "hi", state: outSent, messageID: "om_1", createMs: 10})
	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_1", Content: "hi", RenderedAt: 1, CreateMs: 20}}

	m.applyOutbox()

	require.Len(t, m.msgs, 1, "the bubble and the message it became are one row")
	require.Equal(t, "om_1", m.msgs[0].MessageID)
}

func TestUpdate_KeepsTheBubbleWhenTheIngestFails(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "hi", createMs: 10})
	m.applyOutbox()

	mm, _ := m.Update(sentMsg{localID: "local-1", messageID: "om_1", ingestErr: errors.New("mget failed")})
	m = mm.(Model)

	require.Len(t, m.outbox, 1, "the message is on Feishu; only the local row is missing")
	require.Equal(t, outSent, m.outbox[0].state)
	require.Len(t, m.msgs, 1)
}

func TestUpdate_KeepsTheBubbleUntilTheReloadBringsItsRow(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "hi", createMs: 10})
	m.applyOutbox()

	mm, _ := m.Update(sentMsg{localID: "local-1", messageID: "om_1"})
	m = mm.(Model)

	require.Equal(t, []string{"hi"}, contents(m.msgs), "the page the reload brings has not landed yet")
	require.Len(t, m.outbox, 1)

	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_1", Content: "hi", RenderedAt: 2, CreateMs: 20}}
	m.applyOutbox()

	require.Empty(t, m.outbox)
	require.Equal(t, "om_1", m.msgs[0].MessageID)
}

func TestUpdate_RetiresABubbleNoPaneShowsOnceTheIngestWorked(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_2", msgType: "text", body: "hi", createMs: 10})
	m.applyOutbox()

	mm, _ := m.Update(sentMsg{localID: "local-1", messageID: "om_1"})
	m = mm.(Model)

	require.Empty(t, m.outbox)
	require.Empty(t, m.msgs)
}

func TestApplyOutbox_AnUnrenderedLandingDrawsTheBubblesBody(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "hi", state: outSent, messageID: "om_1", createMs: 10})
	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_1", MsgType: "text", ContentRaw: `{"text":"hi"}`, CreateMs: 20}}

	m.applyOutbox()

	require.Len(t, m.msgs, 1)
	require.Equal(t, "om_1", m.msgs[0].MessageID)
	require.Equal(t, "hi", m.msgs[0].Content)
	require.NotZero(t, m.msgs[0].RenderedAt, "not the dim stand-in an unrendered row draws")
	require.Zero(t, m.msgsBase[0].RenderedAt, "the store's rows are left as the store returned them")
	require.Len(t, m.outbox, 1, "the item lives on until the rendering lands")
}

func TestRetryFailed_ReusesTheIdempotencyKey(t *testing.T) {
	t.Parallel()
	m, f := newOutboxModel(t)
	f.Err = errors.New("network down")
	m.input.SetValue("hi")
	mm, cmd := m.submit()
	m = mm.(Model)

	sent := cmd().(sentMsg)
	require.Error(t, sent.err)
	mm, _ = m.Update(sent)
	m = mm.(Model)
	require.Equal(t, outFailed, m.outbox[0].state)

	f.Err = nil
	m.msgIdx = len(m.msgs) - 1
	mm, cmd = m.retryFailed()
	m = mm.(Model)
	require.Equal(t, outSending, m.outbox[0].state)
	cmd()

	require.Len(t, f.SentKeys, 2)
	require.Equal(t, f.SentKeys[0], f.SentKeys[1], "a retry is the same send, not a second delivery")
}

func TestDiscardFailed_DropsTheRowUnderTheCursor(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "hi", state: outFailed, createMs: 10})
	m.applyOutbox()
	m.msgIdx = len(m.msgs) - 1

	mm, _ := m.discardFailed()
	m = mm.(Model)

	require.Empty(t, m.outbox)
	require.Empty(t, m.msgs)
}

func TestDiscardFailed_LeavesAMessageStillOnItsWay(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "hi", state: outSending, createMs: 10})
	m.applyOutbox()
	m.msgIdx = len(m.msgs) - 1

	mm, _ := m.discardFailed()
	m = mm.(Model)

	require.Len(t, m.outbox, 1)
}

func TestApplyOutbox_QuotesAParentAlreadyOnThePage(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_1", Content: "question", RenderedAt: 1, CreateMs: 10}}
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", replyTo: "om_1", msgType: "text", body: "answer", createMs: 20})

	m.applyOutbox()

	require.Contains(t, m.meta.parents, "om_1", "the reply quotes a message loadMeta never saw it answer")
}

func TestApplyOutbox_PutsAThreadReplyInTheThreadPaneOnly(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.rightKind, m.threadID = rightThread, "omt_1"
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", threadID: "omt_1", replyTo: "om_1",
		inThread: true, msgType: "text", body: "in thread", createMs: 20})

	m.applyOutbox()

	// The chat's flow no longer carries replies, so a bubble put there would
	// show for a moment and vanish when the reload folds it away.
	require.Empty(t, contents(m.msgs))
	require.Equal(t, []string{"in thread"}, contents(m.thread))
	require.EqualValues(t, -1, m.thread[0].MessagePosition)
}

func TestSubmit_HandsTheBubbleOverToTheStoredMessage(t *testing.T) {
	t.Parallel()
	m, f := newOutboxModel(t)
	m.input.SetValue("hello")
	mm, cmd := m.submit()
	m = mm.(Model)

	sent := cmd().(sentMsg)
	require.NoError(t, sent.err)
	require.NoError(t, sent.ingestErr)
	require.Equal(t, "om_sent_1", sent.messageID)
	require.NotContains(t, f.Calls, "mget", "the send's answer is the row; nothing is fetched back")
	mm, cmd = m.Update(sent)
	m = mm.(Model)
	require.Equal(t, outSent, m.outbox[0].state)
	require.Equal(t, []string{"hello"}, contents(m.msgs), "on screen across the wait for the reload")
	mm, _ = m.Update(firstOf[messagesLoadedMsg](t, cmd))
	m = mm.(Model)
	require.Empty(t, m.outbox)
	require.Len(t, m.msgs, 1)
	require.Equal(t, "om_sent_1", m.msgs[0].MessageID)

	rows, err := m.deps.Store.ListMessages(t.Context(), store.MessageQuery{ChatID: "oc_1"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "om_sent_1", rows[0].MessageID)
	require.Len(t, f.SentKeys, 1)
}

func TestSubmit_TheBubbleDrawsTheEmojiThePostCarries(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.input.SetValue("**收到** [赞]")
	mm, _ := m.submit()
	m = mm.(Model)

	require.Len(t, m.msgs, 1)
	e, _ := emoji.ByKey("THUMBSUP")
	require.Contains(t, rowText(renderRows(m.msgs, baseStyle())), "收到 "+e.Glyph,
		"the bubble reads the body on the wire, where the name is an emotion")
}

func TestEnterChat_DropsTheRowsTheOldChatOwned(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_1", Content: "old chat"}}
	m.rightKind, m.threadID = rightThread, "om_1"
	m.threadBase = []store.Message{{MessageID: "om_2", ChatID: "oc_1", ThreadID: "om_1", Content: "old thread"}}
	m.refreshPanes()
	require.Len(t, m.msgs, 1)

	m.pendingChat = "oc_2"
	m.enterChat()
	m.refreshPanes()

	require.Empty(t, m.msgs, "the new chat shows nothing until its own rows land")
	require.Empty(t, m.thread)
}
