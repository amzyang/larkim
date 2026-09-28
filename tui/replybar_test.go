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
	out := ansi.Strip(m.renderInput(sideMain))
	require.Contains(t, out, "林岚", "the quoted sender is named")
	require.Contains(t, out, "@Announcement hey", "the quoted message reads as text, not as an id")
	require.NotContains(t, out, "om_x", "the id is not what a person recognises a message by")
	require.Equal(t, inputHeight, m.composerRows().input, "the quote does not eat into the writing area")
	require.Equal(t, m.composerHeight()+2, lipgloss.Height(m.renderInput(sideMain)))
}

func TestRenderInput_ThreadReplyIsMarkedApart(t *testing.T) {
	x := store.Message{MessageID: "om_x", SenderID: "ou_her", SenderName: "林岚", Content: "hey", RenderedAt: 1}
	plain := ansi.Strip(replying(120, 30, x, false).renderInput(sideMain))
	thread := ansi.Strip(replying(120, 30, x, true).renderInput(sideMain))
	require.NotEqual(t, plain, thread, "a thread reply is not drawn like a plain one")
	require.Contains(t, thread, "thread")
}

func TestRenderInput_NoQuoteWithoutAReplyTarget(t *testing.T) {
	m := sized(120, 30)
	require.Equal(t, restingComposer, m.composerHeight(), "the writing area and the badge row")
	require.Equal(t, restingComposer+2, lipgloss.Height(m.renderInput(sideMain)))
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
	p, _ := m.hit(chatsWidth+2, m.bodyHeight()+2)
	require.Equal(t, paneInput, p, "the quote row belongs to the composer")
	p, _ = m.hit(chatsWidth+2, m.height-statusHeight-1)
	require.Equal(t, paneInput, p, "the composer still reaches the status bar")
}

func TestReplyGist_NamesWhatHasNoText(t *testing.T) {
	require.Equal(t, "(Recalled)", replyGist(store.Message{Deleted: true, Content: "gone", RenderedAt: 1}))
	require.Equal(t, "hi", replyGist(store.Message{MsgType: "text", ContentRaw: `{"text":"hi"}`}), "an unrendered text still reads")
	require.Equal(t, "[Image]", replyGist(store.Message{MsgType: "image", Content: "[Image: img_abc]", RenderedAt: 1}))
	require.Equal(t, "one two", replyGist(store.Message{MsgType: "text", Content: "one\ntwo", RenderedAt: 1}), "the quote stays on one line")
	require.Equal(t, "docs", replyGist(store.Message{MsgType: "text", Content: "[docs](https://x.example)", RenderedAt: 1}),
		"a link quotes its label, not its target")

	card := weeklyCard
	card.Content = "<card title=\"旧渲染\">\n待认领账号：13 个\n</card>"
	require.Equal(t, "设备版本周报 「兜底」", replyGist(card), "a card quotes what it is called")
}

func TestReplyGist_PostKeepsThePictureItPlaced(t *testing.T) {
	x := store.Message{MsgType: "post", Content: "a\n![Image](img_a)\nb\n\nd", RenderedAt: 1}
	require.Equal(t, "a [Image] b d", replyGist(x), "the quote holds every element the post does")
}

func TestReplyGist_PostNamesItsClipAndItsFile(t *testing.T) {
	clip := store.Message{MsgType: "post", Content: "看这个 [Media: file_b]", RenderedAt: 1}
	require.Equal(t, "看这个 [Video]", replyGist(clip))

	file := store.Message{MsgType: "post", Content: "周报\n<file key=\"file_b\" name=\"report.pdf\"/>", RenderedAt: 1}
	require.Equal(t, "周报 [File] report.pdf", replyGist(file))
}

func TestReplyGist_SpellsTheMentionsAPostCarries(t *testing.T) {
	x := store.Message{MsgType: "post", Content: `<at user_id="ou_a">张三</at> 看下`, RenderedAt: 1}
	require.Equal(t, "@张三 看下", replyGist(x), "a quote is dim as a whole, so the tag has to read as the name")
}

func TestReplyGist_UnrenderedFileIsNamedByItsFile(t *testing.T) {
	x := store.Message{MsgType: "file", ContentRaw: `{"file_key":"file_b","file_name":"report.pdf"}`}
	require.Equal(t, "[File] report.pdf", replyGist(x),
		"the file is in the body Feishu sent, so a forwarded child names it too")
}

func TestReplyGist_ReadsACardLarkCliNeverRendered(t *testing.T) {
	pending := weeklyCard
	pending.Content, pending.RenderedAt = "", 0
	require.Equal(t, "设备版本周报 「兜底」", replyGist(pending),
		"a card carries its own words, so waiting on a rendering says nothing")

	shapeless := pending
	shapeless.ContentRaw = `{"json_card":"{}"}`
	require.Equal(t, "[Card]", replyGist(shapeless), "a card with nothing in it is named by its type")
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
