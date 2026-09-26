package tui

import (
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// gistOf summarises one row the way a frame does, for the tests that render a
// row on its own rather than through the pane.
func gistOf(r listRow, self string, pics emojiPics) rowGist {
	return newGistCache().at(r, self, pics)
}

func TestGistCache_SummarisesEachRowOnce(t *testing.T) {
	g := newGistCache()
	r := listRow{chat: reactedP2P("THUMBSUP")}

	first := g.at(r, "ou_me", emojiPics{})
	require.NotEmpty(t, first.chips, "the reaction earns the row a badge")
	second := g.at(r, "ou_me", emojiPics{})
	require.Same(t, &first.chips[0], &second.chips[0], "the second ask is answered from the frame")
}

func TestGistCache_BeginDropsTheFrameBefore(t *testing.T) {
	g := newGistCache()
	r := listRow{chat: reactedP2P("THUMBSUP")}
	first := g.at(r, "ou_me", emojiPics{})

	g.begin()
	require.Empty(t, g.rows)
	again := g.at(r, "ou_me", emojiPics{})
	require.NotSame(t, &first.chips[0], &again.chips[0], "a new frame summarises the row again")
	require.Equal(t, first, again, "to the same line")
}

func TestGistCache_AThreadTakesItsOwnRepliesAndNoReactions(t *testing.T) {
	g := newGistCache()
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group",
		LastReactionsJSON: `[{"emoji_type":"OK","operators":[{"operator_id":"ou_a"}]}]`}
	tf := store.ThreadFeed{ThreadID: "omt_x", ChatID: c.ChatID, ChatMode: c.ChatMode,
		Root: spoke("om_root", "ou_a", "张三", "发版流程", 100),
		Last: spoke("om_last", "ou_a", "张三", "最后一条", 200)}

	v := g.at(listRow{chat: c, thread: tf}, "ou_me", emojiPics{})

	require.Empty(t, v.chips, "a reaction belongs to the chat's row, not the thread's")
	require.Contains(t, v.text, "张三")
	require.Contains(t, v.text, "最后一条")
}

// The pane can only draw a picture the terminal was handed first, so the two
// walks over the chat list have to claim and draw the same set.
func TestPicturePrepare_ClaimsThePicturesTheRowsDraw(t *testing.T) {
	dir := t.TempDir()
	writeTestEmoji(t, dir, getKey)
	m := sized(100, 30)
	m.deps.DataDir = dir
	m.pics = picturesIn(dir)
	first := reactedP2P(getKey)
	first.ChatID = "oc_0"
	m.chats = append([]store.Chat{first}, m.chats[1:]...)

	m.gists.begin()
	require.NotEmpty(t, m.picturePrepare(), "the reaction's picture is sent")

	m.gists.begin()
	g := m.gists.at(listRow{chat: m.chats[0]}, m.deps.Self, m.chatPics())
	drawn := 0
	for _, s := range append(slices.Clone(g.chips), g.summary...) {
		if s.pic.cols == 0 {
			continue
		}
		drawn++
		require.Contains(t, m.pics.id, s.pic.key(), "a picture the row draws was claimed")
	}
	require.NotZero(t, drawn, "the row draws at least one")
}

// BenchmarkChatListFrame is one keystroke's worth of work on a chat list of
// summarised rows: Update claims the pictures, then View draws the lines.
func BenchmarkChatListFrame(b *testing.B) {
	m := sized(100, 40)
	for i := range m.chats {
		m.chats[i].LastMessageID = fmt.Sprintf("om_%d", i)
		m.chats[i].LastRenderedAt = 1
		m.chats[i].LastSenderID, m.chats[i].LastSenderName = "ou_a", "张三"
		m.chats[i].LastContent = "@林岚 明天上午的排期看下，辛苦"
		m.chats[i].LastContentRaw = `{"text":"@林岚 明天上午的排期看下，辛苦"}`
		m.chats[i].LastMentionsJSON = `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`
		m.chats[i].LastReactionsJSON = `[{"emoji_type":"THUMBSUP","operators":[{"operator_id":"ou_a"}]}]`
	}

	b.ReportAllocs()
	for b.Loop() {
		next, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		next.View()
	}
}
