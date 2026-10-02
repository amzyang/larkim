package tui

import (
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	uv "github.com/charmbracelet/ultraviolet"
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
	pics := chipPics(t, "THUMBSUP")

	first := g.at(r, "ou_me", pics)
	require.NotEmpty(t, first.chips, "the reaction earns the row a badge")
	second := g.at(r, "ou_me", pics)
	require.Same(t, &first.chips[0], &second.chips[0], "the second ask is answered from the frame")
}

func TestGistCache_HoldsTheSummariesWhileTheInterleaveStands(t *testing.T) {
	g := newGistCache()
	chats := []store.Chat{reactedP2P("THUMBSUP")}
	rows := listRows(chats, nil)
	pics := chipPics(t, "THUMBSUP")

	g.hold(rows)
	first := g.at(rows[0], "ou_me", pics)
	g.hold(rows)
	held := g.at(rows[0], "ou_me", pics)
	require.Same(t, &first.chips[0], &held.chips[0], "a key that reloaded nothing leaves the lines alone")

	g.hold(listRows(chats, nil))
	require.Empty(t, g.rows, "a reload builds its own interleave, so nothing summarised stands")
	again := g.at(rows[0], "ou_me", pics)
	require.NotSame(t, &first.chips[0], &again.chips[0], "the row is summarised afresh")
	require.Equal(t, first, again, "to the same line")
}

// The interleave is what the summaries are keyed on, so a list that reaches
// them only through it — the threads — has to reach them through Update too.
func TestUpdate_ThreadsArrivingAnewDropTheSummaries(t *testing.T) {
	dir := t.TempDir()
	writeTestEmoji(t, dir, "THUMBSUP")
	m := sized(100, 30)
	m.deps.DataDir = dir
	m.pics = picturesIn(dir)
	m.chats = []store.Chat{reactedP2P("THUMBSUP")}
	row := listRow{chat: m.chats[0]}

	next, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = next.(Model)
	first := m.gists.at(row, m.deps.Self, m.chatPics())

	next, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = next.(Model)
	held := m.gists.at(row, m.deps.Self, m.chatPics())
	require.Same(t, &first.chips[0], &held.chips[0], "a second key reloads neither list")

	m.threads = []store.ThreadFeed{{ThreadID: "omt_x", ChatID: m.chats[0].ChatID}}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = next.(Model)
	again := m.gists.at(row, m.deps.Self, m.chatPics())
	require.NotSame(t, &first.chips[0], &again.chips[0], "the threads took the summaries with them")
}

func TestGistCache_DropsTheSummariesWhenTheCellGridMoves(t *testing.T) {
	dir := t.TempDir()
	writeTestEmoji(t, dir, getKey)
	m := sized(100, 30)
	m.deps.DataDir = dir
	m.pics = picturesIn(dir)
	m.chats = []store.Chat{reactedP2P(getKey)}

	m.gists.hold(m.rows.all(m.chats, m.threads))
	m.gists.at(listRow{chat: m.chats[0]}, m.deps.Self, m.chatPics())
	require.Len(t, m.gists.rows, 1)

	next, _ := m.update(uv.CellSizeEvent{Width: 9, Height: 19})
	m = next.(Model)
	require.Empty(t, m.gists.rows, "a badge cut for the old cells is not the one this grid draws")
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

func TestRowGist_WithDraft_StandsInForTheSummary(t *testing.T) {
	g := gistOf(listRow{chat: reactedP2P("THUMBSUP")}, "ou_me", chipPics(t, "THUMBSUP"))

	over := g.withDraft(store.Draft{Text: "半句话"})
	require.Contains(t, over.text, "半句话")
	require.Equal(t, g.chips, over.chips, "the reactions stay with the row")
	require.Nil(t, over.summary, "draft text is characters alone, so the pane draws it as one string")

	require.Equal(t, g, g.withDraft(store.Draft{Text: "  "}), "blanks are not a draft")
}

// A draft is applied after the cache, never through it: the same row draws
// two drafts in a row while the cache answers every ask with one summary.
func TestGistCache_DraftDoesNotEnterTheCache(t *testing.T) {
	g := newGistCache()
	r := listRow{chat: store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastSenderName: "张三", LastContent: "发布计划定了吗", LastRenderedAt: 1}}
	cached := g.at(r, "ou_me", emojiPics{})

	require.Contains(t, g.at(r, "ou_me", emojiPics{}).withDraft(store.Draft{Text: "一"}).text, "一")
	require.Contains(t, g.at(r, "ou_me", emojiPics{}).withDraft(store.Draft{Text: "二"}).text, "二")
	require.Equal(t, cached, g.at(r, "ou_me", emojiPics{}), "the cache still holds the message's line")
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

	clear(m.gists.rows)
	require.NotEmpty(t, m.picturePrepare(), "the reaction's picture is sent")

	// Summarised afresh, so the two walks are compared on their own reading of
	// the row rather than on one answer handed to both.
	clear(m.gists.rows)
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
