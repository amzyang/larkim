package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// threadFrame is a model with a Thread frame standing in the right column: a root
// and one reply, which is the shape frameRoot reads the sign of.
func threadFrame(w, h int) Model {
	m := sized(w, h)
	msgs := []store.Message{
		{MessageID: "om_root", ChatID: "oc_1", ThreadID: "omt_1", SenderName: "张三",
			Content: "下周一发版", RenderedAt: 1, CreateMs: 1000},
		{MessageID: "om_reply", ChatID: "oc_1", ThreadID: "omt_1", SenderName: "李四",
			Content: "收到", RenderedAt: 1, CreateMs: 2000, MessagePosition: -3},
	}
	m.rightKind, m.threadID = rightThread, "omt_1"
	m.thread, m.threadBase = msgs, msgs
	m.layout()
	return m
}

// writeIn enters the box under the focused pane and puts a draft in it.
func writeIn(m Model, text string) Model {
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.areap().SetValue(text)
	m.replan()
	m.layout()
	return m
}

func TestStartInsert_FocusInTheRightColumnWritesInItsOwnBox(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus = paneThread
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, sideRight, m.side)
	require.Equal(t, paneInput, m.focus)

	m.areap().InsertString("收到")
	require.Equal(t, "收到", m.rightInput.Value())
	require.Empty(t, m.input.Value(), "the chat's box never saw the keys")
}

func TestStartInsert_AForwardedBundleLeavesTheKeysInTheChatsBox(t *testing.T) {
	m := threadFrame(130, 30)
	m.rightKind, m.threadID = rightForward, "om_bundle"
	m.layout()
	m.focus = paneThread
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, sideMain, m.side, "the frame has no box, so the chat's is the only one")
}

func TestComposers_HoldSeparateDrafts(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus = paneMessages
	m = writeIn(m, "写给会话的")
	require.Equal(t, sideMain, m.side)

	m.focus = paneThread
	m = writeIn(m, "写给话题的")
	require.Equal(t, sideRight, m.side)

	require.Equal(t, "写给会话的", m.input.Value(), "the chat's draft stayed where it was")
	require.Equal(t, "写给话题的", m.rightInput.Value())

	// And back again: the box is picked up where it was left, caret and all.
	m.focus = paneMessages
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, "写给会话的", m.area().Value())
}

func TestSubmit_ThreadBoxWithNoQuoteAnswersTheFrameRoot(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus = paneThread
	m = writeIn(m, "我来看看")

	mm, _ := m.submit()
	m = mm.(Model)
	require.Len(t, m.outbox, 1)
	it := m.outbox[0]
	require.Equal(t, "om_root", it.replyTo, "the thread is answered through the message it started from")
	require.True(t, it.inThread)
	require.Equal(t, "omt_1", it.threadID)
	require.Equal(t, "oc_1", it.chatID)
	require.Empty(t, m.rightInput.Value(), "the box it was sent from is cleared")
}

func TestSubmit_ThreadBoxAnswersTheMessageRPointedItAt(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus, m.threadIdx = paneThread, 1
	sel, ok := m.selected()
	require.True(t, ok)
	mm, _ := m.startInsert(&sel, false)
	m = mm.(Model)
	m.areap().SetValue("对")
	m.replan()

	mm, _ = m.submit()
	m = mm.(Model)
	require.Equal(t, "om_reply", m.outbox[0].replyTo)
	require.True(t, m.outbox[0].inThread)
}

func TestSubmit_DetailsBoxQuotesIntoTheChatFlow(t *testing.T) {
	m := threadFrame(130, 30)
	m.rightKind, m.threadID = rightReply, "om_root"
	m.layout()
	m.focus = paneThread
	m = writeIn(m, "补充一句")

	mm, _ := m.submit()
	m = mm.(Model)
	it := m.outbox[0]
	require.Equal(t, "om_root", it.replyTo, "a reply tree is answered at the message it grew under")
	require.False(t, it.inThread, "the answer lands in the chat's flow beside it")
	require.Empty(t, it.threadID)
}

func TestLayout_ForcesTheMainBoxWhenTheFrameHasNoComposer(t *testing.T) {
	m := threadFrame(130, 30)
	m.side = sideRight
	m.rightKind, m.threadID = rightForward, "om_bundle"
	m.layout()
	require.Equal(t, sideMain, m.side)

	m.side = sideRight
	m.closeRight()
	require.Equal(t, sideMain, m.side, "a closed column leaves one box")
}

func TestLayout_FoldedThreadForcesTheRightBox(t *testing.T) {
	m := threadFrame(100, 30)
	require.True(t, m.foldRight(), "this width is the interesting one")
	require.Equal(t, sideRight, m.side, "the chat's box is not on screen to write in")

	wide := threadFrame(130, 30)
	require.False(t, wide.foldRight())
	require.Equal(t, sideMain, wide.side)
}

func TestShowRight_DoesNotCarryADraftIntoAnotherThread(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus = paneThread
	m = writeIn(m, "写到一半")
	m.focus = paneThread

	m.showRight(rightFrame{kind: rightThread, id: "omt_2"})
	require.Empty(t, m.rightInput.Value(), "the draft belonged to the frame it was written under")
	require.Nil(t, m.rightReply)
}

func TestOnClick_PressingABoxTakesThatSide(t *testing.T) {
	m := threadFrame(130, 30)
	y := m.bodyHeight() + 3

	mm, _ := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: m.width - 5, Y: y})
	m = mm.(Model)
	require.Equal(t, sideRight, m.side)
	require.Equal(t, modeInsert, m.mode)
	require.Equal(t, paneInput, m.focus)

	mm, _ = m.onClick(tea.Mouse{Button: tea.MouseLeft, X: chatsWidth + 5, Y: y})
	m = mm.(Model)
	require.Equal(t, sideMain, m.side)
}

func TestOnClick_PressingABoxKeepsTheQuoteItCarries(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus, m.threadIdx = paneThread, 1
	sel, ok := m.selected()
	require.True(t, ok)
	mm, _ := m.startInsert(&sel, false)
	m = mm.(Model)

	mm, _ = m.onClick(tea.Mouse{Button: tea.MouseLeft, X: m.width - 5, Y: m.bodyHeight() + 3})
	m = mm.(Model)
	require.NotNil(t, m.rightReply, "the press was about the box, not about what it answers")
	require.Equal(t, "om_reply", m.rightReply.MessageID)
}

func TestBandAt_NamesTheBoxUnderEachColumn(t *testing.T) {
	m := threadFrame(130, 30)
	_, ok := m.bandAt(2)
	require.False(t, ok, "the chats pane has no box")
	s, ok := m.bandAt(chatsWidth + 2)
	require.True(t, ok)
	require.Equal(t, sideMain, s)
	s, ok = m.bandAt(m.width - 2)
	require.True(t, ok)
	require.Equal(t, sideRight, s)

	m.rightKind = rightForward
	m.layout()
	_, ok = m.bandAt(m.width - 2)
	require.False(t, ok, "a forwarded bundle is read, not answered")
}

func TestCmdSide_TheCommandLineStandsInTheProgramsOwnBox(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus = paneThread
	m = writeIn(m, "话题里写了一半")
	require.Equal(t, sideRight, m.side)
	require.Equal(t, sideMain, m.cmdSide(), "the : line speaks for the program, not for the thread")

	m = press(t, m, "esc", ":", "l", "s")
	v := m.View()
	require.Contains(t, ansi.Strip(m.renderInput(sideMain)), ":ls")
	require.Contains(t, ansi.Strip(m.renderInput(sideRight)), "话题里写了一半",
		"the thread's draft stays on screen beside it")
	require.Equal(t, chatsWidth+1+lipgloss.Width(":ls"), v.Cursor.X)

	folded := threadFrame(100, 30)
	require.Equal(t, sideRight, folded.cmdSide(), "folded, the column's box is the only one there is")
}

func TestRenderInput_TheBoxWithoutTheKeysDrawsItsOwnDraft(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus = paneMessages
	m = writeIn(m, "写给会话的")
	m.rightInput.SetValue("写给话题的")
	m.layout()

	right := ansi.Strip(m.renderInput(sideRight))
	require.Contains(t, right, "写给话题的")
	require.NotContains(t, right, composerHint, "the send hint belongs to the box being typed in")
	require.Equal(t, m.composerHeight()+2, lipgloss.Height(right), "both boxes are one height")
	require.Equal(t, m.rightWidth(), lipgloss.Width(right))
}

func TestComposerHeight_HoldsStillWhenTheKeysCrossSides(t *testing.T) {
	m := threadFrame(130, 30)
	m.focus, m.msgIdx = paneMessages, 0
	sel, ok := m.selected()
	require.True(t, ok)
	mm, _ := m.startInsert(&sel, false)
	m = mm.(Model)
	require.Equal(t, sideMain, m.side)
	quoted, body := m.composerHeight(), m.bodyHeight()

	// The Thread frame answers without quoting anything, so before the quote
	// row was claimed in every state this handoff shortened the band.
	m.focus = paneThread
	mm, _ = m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, sideRight, m.side)
	require.Equal(t, quoted, m.composerHeight(), "the band holds its height across the handoff")
	require.Equal(t, body, m.bodyHeight(), "so the panes above it do not move")
}

func TestComposerHeight_IsTheRestingOneWhicheverBoxHasTheKeys(t *testing.T) {
	m := threadFrame(130, 30)
	require.Equal(t, restingComposer, m.composerHeight())

	m.focus = paneThread
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, restingComposer, m.composerHeight())

	m.rightKind, m.threadID = rightReply, "om_root"
	m.layout()
	require.Equal(t, restingComposer, m.composerHeight(), "a Details frame claims the same rows")
}

func TestOnInsertKey_CtrlRDropsTheQuoteInADetailsFrame(t *testing.T) {
	m := threadFrame(130, 30)
	m.rightKind, m.threadID = rightReply, "om_root"
	m.layout()
	m.focus, m.threadIdx = paneThread, 1
	sel, ok := m.selected()
	require.True(t, ok)
	mm, _ := m.startInsert(&sel, false)
	m = mm.(Model)
	m.areap().SetValue("写到一半")
	require.NotNil(t, m.rightReply)

	out, _ := m.onInsertKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = out.(Model)
	require.Nil(t, m.rightReply)
	_, ok = m.quotedOn(sideRight)
	require.False(t, ok, "the bar the key names is gone")
	require.NotContains(t, ansi.Strip(m.renderInput(sideRight)), replyBarHint)
	require.Equal(t, "写到一半", m.rightInput.Value(), "dropping the quote leaves the draft alone")

	x, ok := m.rightTarget()
	require.True(t, ok)
	require.Equal(t, "om_root", x.MessageID, "the answer still lands where the frame grew from")
}

func TestQuotedOn_ADetailsFrameQuotesNothingUntilAsked(t *testing.T) {
	m := threadFrame(130, 30)
	m.rightKind, m.threadID = rightReply, "om_root"
	m.layout()

	_, ok := m.quotedOn(sideRight)
	require.False(t, ok, "nothing has been aimed at yet")
	require.Contains(t, m.rightInput.Placeholder, "张三", "the box names where an answer would land")

	x, ok := m.rightTarget()
	require.True(t, ok)
	require.Equal(t, "om_root", x.MessageID)
}
