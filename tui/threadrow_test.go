package tui

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threadFeed is a thread of one chat, with the root and the newest reply the
// row is drawn from.
func threadFeed(mode string, root, last store.Message) store.ThreadFeed {
	return store.ThreadFeed{ThreadID: "omt_x", ChatID: "oc_a", ChatName: "平台组",
		ChatMode: mode, Root: root, Last: last}
}

func spoke(id, sender, name, text string, at int64) store.Message {
	return store.Message{MessageID: id, SenderID: sender, SenderType: "user", SenderName: name,
		MsgType: "text", Content: text, ContentRaw: `{"text":"` + text + `"}`,
		RenderedAt: 1, CreateMs: at}
}

// threadLines renders a thread row the way the pane does and strips the
// styling, which is what the assertions read.
func threadLines(t store.ThreadFeed, w int) (string, string, string) {
	r := renderThreadRow(textAvatars{}, listRow{chat: t.Chat(), thread: t}, "ou_me", gistOf(listRow{chat: t.Chat(), thread: t}, "ou_me", emojiPics{}), testNow, w)
	return ansi.Strip(r.avatarTop), ansi.Strip(r.top), ansi.Strip(r.bottom)
}

func TestRenderThreadRow_TitledByTheRootAndAnsweredByTheNewest(t *testing.T) {
	feed := threadFeed("group",
		spoke("om_root", "ou_a", "张三", "发版流程", 100),
		spoke("om_last", "ou_b", "李四", "收到", 200))

	avatar, top, bottom := threadLines(feed, 38)

	assert.Contains(t, avatar, threadGlyph, "the column says which kind of row this is")
	assert.Contains(t, top, "张三: 发版流程")
	assert.Contains(t, bottom, "李四: 收到")
}

// The clock is the newest reply's: where the thread stands is when it was
// last answered, not when it was opened.
func TestRenderThreadRow_CountsTheUnreadRepliesAndClocksTheLastOne(t *testing.T) {
	feed := threadFeed("group",
		spoke("om_root", "ou_a", "张三", "发版流程", 100),
		spoke("om_last", "ou_b", "李四", "收到", testNow.Add(-2*time.Hour).UnixMilli()))
	feed.Unread = 3

	_, top, _ := threadLines(feed, 38)

	assert.Contains(t, top, "3")
	assert.Contains(t, top, "08:00")
}

// A chat of two names nobody on the summary, the way its own row does.
func TestRenderThreadRow_AChatOfTwoNamesNobody(t *testing.T) {
	feed := threadFeed("p2p",
		spoke("om_root", "ou_a", "张三", "在吗", 100),
		spoke("om_last", "ou_a", "张三", "好的", 200))

	_, _, bottom := threadLines(feed, 38)

	assert.Contains(t, bottom, "好的")
	assert.NotContains(t, bottom, "张三: 好的")
}

func TestRenderThreadRow_TheReadersOwnTurnReadsAsYou(t *testing.T) {
	feed := threadFeed("group",
		spoke("om_root", "ou_a", "张三", "发版流程", 100),
		spoke("om_last", "ou_me", "林岚", "我来", 200))

	_, _, bottom := threadLines(feed, 38)

	assert.Contains(t, bottom, "You: 我来")
}

func TestRenderThreadRow_MarksARepliesThatCallsTheReaderByName(t *testing.T) {
	feed := threadFeed("group",
		spoke("om_root", "ou_a", "张三", "发版流程", 100),
		spoke("om_last", "ou_a", "张三", "@林岚 看下", 200))
	feed.NamesSelf = true

	_, _, bottom := threadLines(feed, 38)

	assert.Contains(t, bottom, "@")
}

// A recall keeps the row where it was and says what happened, the way a
// chat's own summary line does.
func TestRenderThreadRow_ARecalledLastReplySaysSo(t *testing.T) {
	last := spoke("om_last", "ou_b", "李四", "收到", 200)
	last.Deleted = true
	feed := threadFeed("group", spoke("om_root", "ou_a", "张三", "发版流程", 100), last)

	_, _, bottom := threadLines(feed, 38)

	assert.Contains(t, bottom, "李四 recalled a message")
}

func TestRenderThreadRow_AMutedChatKeepsItsMark(t *testing.T) {
	feed := threadFeed("group",
		spoke("om_root", "ou_a", "张三", "发版流程", 100),
		spoke("om_last", "ou_b", "李四", "收到", 200))
	feed.Muted, feed.Unread = true, 2

	_, top, bottom := threadLines(feed, 38)

	assert.Contains(t, top, "2", "silence asks not to be pulled, not not to be told")
	assert.Contains(t, bottom, muteGlyph)
}

// The right edge stays put however long the root's first words are.
func TestRenderThreadRow_TheTitleTakesTheTruncation(t *testing.T) {
	feed := threadFeed("group",
		spoke("om_root", "ou_a", "张三", "发版流程改到周五下午三点，所有人都要到场", 100),
		spoke("om_last", "ou_b", "李四", "收到", testNow.UnixMilli()))

	_, top, _ := threadLines(feed, 38)

	require.Contains(t, top, "…")
	assert.Contains(t, top, "10:00", "the clock is placed first and the title absorbs the rest")
}
