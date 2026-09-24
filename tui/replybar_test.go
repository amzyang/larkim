package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func replying(w, h int, x store.Message, inThread bool) Model {
	m := sized(w, h)
	m.replyTo, m.inThrd = &x, inThread
	m.mode, m.focus = modeInsert, paneInput
	m.layout()
	return m
}

func TestRenderInput_QuotesTheMessageBeingRepliedTo(t *testing.T) {
	m := replying(120, 30, store.Message{MessageID: "om_x", SenderID: "ou_her", SenderName: "林岚",
		Content: "@Announcement hey", RenderedAt: 1}, false)
	out := ansi.Strip(m.renderInput())
	require.Contains(t, out, "林岚", "the quoted sender is named")
	require.Contains(t, out, "@Announcement hey", "the quoted message reads as text, not as an id")
	require.NotContains(t, out, "om_x", "the id is not what a person recognises a message by")
	require.Contains(t, out, "i to write", "the composer keeps its full writing area below the quote")
	require.Equal(t, m.composerHeight()+2, lipgloss.Height(m.renderInput()))
}

func TestRenderInput_ThreadReplyIsMarkedApart(t *testing.T) {
	x := store.Message{MessageID: "om_x", SenderID: "ou_her", SenderName: "林岚", Content: "hey", RenderedAt: 1}
	plain := ansi.Strip(replying(120, 30, x, false).renderInput())
	thread := ansi.Strip(replying(120, 30, x, true).renderInput())
	require.NotEqual(t, plain, thread, "a thread reply is not drawn like a plain one")
	require.Contains(t, thread, "thread")
}

func TestRenderInput_NoQuoteWithoutAReplyTarget(t *testing.T) {
	m := sized(120, 30)
	require.Equal(t, inputHeight, m.composerHeight())
	require.Equal(t, inputHeight+2, lipgloss.Height(m.renderInput()))
}

func TestView_ReplyBarKeepsTheTerminalHeight(t *testing.T) {
	m := replying(120, 30, store.Message{MessageID: "om_x", SenderName: "林岚", Content: "hey", RenderedAt: 1}, false)
	v := m.View()
	require.Equal(t, m.height, lipgloss.Height(v.Content))
	for _, line := range strings.Split(v.Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "no line wider than the terminal: %q", line)
	}
}

func TestHit_ComposerGrowsWithTheReplyBar(t *testing.T) {
	m := replying(120, 30, store.Message{MessageID: "om_x", SenderName: "林岚", Content: "hey", RenderedAt: 1}, false)
	p, _ := m.hit(2, m.bodyHeight()+2)
	require.Equal(t, paneInput, p, "the quote row belongs to the composer")
	p, _ = m.hit(2, m.height-statusHeight-1)
	require.Equal(t, paneInput, p, "the composer still reaches the status bar")
}

func TestReplyGist_NamesWhatHasNoText(t *testing.T) {
	require.Equal(t, "(Recalled)", replyGist(store.Message{Deleted: true, Content: "gone", RenderedAt: 1}))
	require.Equal(t, "hi", replyGist(store.Message{MsgType: "text", ContentRaw: `{"text":"hi"}`}), "an unrendered text still reads")
	require.Equal(t, "[图片]", replyGist(store.Message{MsgType: "image", Content: "[Image: img_abc]", RenderedAt: 1}))
	require.Equal(t, "one two", replyGist(store.Message{MsgType: "text", Content: "one\ntwo", RenderedAt: 1}), "the quote stays on one line")
	require.Equal(t, "docs", replyGist(store.Message{MsgType: "text", Content: "[docs](https://x.example)", RenderedAt: 1}),
		"a link quotes its label, not its target")
}

func TestOnInsertKey_CtrlRDropsTheQuoteAndKeepsTheDraft(t *testing.T) {
	m := replying(120, 30, store.Message{MessageID: "om_x", SenderName: "林岚", Content: "hey", RenderedAt: 1}, true)
	m.input.SetValue("half a sentence")
	out, _ := m.onInsertKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	got := out.(Model)
	require.Nil(t, got.replyTo)
	require.False(t, got.inThrd)
	require.Equal(t, "half a sentence", got.input.Value(), "dropping the quote leaves the draft alone")
	require.Equal(t, modeInsert, got.mode, "writing continues")
}

func TestRenderMessages_MarksTheQuotedMessage(t *testing.T) {
	m := sized(120, 30)
	require.NotContains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), "↩")
	before := len(m.msgRows)
	m.msgIdx = len(m.msgs) - 1
	m.replyTo, m.mode, m.focus = &m.msgs[m.msgIdx], modeInsert, paneInput
	m.layout()
	require.Contains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), "↩",
		"the lead says which message the open draft answers")
	require.Equal(t, before, len(m.msgRows), "aiming the draft adds no row, so the list stays where it was")
}
