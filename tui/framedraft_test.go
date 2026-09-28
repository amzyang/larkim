package tui

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frameDraftModel is a model over a real store holding one chat with a thread
// in it, so a box the right column carries can be watched across a frame
// change and a restart.
func frameDraftModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.EnsureChat(ctx, "oc_group", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
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
	require.NoError(t, err)

	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 130, 36
	m.chatID = "oc_group"
	m.layout()
	return m, st
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

func TestShowRight_EachFrameGetsItsOwnDraftBack(t *testing.T) {
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
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.rightInput.SetValue("写到一半")
	m = deliver(t, m, m.saveRightBox())
	m.rightInput.SetValue("")

	m = deliver(t, m, m.loadRight())

	assert.Empty(t, m.rightInput.Value(), "what the reader cleared stays cleared")
}

func TestDraftForThreadRow_ReadsTheLiveRightBox(t *testing.T) {
	m, _ := frameDraftModel(t)
	m = openThread(t, m, "omt_1")
	m.rightInput.SetValue("半句")
	m.frameDrafts = map[string]store.Draft{"omt_2": {ChatID: "oc_group", FrameID: "omt_2", Text: "别处的"}}

	assert.Equal(t, "半句", m.draftForThreadRow("omt_1").Text,
		"the open frame answers from the box being typed into, not from the map")
	assert.Equal(t, "别处的", m.draftForThreadRow("omt_2").Text)
	assert.True(t, m.draftForThreadRow("omt_3").Empty())
}
