package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frameDraftModel is a model over a real store holding one chat with a thread
// in it, so a box the right column carries can be watched across a frame
// change and a restart.
func frameDraftModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := storetest.OpenSeed(t, filepath.Join(t.TempDir(), "t.db"), "tui.framedraft", seedFrameDraft)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 130, 36
	m.chatID = "oc_group"
	m.layout()
	return m, st
}

func seedFrameDraft(st *store.Store) error {
	ctx := context.Background()
	if err := st.EnsureChat(ctx, "oc_group", 1); err != nil {
		return err
	}
	_, err := st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_root", ChatID: "oc_group", ThreadID: "omt_1", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", ContentRaw: `{"text":"发布计划定了吗"}`,
			CreateMs: 100, UpdateMs: 100},
		{MessageID: "om_reply", ChatID: "oc_group", ThreadID: "omt_1", MsgType: "text",
			SenderID: "ou_b", SenderName: "李四", ContentRaw: `{"text":"周四"}`,
			CreateMs: 200, UpdateMs: 200, MessagePosition: -3},
		{MessageID: "om_other", ChatID: "oc_group", ThreadID: "omt_2", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", ContentRaw: `{"text":"另一个话题"}`,
			CreateMs: 300, UpdateMs: 300},
	}, 1)
	return err
}

// deliver runs a command tree to completion and plays every message it
// produces back into the model, which is what the program does between an
// update and the next frame.
func deliver(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			m = deliver(t, m, c)
		}
	default:
		next, more := m.Update(msg)
		m = next.(Model)
		m = deliver(t, m, more)
	}
	return m
}

func openThread(t *testing.T, m Model, threadID string) Model {
	t.Helper()
	m, cmd := m.openRight(rightFrame{kind: rightThread, id: threadID})
	return deliver(t, m, cmd)
}

func TestSaveComposer_WritesBothBoxes(t *testing.T) {
	t.Parallel()
	m, st := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.input.SetValue("写给会话的")
	m.rightInput.SetValue("写给话题的")

	deliver(t, m, m.saveComposer())

	chat, err := st.LoadDraft(t.Context(), "oc_group", "")
	require.NoError(t, err)
	assert.Equal(t, "写给会话的", chat.Text)
	thread, err := st.LoadDraft(t.Context(), "oc_group", "omt_1")
	require.NoError(t, err)
	assert.Equal(t, "写给话题的", thread.Text)
}

func TestCloseRight_KeepsTheThreadDraft(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.focus = paneThread
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.areap().SetValue("我来看看")

	m = deliver(t, m, m.closeRight())
	require.Empty(t, m.rightInput.Value(), "the column took the box down with it")

	m = openThread(t, m, "omt_1")
	assert.Equal(t, "我来看看", m.rightInput.Value(), "reopening the frame brings its draft back")
}

// The thread row's gist is the frame's own box: a draft typed into the right
// column stands in for the last reply, with no replier's name before it.
func TestThreadRowLine_FrameDraftIsTheGistLine(t *testing.T) {
	t.Parallel()
	row := listRow{chat: store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group"},
		thread: store.ThreadFeed{ThreadID: "omt_1", ChatID: "oc_group", ChatMode: "group",
			Root: spoke("om_root", "ou_a", "张三", "发布流程", 100),
			Last: spoke("om_last", "ou_b", "李四", "周四", 200)}}
	d := store.Draft{ChatID: "oc_group", FrameID: "omt_1", Text: "写给话题的"}
	g := gistOf(row, "ou_me", emojiPics{}).withDraft(d)

	text, _ := threadRowLine(row, d, g, 60)

	stripped := ansi.Strip(text)
	assert.Contains(t, stripped, "写给话题的")
	assert.Contains(t, stripped, draftGlyph)
	assert.NotContains(t, stripped, "李四:", "the frame's box is the reader's, not the last replier's")
}

// A frame draft lands under its thread, not under the chat the thread is in.
func TestDraftSavedMsg_PatchesTheFrameDraftsMap(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)

	next, _ := m.Update(draftSavedMsg{draft: store.Draft{ChatID: "oc_group", FrameID: "omt_1", Text: "写给话题的"}})
	m = next.(Model)
	assert.Equal(t, "写给话题的", m.frameDrafts["omt_1"].Text)

	next, _ = m.Update(draftSavedMsg{draft: store.Draft{ChatID: "oc_group", FrameID: "omt_1"}})
	m = next.(Model)
	assert.NotContains(t, m.frameDrafts, "omt_1", "an empty write is a delete")
}

// The thread frame standing open in the right column draws no draft on its
// row either: the box beside it is the draft.
func TestRenderChats_TheOpenThreadRowStaysBare(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.rightInput.SetValue("写给话题的")
	m = deliver(t, m, m.saveComposer())
	m.chats = []store.Chat{{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastMessageID: "om_other", LastSenderName: "张三", LastContent: "另一个话题", LastRenderedAt: 1}}
	m.threads = []store.ThreadFeed{{ThreadID: "omt_1", ChatID: "oc_group", ChatMode: "group",
		Root: spoke("om_root", "ou_a", "张三", "发布流程", 100),
		Last: spoke("om_last", "ou_b", "李四", "周四", 200)}}

	out := ansi.Strip(m.renderChats(m.chatsBodyHeight()))

	assert.NotContains(t, out, "写给话题的")
	assert.NotContains(t, out, draftGlyph)
	assert.Contains(t, out, "周四", "the thread row keeps its last reply")
}

func TestShowRight_EachFrameGetsItsOwnDraftBack(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.rightInput.SetValue("第一个话题的")

	m = openThread(t, m, "omt_2")
	require.Empty(t, m.rightInput.Value(), "another frame opens on its own empty box")
	m.rightInput.SetValue("第二个话题的")

	m = openThread(t, m, "omt_1")
	assert.Equal(t, "第一个话题的", m.rightInput.Value())
	m = openThread(t, m, "omt_2")
	assert.Equal(t, "第二个话题的", m.rightInput.Value())
}

func TestRightBox_QuoteComesBackWithItsText(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.focus, m.threadIdx = paneThread, indexOfID(m.thread, "om_reply")
	sel, ok := m.selected()
	require.True(t, ok)
	mm, _ := m.startInsert(&sel, false)
	m = mm.(Model)
	m.areap().SetValue("对")

	m = deliver(t, m, m.closeRight())
	m = openThread(t, m, "omt_1")
	require.NotNil(t, m.rightReply)
	assert.Equal(t, "om_reply", m.rightReply.MessageID)
}

// A reload is not an entry: a tick landing a new reply must not put back a
// draft the reader has just cleared.
func TestTakeRightDraft_AReloadLeavesTheBoxAlone(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.rightInput.SetValue("写到一半")
	m = deliver(t, m, m.saveRightBox())
	m.rightInput.SetValue("")

	m = deliver(t, m, m.loadRight())

	assert.Empty(t, m.rightInput.Value(), "what the reader cleared stays cleared")
}

func TestDraftForThreadRow_TheOpenFrameDrawsNoDraft(t *testing.T) {
	t.Parallel()
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.rightInput.SetValue("半句")
	m.frameDrafts = map[string]store.Draft{"omt_2": {ChatID: "oc_group", FrameID: "omt_2", Text: "别处的"}}

	assert.Empty(t, m.draftForThreadRow("omt_1"),
		"the open frame leaves the box beside it to say the draft")
	assert.Equal(t, "别处的", m.draftForThreadRow("omt_2").Text)
	assert.True(t, m.draftForThreadRow("omt_3").Empty())
}
