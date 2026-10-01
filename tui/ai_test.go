package tui

import (
	"cmp"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// fakeAI stands in for the ACP client: it hands out a channel the test owns,
// reports whether the request was cancelled, and records what it was asked.
type fakeAI struct {
	ch        chan ai.Chunk
	cancelled chan struct{}
	asks      []aiAsk
}

// aiAsk is one question the fake was asked, as Stream received it.
type aiAsk struct {
	transcript, prompt string
}

func newFakeAI() *fakeAI {
	return &fakeAI{ch: make(chan ai.Chunk, 4), cancelled: make(chan struct{})}
}

func (f *fakeAI) Stream(ctx context.Context, transcript, prompt string) <-chan ai.Chunk {
	f.asks = append(f.asks, aiAsk{transcript: transcript, prompt: prompt})
	go func() {
		<-ctx.Done()
		close(f.cancelled)
	}()
	return f.ch
}

// aiFixture is a model over a real store holding one chat with three
// messages, so a question's window is read from the store the way a live one
// is.
func aiFixture(t *testing.T, f *fakeAI) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	now := time.Now()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"}}, 1))
	msgs := []store.Message{
		{MessageID: "om_1", ChatID: "oc_quiet", CreateMs: now.Add(-3 * time.Hour).UnixMilli(), MessagePosition: 1,
			SenderID: "ou_a", SenderName: "张三", RawJSON: "{}"},
		{MessageID: "om_2", ChatID: "oc_quiet", CreateMs: now.Add(-2 * time.Hour).UnixMilli(), MessagePosition: 2,
			SenderID: "ou_me", SenderName: "林岚", RawJSON: "{}"},
	}
	_, err = st.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)
	for id, text := range map[string]string{"om_1": "发布单合了吗", "om_2": "还没"} {
		require.NoError(t, st.UpdateRendered(ctx, id, text, "", 2))
	}
	m := New(Deps{Store: st, Syncer: &sync.Syncer{Store: st}, Self: "ou_me", AI: f})
	m.width, m.height = 130, 40
	m.chatID = "oc_quiet"
	m.focus = paneMessages
	m.cfg.AI.Context = 80
	m.chats = []store.Chat{{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"}}
	msgs[0].Content, msgs[0].RenderedAt = "发布单合了吗", 2
	msgs[1].Content, msgs[1].RenderedAt = "还没", 2
	m.msgsBase = msgs
	m.msgs = msgs
	m.layout()
	return m
}

// ask feeds one question through the panel: Enter in its box, the ask run,
// and the stream armed. The turn comes back for the chunks a test plays.
func ask(t *testing.T, m Model, question string) (Model, *aiTurn) {
	t.Helper()
	m = press(t, m, "a")
	m.aiP.input.SetValue(question)
	out, cmd := m.submitAI()
	m = out.(Model)
	started, ok := cmd().(aiStartedMsg)
	require.True(t, ok, "the ask arms a stream")
	out, _ = m.onAIStarted(started)
	m = out.(Model)
	s := m.aiP.session()
	require.NotNil(t, s)
	require.Len(t, s.turns, 1)
	return m, s.turns[0]
}

// aiOpenOn stands in for pressing a on a model a test built by hand: the
// panel open on the chat, with a session to draw.
func aiOpenOn(m Model) Model {
	if m.aiP == nil {
		m.aiP = newAI()
	}
	m.aiP.open = true
	m.aiP.chat = cmp.Or(m.chatID, "oc_1")
	m.layout()
	return m
}

func TestOpenAI_NothingIsGenerated(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)

	m = press(t, m, "a")

	require.True(t, m.aiOpen())
	require.Equal(t, modeInsert, m.mode)
	require.Equal(t, sideAI, m.side)
	require.Equal(t, paneInput, m.focus)
	require.Empty(t, f.asks, "opening asks the agent nothing")
}

func TestOpenAI_KeepsTheFrameUnderItAndEscUncoversIt(t *testing.T) {
	f := newFakeAI()
	m := threadFrame(130, 30)
	m.rightInput.SetValue("回复草稿")
	m.deps.AI = f

	m = press(t, m, "a")
	require.True(t, m.aiOpen())
	require.Equal(t, rightThread, m.rightKind, "the frame is covered, not closed")
	require.Equal(t, "omt_1", m.threadID)
	require.Equal(t, "回复草稿", m.rightInput.Value(), "the covered box keeps its draft")

	m = m.closeAI()
	require.False(t, m.aiOpen())
	require.True(t, m.threadOpen(), "Esc puts the frame back")
	require.Equal(t, paneThread, m.focus)
}

func TestAskAI_ReadsTheWindowFromTheStoreAndCarriesHistory(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	// A message the model's page never saw: the window is the store's, not
	// the pane's.
	st := m.deps.Store
	_, err := st.UpsertMessages(t.Context(), []store.Message{
		{MessageID: "om_3", ChatID: "oc_quiet", CreateMs: time.Now().UnixMilli(), MessagePosition: 3,
			SenderID: "ou_a", SenderName: "张三", RawJSON: "{}"}}, 3)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(t.Context(), "om_3", "现在合了吗", "", 4))

	m, first := ask(t, m, "发布单怎么回？")
	require.Len(t, f.asks, 1)
	require.Contains(t, f.asks[0].transcript, "发布单合了吗", "the window comes from the store")
	require.Contains(t, f.asks[0].transcript, "现在合了吗")
	require.Contains(t, f.asks[0].prompt, "<ask>\n发布单怎么回？\n</ask>")

	// The answer lands, and the follow-up carries it as history.
	out, _ := m.onAIChunk(aiChunkMsg{turn: first.id, chunk: ai.Chunk{Text: "今晚合。"}})
	m = out.(Model)
	out, _ = m.onAIChunk(aiChunkMsg{turn: first.id, chunk: ai.Chunk{Done: true}})
	m = out.(Model)

	m.aiP.input.SetValue("那风险呢？")
	out, cmd := m.submitAI()
	m = out.(Model)
	started, ok := cmd().(aiStartedMsg)
	require.True(t, ok)
	out, _ = m.onAIStarted(started)
	m = out.(Model)

	require.Len(t, f.asks, 2)
	require.Contains(t, f.asks[1].prompt, "<ask>\n发布单怎么回？\n</ask>", "the earlier turn rides along")
	require.Contains(t, f.asks[1].prompt, "<answer>\n今晚合。\n</answer>")
	require.Contains(t, f.asks[1].prompt, "<ask>\n那风险呢？\n</ask>")
}

func TestAskAI_AnchorAndDraftRideInTheAboutBlock(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)

	m.msgIdx = 0 // cursor on 张三's message
	m = press(t, m, "a")
	require.NotNil(t, m.aiP.anchor, "the anchor is the message under the cursor")
	require.Equal(t, "om_1", m.aiP.anchor.MessageID)
	m.input.SetValue("我的草稿")
	m.aiP.input.SetValue("帮我润色")
	out, cmd := m.submitAI()
	m = out.(Model)
	_, ok := cmd().(aiStartedMsg)
	require.True(t, ok)
	require.Len(t, f.asks, 1)
	// The anchor sits inside the store's window already, and the draft is the
	// reader's own words, so both travel in the about block of the prompt.
	require.Contains(t, f.asks[0].prompt, "<draft>\n我的草稿\n</draft>")
	require.Contains(t, f.asks[0].transcript, "id=om_1")
	require.Contains(t, f.asks[0].prompt, "<ask>\n帮我润色\n</ask>")
}

func TestOnAIChunk_EscKeepsTheAnswerComing(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "总结一下")

	m = m.closeAI()
	out, cmd := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "一半"}})
	m = out.(Model)
	require.NotNil(t, cmd, "the stream is still being read")
	require.Equal(t, "一半", t1.answer)

	out, _ = m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Done: true}})
	m = out.(Model)
	require.Equal(t, aiDone, t1.state, "the answer finishes into its session off screen")
	select {
	case <-f.cancelled:
		t.Fatal("closing the pane cancelled the answer")
	default:
	}
	require.Empty(t, m.input.Value(), "no model output reaches a composer")
}

func TestOnAIChunk_ATurnNobodyHoldsIsDropped(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, _ = ask(t, m, "总结一下")
	s := m.aiP.session()
	gone := s.turns[0]
	s.turns = nil

	out, cmd := m.onAIChunk(aiChunkMsg{turn: gone.id, chunk: ai.Chunk{Text: "late"}})
	m = out.(Model)
	require.Nil(t, cmd)
	require.Empty(t, gone.answer)
}

func TestAskAI_AChatSwitchKeepsTheOldChatSession(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "总结一下")

	m.pendingChat = "oc_elsewhere"
	m.chatID = "oc_elsewhere"
	_ = m.enterChat()
	require.True(t, m.aiOpen(), "the panel follows the reader")
	require.Equal(t, "oc_elsewhere", m.aiP.chat)
	require.Empty(t, m.aiP.sess, "another chat's sessions are its own")

	out, _ := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "答", Done: true}})
	m = out.(Model)
	require.Equal(t, aiDone, t1.state, "the old chat's answer lands in the old chat's session")
	require.Empty(t, m.input.Value(), "a draft of the old chat never reaches the new one's composer")
}

func TestAskAI_StopCancelsTheAnswer(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, _ = ask(t, m, "总结一下")

	m = press(t, m, "esc") // back to the pane, so x is a pane key
	m, _, _ = m.onAIKey("x")
	// The fake closes its channel from a goroutine watching the context, so
	// the close is waited for rather than assumed immediate.
	select {
	case <-f.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("x left the agent running")
	}

	mm, _ := m.onAIChunk(aiChunkMsg{turn: m.aiP.session().turns[0].id, chunk: ai.Chunk{Stopped: true, Done: true}})
	m = mm.(Model)
	require.Equal(t, aiStopped, m.aiP.session().turns[0].state)
}

// The frame hidden under the panel keeps its fields but not its keys: a
// message nobody can see is not a message to act on.
func TestOnAIKey_TheHiddenFrameGetsNoKeys(t *testing.T) {
	f := newFakeAI()
	m := threadFrame(130, 30)
	m.deps.AI = f
	m = press(t, m, "a")
	m.mode, m.side = modeNormal, sideMain
	m.areap().Blur()

	for _, key := range []string{"r", "R", "D", "E", "e", "t", "o"} {
		out, _, took := m.onAIKey(key)
		m = out
		require.True(t, took, "the %s key belongs to the panel", key)
	}
	require.Equal(t, modeNormal, m.mode)
	require.Nil(t, m.replyTo, "no reply was started against the hidden thread")
	require.Equal(t, confirmNone, m.confirm.kind, "no recall was armed")
}

func TestOnAIKey_EnterMovesToTheInput(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m = press(t, m, "a")
	m.mode = modeNormal
	m.areap().Blur()

	out, cmd, took := m.onAIKey("i")
	m = out
	require.True(t, took)
	require.Equal(t, modeInsert, m.mode)
	require.Equal(t, sideAI, m.side)
	_ = cmd
}

// The draft hand-off stays for the :ai draft form, and only into the chat the
// question was asked in.
func TestAskAI_TheDraftFormFillsTheComposerOfItsOwnChat(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)

	out, cmd := m.askCommand("draft 委婉")
	m = out.(Model)
	started, ok := cmd().(aiStartedMsg)
	require.True(t, ok)
	out, _ = m.onAIStarted(started)
	m = out.(Model)
	turn := m.aiP.session().turns[0]
	require.True(t, turn.draft)
	require.Equal(t, "draft 委婉", strings.TrimSpace(turn.ask), "the form's wording is what the list shows")

	out, _ = m.onAIChunk(aiChunkMsg{turn: turn.id, chunk: ai.Chunk{Text: "好的，今晚合。", Done: true}})
	m = out.(Model)
	require.Equal(t, "好的，今晚合。", m.input.Value(), "the finished draft lands in the chat's composer")

	m.input.Reset()
	m.aiP.chat = "oc_elsewhere"
	out, _ = m.onAIChunk(aiChunkMsg{turn: turn.id, chunk: ai.Chunk{Text: "好的，今晚合。", Done: true}})
	m = out.(Model)
	require.Empty(t, m.input.Value(), "another chat's draft stays out of this one's composer")
}

func TestAskAI_WithoutAnAgentTheQuestionStillLands(t *testing.T) {
	m := aiFixture(t, nil)
	m.ai = nil

	m, turn := ask(t, m, "总结一下")
	require.Equal(t, aiAsking, turn.state)
	out, _ := m.onAIChunk(aiChunkMsg{turn: turn.id, chunk: ai.Chunk{Err: errAssistantOff, Done: true}})
	m = out.(Model)
	require.Equal(t, aiFailed, turn.state)
	require.Contains(t, turn.err, "assistant off")
}

// Sessions switch without stopping anything, and A starts a fresh one with
// the keys already in it.
func TestOnAIKey_SessionsSwitchAndStart(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, first := ask(t, m, "总结一下")

	out, _, _ := m.onAIKey("A")
	m = out
	require.Len(t, m.aiP.sess, 2)
	require.Equal(t, 1, m.aiP.cur)
	require.Equal(t, modeInsert, m.mode)

	out, _, _ = m.onAIKey("[")
	m = out
	require.Equal(t, 0, m.aiP.cur, "back to the first session")
	require.Equal(t, aiAsking, first.state, "switching stopped nothing")
}

func TestAskAI_VISUALSelectionBecomesContext(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m.msgIdx = 1
	m = press(t, m, "v", "k")
	require.Equal(t, modeVisual, m.mode)

	m = press(t, m, "a")
	require.True(t, m.aiOpen())
	require.Equal(t, modeInsert, m.mode, "VISUAL is left behind and the keys land in the panel's box")
	require.Equal(t, []string{"om_1", "om_2"}, m.aiP.selection)
	require.Nil(t, m.aiP.anchor)

	m.aiP.input.SetValue("这两条什么意思？")
	out, cmd := m.submitAI()
	m = out.(Model)
	_, ok := cmd().(aiStartedMsg)
	require.True(t, ok)
	require.Len(t, f.asks, 1)
	require.Contains(t, f.asks[0].prompt, "id=om_1")
	require.Contains(t, f.asks[0].prompt, "id=om_2")
}

func TestRenderAI_ShowsTheTurnsAndItsOwnBand(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "发布单怎么回？")
	out, _ := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "今晚合。", Done: true}})
	m = out.(Model)
	m.aiP.rebuild(m)

	pane := ansi.Strip(m.renderAI(m.bodyHeight()))
	require.Contains(t, pane, "发布单怎么回？")
	require.Contains(t, pane, "今晚合。", "the answer renders through the markdown path")
	require.Contains(t, pane, "↩ 张三: 发布单合了吗", "the strip names what the question was about")

	// The column owns its band: the box under it holds the question input,
	// named while it is empty.
	band := ansi.Strip(m.renderBand(sideAI))
	require.Contains(t, band, "Enter ask")
	require.Contains(t, band, "Ask about this chat…")
}
