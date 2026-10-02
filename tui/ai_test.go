package tui

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
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

// aiAsk is one question the fake was asked, as Stream received it. message
// says it was the stream-to-chat variant, history that the agent was given
// the chat's own reading reach.
type aiAsk struct {
	transcript, prompt string
	message            bool
	history            ai.History
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

func (f *fakeAI) StreamHistory(ctx context.Context, transcript, prompt string, h ai.History) <-chan ai.Chunk {
	f.asks = append(f.asks, aiAsk{transcript: transcript, prompt: prompt, history: h})
	go func() {
		<-ctx.Done()
		close(f.cancelled)
	}()
	return f.ch
}

func (f *fakeAI) StreamChat(ctx context.Context, transcript, prompt string) <-chan ai.Chunk {
	f.asks = append(f.asks, aiAsk{transcript: transcript, prompt: prompt, message: true})
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

// askStarted runs the batch an ask returns — every cmd of it, the way a live
// run does, saves included — and hands back the stream-start message among
// them.
func askStarted(t *testing.T, cmd tea.Cmd) aiStartedMsg {
	t.Helper()
	var found aiStartedMsg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch v := c().(type) {
		case aiStartedMsg:
			found = v
		case tea.BatchMsg:
			for _, cc := range v {
				run(cc)
			}
		}
	}
	run(cmd)
	require.NotZero(t, found.turn, "the ask arms a stream")
	return found
}

// ask feeds one question through the panel: Enter in its box, the ask run,
// and the stream armed. The turn comes back for the chunks a test plays.
func ask(t *testing.T, m Model, question string) (Model, *aiTurn) {
	t.Helper()
	m = press(t, m, "a")
	m.aiP.input.SetValue(question)
	out, cmd := m.submitAI()
	m = out.(Model)
	started := askStarted(t, cmd)
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
		m.aiP = newAI(false)
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
	started := askStarted(t, cmd)
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
	askStarted(t, cmd)
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

	for _, key := range []string{"r", "R", "E", "e", "t", "o"} {
		out, _, took := m.onAIKey(key)
		m = out
		require.True(t, took, "the %s key belongs to the panel", key)
	}
	require.Equal(t, modeNormal, m.mode)
	require.Nil(t, m.replyTo, "no reply was started against the hidden thread")
	require.Equal(t, confirmNone, m.confirm.kind, "no recall was armed")

	// D deletes the panel's own session, which asks first; the frame beneath
	// is untouched either way.
	m.confirm = confirmation{}
	out, _, took := m.onAIKey("D")
	m = out
	require.True(t, took)
	require.Equal(t, confirmDeleteAI, m.confirm.kind, "D arms the session delete, not a recall")
	require.Nil(t, m.replyTo)
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

// :ai with a snippet's name asks the snippet's text, and no composer is
// touched by an answer: Insert is the only path that writes into one.
func TestAskAI_ASnippetNameAsksTheSnippet(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)

	out, cmd := m.askCommand("summary")
	m = out.(Model)
	started := askStarted(t, cmd)
	out, _ = m.onAIStarted(started)
	m = out.(Model)
	turn := m.aiP.session().turns[0]
	require.Equal(t, "Summary", strings.TrimSpace(turn.ask), "the snippet's name is what the list shows")
	require.Contains(t, turn.sent, "Summarize this chat")
	require.Len(t, f.asks, 1)
	require.Contains(t, f.asks[0].prompt, "action items with owners")

	out, _ = m.onAIChunk(aiChunkMsg{turn: turn.id, chunk: ai.Chunk{Text: "讨论了发布。", Done: true}})
	m = out.(Model)
	require.Empty(t, m.input.Value(), "no model output reaches a composer")
}

func TestAskAI_WithoutAnAgentTheQuestionStillLands(t *testing.T) {
	m := aiFixture(t, nil)
	m.ai = nil

	m, turn := ask(t, m, "总结一下")
	require.Equal(t, aiAsking, turn.state)
	out, _ := m.onAIChunk(aiChunkMsg{turn: turn.id, chunk: ai.Chunk{Err: m.assistantOff(), Done: true}})
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
	askStarted(t, cmd)
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

// A restart finds the chat's conversations where they were left, and another
// chat's header does not list them.
func TestAIPanel_ARestartFindsTheSessionsWhereTheyWereLeft(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "发布单怎么回？")
	// The save the finish fires is part of the finish: run it before the
	// store is re-read, the way a live run does.
	mm, done := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "今晚合。", Done: true}})
	m = mm.(Model)
	require.NotNil(t, done)
	done()

	// A new model over the same store, the way a restart is.
	f2 := newFakeAI()
	reborn := New(Deps{Store: m.deps.Store, Syncer: &sync.Syncer{Store: m.deps.Store},
		Self: "ou_me", AI: f2})
	reborn.width, reborn.height = 130, 40
	reborn.chatID = "oc_quiet"
	reborn.chats = m.chats
	reborn.layout()
	reborn = press(t, reborn, "a")

	require.Len(t, reborn.aiP.sess, 1, "the stored session comes back")
	s := reborn.aiP.session()
	require.Equal(t, "发布单怎么回？", s.title)
	require.Len(t, s.turns, 1)
	require.Equal(t, "今晚合。", s.turns[0].answer)
	require.Equal(t, aiDone, s.turns[0].state)

	// Another chat's header lists nothing of this chat's.
	reborn.pendingChat = "oc_elsewhere"
	reborn.chatID = "oc_elsewhere"
	_ = reborn.enterChat()
	require.Empty(t, reborn.aiP.sess, "another chat's sessions are its own")
}

// A turn still asking at load is an answer nobody finished: it reads as
// interrupted, not as one still owed.
func TestAIPanel_ATurnStillAskingAtLoadIsInterrupted(t *testing.T) {
	m := aiFixture(t, newFakeAI())
	// The row an ask writes before any answer arrives, left behind by a run
	// that died mid-stream.
	st := m.deps.Store
	require.NoError(t, st.UpsertChats(t.Context(), []store.Chat{{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"}}, 1))
	s := &aiSession{id: "as_kept", title: "总结", created: 10}
	tt := &aiTurn{id: "at_kept", seq: 0, ask: "总结一下", sent: "总结一下",
		window: 80, state: aiAsking, at: time.UnixMilli(11)}
	s.turns = append(s.turns, tt)
	require.NoError(t, st.SaveAISession(t.Context(), store.AISession{ID: "as_kept", ChatID: "oc_quiet", Title: "总结", CreatedMs: 10}))
	require.NoError(t, st.SaveAITurn(t.Context(), storedTurn(s, tt)))

	f2 := newFakeAI()
	reborn := New(Deps{Store: st, Syncer: &sync.Syncer{Store: st}, Self: "ou_me", AI: f2})
	reborn.width, reborn.height = 130, 40
	reborn.chatID = "oc_quiet"
	reborn.layout()
	reborn = press(t, reborn, "a")

	_, loaded := reborn.aiP.findTurn("at_kept")
	require.NotNil(t, loaded)
	require.Equal(t, aiInterrupted, loaded.state)
	require.Empty(t, reborn.aiP.session().turns[0].ch, "nobody owes this answer anymore")
}

// D asks first, and y drops the session and its turns from the panel and the
// store.
func TestDeleteAI_AsksFirstAndDropsTheStoredRows(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "总结一下")
	mm, _ := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "答", Done: true}})
	m = mm.(Model)

	m.mode, m.side = modeNormal, sideMain
	m.areap().Blur()

	out, _, _ := m.onAIKey("D")
	m = out
	require.Equal(t, confirmDeleteAI, m.confirm.kind, "D asks before it deletes")

	// n leaves everything where it was.
	nout, ncmd, _ := m.answerConfirm("n")
	m = nout.(Model)
	require.Nil(t, ncmd)
	sessions, err := m.deps.Store.ListAISessions(t.Context(), "oc_quiet")
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	out, _, _ = m.onAIKey("D")
	m = out
	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	require.NotNil(t, ycmd)
	ycmd()
	require.Empty(t, m.confirm.kind)
	require.Len(t, m.aiP.sess, 1, "the panel opens an empty session when none is left")
	require.Empty(t, m.aiP.sess[0].turns)
	require.Empty(t, m.aiP.sess[0].title)
	sessions, err = m.deps.Store.ListAISessions(t.Context(), "oc_quiet")
	require.NoError(t, err)
	require.Empty(t, sessions, "the rows went with the conversation")
}

// askAnchored asks a question whose anchor is set by hand, the way a cursor
// on a thread reply would leave it.
func askAnchored(t *testing.T, m Model, anchor *store.Message, question string) (Model, *aiTurn) {
	t.Helper()
	m = press(t, m, "a")
	m.aiP.anchor = anchor
	m.aiP.input.SetValue(question)
	out, cmd := m.submitAI()
	m = out.(Model)
	started := askStarted(t, cmd)
	out, _ = m.onAIStarted(started)
	m = out.(Model)
	s := m.aiP.session()
	require.NotNil(t, s)
	return m, s.turns[0]
}

// answerDone finishes a turn's answer and runs the save its finish fires.
func answerDone(t *testing.T, m Model, t1 *aiTurn, text string) Model {
	t.Helper()
	out, done := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: text, Done: true}})
	m = out.(Model)
	require.NotNil(t, done)
	done()
	return m
}

// A finished answer's blocks become cards drawn through the preview's own
// rendering, with their actions under them; a streaming answer shows none of
// that yet.
func TestRebuild_BlocksBecomeCardsWithActions(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "帮我回复")
	m = answerDone(t, m, t1, "两选一：\n<reply>\n今晚合。\n</reply>\n<reply>\n明早合，先跑回归。\n</reply>")
	m.aiP.rebuild(m)

	feet, cards := 0, 0
	for _, a := range m.aiP.rowAct {
		if a.card >= 0 {
			cards++
		}
	}
	for _, r := range m.aiP.rows {
		if strings.Contains(ansi.Strip(r.text), "Insert") {
			feet++
		}
	}
	require.Equal(t, 2, feet, "each card draws its own action row")
	require.Positive(t, cards)
	require.Contains(t, ansi.Strip(m.renderAI(m.bodyHeight())), "今晚合。")

	// While the answer streams there is nothing to act on yet.
	m2, t2 := ask(t, aiFixture(t, newFakeAI()), "帮我回复")
	out, _ := m2.onAIChunk(aiChunkMsg{turn: t2.id, chunk: ai.Chunk{Text: "<reply>\n写到一半"}})
	m2 = out.(Model)
	require.NotContains(t, ansi.Strip(m2.renderAI(m2.bodyHeight())), "Insert",
		"actions wait for the answer to finish")
}

// The action zones hit at their edges and miss the cells beside them.
func TestAICardZones_HitAtTheEdgesMissBesideThem(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "帮我回复")
	m = answerDone(t, m, t1, "<reply>\n今晚合。\n</reply>")
	m.aiP.rebuild(m)

	line := -1
	for i, r := range m.aiP.rows {
		if strings.Contains(ansi.Strip(r.text), "Insert") {
			line = i
			break
		}
	}
	require.GreaterOrEqual(t, line, 0)
	// One zone of the row: its own edges hit, and the cells beside it — or
	// past the row's end — answer nothing that belongs to another act.
	at := func(x int) (clickZone, bool) { return zoneAt(m.aiP.rows, line, x) }
	for _, z := range m.aiP.rows[line].zones {
		hl, ok := at(z.x0)
		require.True(t, ok && hl.act == z.act, "the left edge hits")
		hr, ok := at(z.x1 - 1)
		require.True(t, ok && hr.act == z.act, "the right edge hits")
		if next, ok := at(z.x1); ok {
			require.NotEqual(t, z.act, next.act, "the cell after the zone is not this zone")
		}
	}
}

// Insert fills the chat's box with the card's text, quoting the anchor — in
// its thread when the anchor is a reply of one — and never sends anything.
func TestInsertCard_FillsTheChatBoxQuotingTheAnchor(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	anchor := store.Message{MessageID: "om_r", ChatID: "oc_quiet", ThreadID: "omt_9",
		MessagePosition: -1, SenderID: "ou_a", SenderName: "张三", Content: "发布单合了吗"}
	m, t1 := askAnchored(t, m, &anchor, "怎么回")
	m = answerDone(t, m, t1, "<reply>\n好的，明早合。\n</reply>")

	m.mode = modeNormal
	m.areap().Blur()
	// The cursor stands on the card the follow left it on.
	out, _, took := m.onAIKey("enter")
	m = out
	require.True(t, took)
	require.False(t, m.aiOpen(), "inserting puts the panel away")
	require.Equal(t, "om_r", m.replyTo.MessageID, "the box quotes the anchor")
	require.True(t, m.inThrd, "a reply of a thread is answered in the thread")
	require.Equal(t, "好的，明早合。", m.input.Value())
	require.Empty(t, m.outbox, "inserting never sends")
	require.Equal(t, modeInsert, m.mode)
}

// Insert into an empty box fills it; a box that already holds text gets the
// card at the cursor; and the question's draft chip turns it into Replace.
func TestInsertCard_ReplacesWhenTheQuestionCarriedTheDraft(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m.input.SetValue("旧草稿")
	m, t1 := ask(t, m, "润色一下")
	m = answerDone(t, m, t1, "<reply>\n新的措辞\n</reply>")

	m.mode = modeNormal
	m.areap().Blur()
	out, _, _ := m.onAIKey("r")
	m = out
	require.Equal(t, "新的措辞", m.input.Value(), "the draft chip the question carried makes this Replace")
}

func TestInsertCard_ATypedBoxGetsTheCardAtTheCursor(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "给个措辞")
	m = answerDone(t, m, t1, "<reply>\n插进来\n</reply>")
	// The box picked up typing after the question was asked, so no draft chip
	// rode with it: the text goes in at the cursor, not over what is there.
	m.input.Reset()
	m.input.SetValue("先说这个 ")
	m.mode = modeNormal
	m.areap().Blur()
	out, _, _ := m.onAIKey("r")
	m = out
	require.Equal(t, "先说这个 插进来", m.input.Value())
}

// The thread frame's own box is used when the anchor's thread is the frame
// standing under the panel.
func TestInsertCard_TheFrameUnderThePanelTakesTheThreadAnswer(t *testing.T) {
	f := newFakeAI()
	m := threadFrame(130, 30)
	m.deps.AI = f
	// The fixture builds its model by hand; the ask needs a store to save
	// its turn into, and an empty one answers.
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m.deps.Store = st
	// threadFrame loads a thread omt_1 whose replies are anchored there.
	root := m.thread[0]
	anchor := m.thread[len(m.thread)-1]
	require.NotEqual(t, root.MessageID, anchor.MessageID)
	require.NotEmpty(t, anchor.ThreadID)
	m, t1 := askAnchored(t, m, &anchor, "怎么回")
	m = answerDone(t, m, t1, "<reply>\n在串里回\n</reply>")

	m.mode = modeNormal
	m.areap().Blur()
	out, _, _ := m.onAIKey("enter")
	m = out
	require.False(t, m.aiOpen())
	require.Equal(t, rightThread, m.rightKind, "the frame is back, holding the answer")
	require.Equal(t, anchor.MessageID, m.rightReply.MessageID)
	require.Equal(t, "在串里回", m.rightInput.Value())
}

// yy copies the card under the cursor, Y the whole answer.
func TestCopy_CardAndAnswer(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, t1 := ask(t, m, "给个措辞")
	m = answerDone(t, m, t1, "解说\n<reply>\n卡片文字\n</reply>")
	m.mode = modeNormal
	m.areap().Blur()

	out, arm, _ := m.onAIKey("y")
	m = out
	require.Nil(t, arm, "arming the prefix copies nothing yet")
	out, cmd, _ := m.onAIKey("y")
	m = out
	require.Equal(t, "卡片文字", clipboard(t, cmd))

	out, cmd, _ = m.onAIKey("Y")
	m = out
	require.Equal(t, "解说\n<reply>\n卡片文字\n</reply>", clipboard(t, cmd),
		"Y takes the whole answer as the agent wrote it")
}

// Regenerate re-asks from the recorded context: the window is cut at the
// time the question was asked, and the answer being replaced does not ride
// along as history.
func TestRegenerate_RebuildsThePromptWithTheWindowCutAtAskTime(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	st := m.deps.Store
	m, t1 := ask(t, m, "总结一下")
	m = answerDone(t, m, t1, "第一版答案")
	asked := t1.at

	// A message that arrived after the question was asked.
	_, err := st.UpsertMessages(t.Context(), []store.Message{
		{MessageID: "om_late", ChatID: "oc_quiet", CreateMs: asked.Add(time.Minute).UnixMilli(),
			MessagePosition: 9, SenderID: "ou_a", SenderName: "张三", RawJSON: "{}"}}, 9)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(t.Context(), "om_late", "后来的消息", "", 10))

	m.mode = modeNormal
	m.areap().Blur()
	out, cmd, _ := m.onAIKey(".")
	m = out
	started := askStarted(t, cmd)
	sout, _ := m.onAIStarted(started)
	m = sout.(Model)

	require.Len(t, f.asks, 2)
	require.Contains(t, f.asks[1].transcript, "发布单合了吗", "the window is the one the question saw")
	require.NotContains(t, f.asks[1].transcript, "后来的消息", "the window is cut at the ask time")
	require.NotContains(t, f.asks[1].prompt, "第一版答案", "the answer being replaced is not its own history")
	require.Contains(t, f.asks[1].prompt, "<ask>\n总结一下\n</ask>", "the same question is asked")
	require.Equal(t, aiAsking, t1.state)
}

// aiSendFixture is the AI fixture with a Fake client behind it, so a card's
// send reaches something that answers.
func aiSendFixture(t *testing.T, f *fakeAI) (Model, *larkcli.Fake) {
	t.Helper()
	c := larkcli.NewFake()
	m := aiFixture(t, f)
	m.deps.Client = c
	return m, c
}

// s asks first, naming the chat, the first line of the text and its @All; y
// posts exactly one message to the panel's chat, and the card earns its ✓.
func TestSendCard_AsksFirstAndYPostsOnce(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	m, t1 := ask(t, m, "帮我回")
	m = answerDone(t, m, t1, "<reply>\n今晚合 #4412。@All\n</reply>")
	m.mode = modeNormal
	m.areap().Blur()

	out, _, _ := m.onAIKey("s")
	m = out
	require.Equal(t, confirmAISend, m.confirm.kind)
	require.Contains(t, m.notice, "send to 平台组", "the prompt names the chat")
	require.Contains(t, m.notice, "今晚合 #4412", "the prompt shows the first line")
	require.Contains(t, m.notice, "@All", "the prompt says when it mentions @All")
	require.Empty(t, c.Sent)

	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	require.Len(t, m.outbox, 1)
	require.Equal(t, "oc_quiet", m.outbox[0].chatID)
	require.Empty(t, m.outbox[0].replyTo, "a send posts a new message")
	res := ycmd()
	next, _ := m.Update(res)
	m = next.(Model)
	require.Len(t, c.Sent, 1, "y records exactly one send")
	require.Contains(t, ansi.Strip(m.renderAI(m.bodyHeight())), "✓ sent",
		"the card shows it went")

	// Sending it again asks again.
	out, _, _ = m.onAIKey("s")
	m = out
	require.Equal(t, confirmAISend, m.confirm.kind)
}

// n and Esc record no send at all.
func TestSendCard_NAndEscSendNothing(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	m, t1 := ask(t, m, "帮我回")
	m = answerDone(t, m, t1, "<reply>\n今晚合。\n</reply>")
	m.mode = modeNormal
	m.areap().Blur()

	out, _, _ := m.onAIKey("s")
	m = out
	nout, _, _ := m.answerConfirm("n")
	m = nout.(Model)
	require.Empty(t, m.outbox)
	require.Empty(t, c.Sent)

	out, _, _ = m.onAIKey("s")
	m = out
	// Esc cancels through the ordinary key path: anything but y or n.
	eout, _, _ := m.answerConfirm("esc")
	m = eout.(Model)
	require.Empty(t, m.outbox)
	require.Empty(t, c.Sent)
}

// S replies to the question's anchor, inside its thread when it is in one.
func TestReplyCard_RepliesToTheAnchorInItsThread(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	c.Messages["om_r"] = larkcli.RawMessage{MessageID: "om_r", ChatID: "oc_quiet"}
	anchor := store.Message{MessageID: "om_r", ChatID: "oc_quiet", ThreadID: "omt_r",
		MessagePosition: -1, SenderID: "ou_a", SenderName: "张三", Content: "发布单合了吗"}
	m, t1 := askAnchored(t, m, &anchor, "怎么回")
	m = answerDone(t, m, t1, "<reply>\n好的，明早合。\n</reply>")
	m.mode = modeNormal
	m.areap().Blur()

	out, _, _ := m.onAIKey("S")
	m = out
	require.Contains(t, m.notice, "reply to 张三", "the prompt names whom")
	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	require.Len(t, m.outbox, 1)
	require.Equal(t, "om_r", m.outbox[0].replyTo)
	require.True(t, m.outbox[0].inThread, "a reply of a thread lands in the thread")
	require.Equal(t, "omt_r", m.outbox[0].threadID)
	res := ycmd()
	next, _ := m.Update(res)
	m = next.(Model)
	require.Len(t, c.Sent, 1)
	var sent larkcli.RawMessage
	for _, x := range c.Messages {
		if x.ParentID == "om_r" {
			sent = x
		}
	}
	require.NotEmpty(t, sent.ThreadID, "Feishu holds the reply inside a thread")
}

// A card whose text names a local file asks, and the answer is the reader's:
// y sends it with the upload, n leaves the reference as text, and nothing
// uploads without the y.
func TestSendCard_ALocalFileIsTheReadersChoice(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/发布说明.pdf": 2048})
	card := "[发布说明.pdf](~/发布说明.pdf)"
	m, t1 := ask(t, m, "转给他们")
	m = answerDone(t, m, t1, "<reply>\n"+card+"\n</reply>")
	m.mode = modeNormal
	m.areap().Blur()

	out, _, _ := m.onAIKey("s")
	m = out
	require.Contains(t, m.notice, "发布说明.pdf", "the prompt shows the file")

	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	require.Equal(t, "file", m.outbox[0].msgType, "y sends it with the upload")
	res := ycmd()
	next, _ := m.Update(res)
	m = next.(Model)
	require.Len(t, c.Sent, 1)

	// Sending the same card again asks again, and n sends without the file:
	// the reference stays text.
	m.mode = modeNormal
	m.areap().Blur()
	out2, _, _ := m.onAIKey("s")
	m = out2
	nout, ncmd, _ := m.answerConfirm("n")
	m = nout.(Model)
	require.Equal(t, "text", m.outbox[1].msgType, "n sends the reference as text")
	require.Empty(t, m.outbox[1].file.local)
	res2 := ncmd()
	next2, _ := m.Update(res2)
	m = next2.(Model)
	require.Len(t, c.Sent, 2)
	require.Equal(t, card, m.outbox[1].send.Text, "the reference stays what the reader can read")
}

// A name resolves only when the destination is the open chat; anywhere else
// it stays the text the reader can read.
func TestSendCard_AnAtNameOutsideTheOpenChatStaysText(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	m, t1 := ask(t, m, "帮我回")
	m = answerDone(t, m, t1, "<reply>\n@张三 收一下\n</reply>")
	m.mode = modeNormal
	m.areap().Blur()
	// The card belongs to a chat the reader has since left.
	m.aiP.chat = "oc_elsewhere"

	out, _, _ := m.onAIKey("s")
	m = out
	require.Contains(t, m.notice, "@names stay text", "the prompt says so")
	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	require.Equal(t, "oc_elsewhere", m.outbox[0].chatID)
	res := ycmd()
	next, _ := m.Update(res)
	m = next.(Model)
	require.Len(t, c.Sent, 1)
	require.NotContains(t, c.Sent[0].Text, "<at", "the name was never turned into a tag")
	require.Contains(t, c.Sent[0].Text, "@张三")
}

// The digits and the chips insert a snippet without asking anything.
func TestSnippets_InsertWithoutAsking(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m = press(t, m, "a")
	m.mode = modeNormal
	m.areap().Blur()

	out, _, _ := m.onAIKey("1")
	m = out
	require.Equal(t, ai.BuiltinSnippets()[0].Text, m.aiP.input.Value(),
		"the digit puts the snippet's text in the box")
	require.Equal(t, modeInsert, m.mode, "the keys land in the box")
	require.Empty(t, f.asks, "inserting never asks")

	// The band draws the offers under their digits.
	band := ansi.Strip(m.renderBand(sideAI))
	require.Contains(t, band, "1 Summary")
	require.Contains(t, band, "2 Draft")
	require.Contains(t, band, "/ snippets")
}

// ai.snippets replaces the built-ins whole.
func TestSnippets_TheConfigListReplacesTheBuiltins(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m.cfg.AI.Snippets = config.SnippetList{{Name: "Standup", Text: "Summarize today's blockers."}}
	m = press(t, m, "a")
	m.mode = modeNormal
	m.areap().Blur()

	out, _, _ := m.onAIKey("1")
	m = out
	require.Equal(t, "Summarize today's blockers.", m.aiP.input.Value())
	band := ansi.Strip(m.renderBand(sideAI))
	require.Contains(t, band, "1 Standup")
	require.NotContains(t, band, "Summary", "the built-ins went with the list")

	// And :ai names the replacement offer.
	cout, cmd := m.askCommand("standup")
	m = cout.(Model)
	started := askStarted(t, cmd)
	sout, _ := m.onAIStarted(started)
	m = sout.(Model)
	require.Contains(t, m.aiP.session().turns[0].sent, "blockers")
}

// The / popup offers the snippets, filters as typed, and writes the chosen
// text in the box.
func TestSnippets_TheSlashPopupFillsTheBox(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m = press(t, m, "a")

	m.aiP.input.SetValue("/")
	m.takePum()
	require.True(t, m.pum.open(), "a bare / at the start opens the offers")
	require.Len(t, m.pum.menu.items, 3)

	m.aiP.input.SetValue("/opt")
	m.takePum()
	require.True(t, m.pum.open())
	require.Len(t, m.pum.menu.items, 1, "the query narrows the offers")

	out, _, took := m.onPumKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = out.(Model)
	require.True(t, took)
	require.False(t, strings.HasPrefix(m.aiP.input.Value(), "/"), "the / run is erased")
	require.Contains(t, m.aiP.input.Value(), "three different replies", "the chosen text stands in the box")
	require.False(t, m.pum.open())

	// A / anywhere else opens nothing.
	m.aiP.input.SetValue("看下 /etc/hosts")
	m.takePum()
	require.False(t, m.pum.open())
}

// streamWritten runs a cmd batch the stream pipeline returned and hands back
// the card-write answer in it. The waitForAI arm of the batch consumes a
// chunk the test has already buffered, so nothing blocks.
func streamWritten(t *testing.T, cmd tea.Cmd) (streamWrittenMsg, bool) {
	t.Helper()
	var found streamWrittenMsg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch v := c().(type) {
		case streamWrittenMsg:
			found = v
		case tea.BatchMsg:
			for _, cc := range v {
				run(cc)
			}
		}
	}
	run(cmd)
	return found, found.turn != ""
}

// streamAsk arms the ctrl+s ask and answers its y, leaving the stream armed.
func streamAsk(t *testing.T, m Model, question string) (Model, *aiTurn) {
	t.Helper()
	// A plain stream test carries no anchor; the caller that wants one sets it
	// after this.
	m.aiP.anchor, m.aiP.selection = nil, nil
	m.aiP.input.SetValue(question)
	out, _ := m.askStreamConfirm()
	m = out.(Model)
	require.Equal(t, confirmAIStream, m.confirm.kind)
	require.Equal(t, modeNormal, m.mode, "the y/n owns the next key, so the keys leave the box")
	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	started := askStarted(t, ycmd)
	sout, _ := m.onAIStarted(started)
	m = sout.(Model)
	t1 := m.aiP.session().turns[0]
	require.NotNil(t, t1.stream)
	return m, t1
}

// One post puts the card up, every rewrite carries the full text so far, and
// the last write is the final text.
func TestStreamToChat_OnePostThenWholeCardRewrites(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	m = press(t, m, "a")
	m, t1 := streamAsk(t, m, "把结论发出来")
	require.True(t, f.asks[0].message, "the ask is the message variant")
	require.Contains(t, f.asks[0].prompt, "<ask>\n把结论发出来\n</ask>")

	// The first piece posts the card at once.
	f.ch <- ai.Chunk{Text: "第二"}
	out, cmd := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "第一段"}})
	m = out.(Model)
	w, ok := streamWritten(t, cmd)
	require.True(t, ok, "the first piece posts")
	sout, _ := m.onStreamWritten(w)
	m = sout.(Model)
	require.Len(t, c.Sent, 1, "one send, and no more after it")
	require.Contains(t, ansi.Strip(m.renderAI(m.bodyHeight())), "● live in chat")

	// A rewrite while the answer grows carries the full text so far.
	f.ch <- ai.Chunk{Text: "，第二段"}
	out, cmd = m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "，第二段"}})
	m = out.(Model)
	w, ok = streamWritten(t, cmd)
	require.True(t, ok)
	sout, _ = m.onStreamWritten(w)
	m = sout.(Model)

	// The answer ends with its last piece, and the final write is the final
	// text.
	f.ch <- ai.Chunk{Done: true}
	out, cmd = m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "。", Done: true}})
	m = out.(Model)
	w, ok = streamWritten(t, cmd)
	require.True(t, ok, "the answer's end writes the card closed")
	sout, _ = m.onStreamWritten(w)
	m = sout.(Model)

	require.Len(t, c.Sent, 1, "still the one post")
	require.GreaterOrEqual(t, len(c.Patched), 2)
	require.Equal(t, "第一段，第二段。", larkcli.CardMarkdown(lastPatch(t, c)),
		"the last write is the final text, whole")
	require.True(t, t1.stream.closed)
	require.Contains(t, ansi.Strip(m.renderAI(m.bodyHeight())), "Recall")
}

// lastPatch is the content of the last card rewrite.
func lastPatch(t *testing.T, c *larkcli.Fake) string {
	t.Helper()
	_, content, _ := strings.Cut(c.Patched[len(c.Patched)-1], " ")
	return content
}

// x stops the answer and the card is marked interrupted, half-written no more.
func TestStreamToChat_StopLeavesTheCardMarkedInterrupted(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	m = press(t, m, "a")
	m, t1 := streamAsk(t, m, "写下去")

	f.ch <- ai.Chunk{Done: true}
	out, cmd := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "写了一半"}})
	m = out.(Model)
	w, ok := streamWritten(t, cmd)
	require.True(t, ok)
	sout, _ := m.onStreamWritten(w)
	m = sout.(Model)

	f.ch <- ai.Chunk{Done: true}
	out, cmd = m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Stopped: true, Done: true}})
	m = out.(Model)
	require.Equal(t, aiStopped, t1.state)
	w, ok = streamWritten(t, cmd)
	require.True(t, ok, "the stop writes the card closed")
	sout, _ = m.onStreamWritten(w)
	m = sout.(Model)
	require.True(t, t1.stream.closed)
	require.Contains(t, larkcli.CardMarkdown(lastPatch(t, c)), "(interrupted)",
		"a stopped answer says so on the card")
}

// A first post that fails keeps the answer in the panel, marked not posted.
func TestStreamToChat_AFailedSendKeepsTheAnswerInThePanel(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	c.SendErr = errors.New("boom")
	m = press(t, m, "a")
	m, t1 := streamAsk(t, m, "说说看")

	f.ch <- ai.Chunk{Done: true}
	out, cmd := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "答案"}})
	m = out.(Model)
	w, ok := streamWritten(t, cmd)
	require.True(t, ok)
	require.Error(t, w.err, "the post itself failed")
	sout, _ := m.onStreamWritten(w)
	m = sout.(Model)

	require.Empty(t, t1.stream.messageID, "nothing of it reached the chat")
	require.NotEmpty(t, t1.stream.err)
	require.Contains(t, ansi.Strip(m.renderAI(m.bodyHeight())), "not posted",
		"the answer stays in the panel and says what happened")
	require.Contains(t, ansi.Strip(m.renderAI(m.bodyHeight())), "Send", "Send is offered")
}

// With an anchor the card is a reply, inside its thread when it is in one.
func TestStreamToChat_AnAnchoredAnswerRepliesIntoTheThread(t *testing.T) {
	f := newFakeAI()
	m, c := aiSendFixture(t, f)
	c.Messages["om_r"] = larkcli.RawMessage{MessageID: "om_r", ChatID: "oc_quiet"}
	m = press(t, m, "a")
	anchor := store.Message{MessageID: "om_r", ChatID: "oc_quiet", ThreadID: "omt_r",
		MessagePosition: -1, SenderID: "ou_a", SenderName: "张三", Content: "发布单合了吗"}
	m.aiP.anchor = &anchor
	m.aiP.input.SetValue("帮我回")
	out, _ := m.askStreamConfirm()
	m = out.(Model)
	require.Contains(t, m.notice, "as a reply to 张三")

	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	started := askStarted(t, ycmd)
	sout, _ := m.onAIStarted(started)
	m = sout.(Model)
	t1 := m.aiP.session().turns[0]
	require.Equal(t, "om_r", t1.stream.replyTo)
	require.True(t, t1.stream.inThread)

	f.ch <- ai.Chunk{Done: true}
	out, cmd := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "回在串里", Done: true}})
	m = out.(Model)
	w, ok := streamWritten(t, cmd)
	require.True(t, ok)
	sout, _ = m.onStreamWritten(w)
	m = sout.(Model)
	var sent larkcli.RawMessage
	for _, x := range c.Messages {
		if x.ParentID == "om_r" {
			sent = x
		}
	}
	require.Equal(t, "oc_quiet", sent.ChatID, "the card went to the chat")
	require.NotEmpty(t, sent.ThreadID, "the reply is inside the thread")
}

// With ai.history on the ask goes out through the history variant, the chip
// says so, and the trace lines a call leaves render dim.
func TestAskAI_HistoryOnTeachesAndTraces(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m.cfg.AI.History = true
	m.deps.ConfigPath = "/Users/linlan/dev.yaml"

	m, t1 := ask(t, m, "再早一点的记录？")
	require.Len(t, f.asks, 1)
	require.Equal(t, ai.History{ChatID: "oc_quiet", ConfigPath: "/Users/linlan/dev.yaml"}, f.asks[0].history,
		"the ask carries the chat and the config path it was taught")
	require.Contains(t, ansi.Strip(m.aiChips(m.rightWidth()-2)), "⌕ history")

	out, _ := m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Trace: "⌕ messages list · 40 rows"}})
	m = out.(Model)
	out, _ = m.onAIChunk(aiChunkMsg{turn: t1.id, chunk: ai.Chunk{Text: "更早还有一条。", Done: true}})
	m = out.(Model)
	m.aiP.rebuild(m)
	pane := ansi.Strip(m.renderAI(m.bodyHeight()))
	require.Contains(t, pane, "⌕ messages list · 40 rows",
		"the call leaves its dim line in the answer")
}

// Off means no history reach at all: the plain stream is what asks.
func TestAskAI_HistoryOffIsThePlainStream(t *testing.T) {
	f := newFakeAI()
	m := aiFixture(t, f)
	m, _ = ask(t, m, "总结一下")
	require.Len(t, f.asks, 1)
	require.Zero(t, f.asks[0].history)
	require.NotContains(t, ansi.Strip(m.aiChips(m.rightWidth()-2)), "history")
}

// A pending y/n owns the mouse like it owns the keys: a click neither
// answers it nor moves the state the verdict was asked about — clicking
// another chat must not re-aim the stream the y is about to post.
func TestConfirm_AClickWhilePendingChangesNothing(t *testing.T) {
	f := newFakeAI()
	m, _ := aiSendFixture(t, f)
	m = press(t, m, "a")
	m.chats = append(m.chats, store.Chat{ChatID: "oc_elsewhere", Name: "别的群", ChatMode: "group"})
	m.layout()
	m.aiP.input.SetValue("帮我回")
	out, _ := m.askStreamConfirm()
	m = out.(Model)
	require.Equal(t, confirmAIStream, m.confirm.kind)

	next, _ := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: 4,
		Y: 1 + headerHeight + rowOf(1)*chatRowStride})
	m = next.(Model)
	require.Equal(t, confirmAIStream, m.confirm.kind, "the question holds the screen")
	require.Empty(t, m.pendingChat, "no chat was opened under it")
	require.NotContains(t, m.notice, "oc_elsewhere")

	// The y then asks what the question described: the chat it named.
	yout, ycmd, _ := m.answerConfirm("y")
	m = yout.(Model)
	started := askStarted(t, ycmd)
	sout, _ := m.onAIStarted(started)
	m = sout.(Model)
	require.Len(t, f.asks, 1)
	t1 := m.aiP.session().turns[0]
	require.Equal(t, "oc_quiet", t1.stream.chatID, "the stream goes to the chat the question was about")
}

// A turn that left before its stream's cancel arrived takes the cancel with
// it: the agent stops here or nothing ever will.
func TestOnAIStarted_ATurnGoneStopsTheAgent(t *testing.T) {
	m := aiFixture(t, newFakeAI())
	m = press(t, m, "a")
	stopped := false
	started := aiStartedMsg{turn: "at_gone", cancel: func() { stopped = true }}
	out, _ := m.onAIStarted(started)
	require.NotNil(t, out)
	require.True(t, stopped)
}

// The stream foot's acts answer where their labels are drawn, the state text
// at the head of the row included.
func TestStreamFoot_ZonesAnswerAtTheirLabels(t *testing.T) {
	t1 := &aiTurn{id: "at_1", state: aiDone, answer: "回好了",
		stream: &aiStreamCard{chatID: "oc_quiet", messageID: "om_c", closed: true}}
	row := streamFoot(t1, 60)
	plain := ansi.Strip(row.text)
	want := map[string]aiActKind{"Jump": actJump, "Copy": actCopy, "Recall": actRecall}
	for label, kind := range want {
		i := strings.Index(plain, label)
		require.GreaterOrEqual(t, i, 0, label+" is drawn")
		// Zones answer in columns; the box-drawing lead is bytes, not columns.
		x := len([]rune(plain[:i]))
		z, ok := zoneAt([]msgRow{row}, 0, x)
		require.True(t, ok, "%s at column %d hits a zone", label, x)
		require.Equal(t, kind, z.act.kind, label+" answers at its own label")
	}
}

// The assistant's box is built after the background was learned, and bubbles'
// own default paints its cursor line black; the box must carry the theme's
// composer styles both at birth and after the background changes.
func TestAIComposer_FollowsBackground(t *testing.T) {
	m := Model{aiP: newAI(false), input: newComposer(true), rightInput: newComposer(true)}
	st := m.aiP.input.Styles()
	require.Equal(t, colDim, st.Focused.Placeholder.GetForeground())
	require.Equal(t, lipgloss.NoColor{}, st.Focused.CursorLine.GetBackground())

	m.setBackground(color.Black, true)
	st = m.aiP.input.Styles()
	require.Equal(t, colDim, st.Focused.Placeholder.GetForeground())
	require.Equal(t, lipgloss.NoColor{}, st.Focused.CursorLine.GetBackground())
}