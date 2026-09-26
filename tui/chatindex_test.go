package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func chat(id, name string) store.Chat { return store.Chat{ChatID: id, Name: name, ChatMode: "group"} }

func TestChatIndex_MatchesPinyinInitials(t *testing.T) {
	ix := newChatIndex()
	_, ok := ix.match(chat("oc_a", "平台组"), "ptz")
	require.True(t, ok)
	_, ok = ix.match(chat("oc_a", "平台组"), "pingtai")
	require.True(t, ok, "full pinyin reaches it too")
	_, ok = ix.match(chat("oc_a", "平台组"), "xyz")
	require.False(t, ok)
}

func TestChatIndex_MatchesTheNameItself(t *testing.T) {
	ix := newChatIndex()
	pos, ok := ix.match(chat("oc_a", "项目协作群"), "协作")
	require.True(t, ok)
	require.Equal(t, []int{2, 3}, pos, "the runes the query landed on, for underlining")
}

func TestChatIndex_MatchesChatIDBySubstringNotFuzzily(t *testing.T) {
	ix := newChatIndex()
	_, ok := ix.match(chat("oc_quiet", "平台组"), "quiet")
	require.True(t, ok, "an id is reached by the piece of it one remembers")
	_, ok = ix.match(chat("oc_quiet", "平台组"), "ocqt")
	require.False(t, ok, "fuzzy over a hex id would match almost anything")
}

func TestChatIndex_PositionsEmptyOnPinyinHit(t *testing.T) {
	ix := newChatIndex()
	// Underlining name runes at pinyin offsets would point at the wrong
	// characters, so a pinyin hit marks nothing.
	pos, ok := ix.match(chat("oc_a", "平台组"), "ptz")
	require.True(t, ok)
	require.Empty(t, pos)
}

func TestChatIndex_RespellsOnRename(t *testing.T) {
	ix := newChatIndex()
	_, ok := ix.match(chat("oc_a", "平台组"), "ptz")
	require.True(t, ok)
	_, ok = ix.match(chat("oc_a", "财务组"), "ptz")
	require.False(t, ok, "the terms are memoized on the name, not the id alone")
	_, ok = ix.match(chat("oc_a", "财务组"), "cwz")
	require.True(t, ok)
}

func TestChatIndex_EmptyQueryTakesEverything(t *testing.T) {
	ix := newChatIndex()
	_, ok := ix.match(chat("oc_a", "平台组"), "")
	require.True(t, ok)
}

func TestVisibleChats_KeepsListOrderUnderFilter(t *testing.T) {
	m := New(Deps{Self: "ou_me"})
	// 抖音 scores worse than 抖 alone would, so a score-ranked list would put
	// the second row first. The list is the reader's own recency order and
	// must not reshuffle under their hand.
	m.chats = []store.Chat{
		chat("oc_1", "抖音自动化测试"),
		chat("oc_2", "抖音"),
		chat("oc_3", "平台组"),
	}
	m.chatFilter = "dy"
	vis := m.visibleRows()
	require.Equal(t, []string{"oc_1", "oc_2"}, []string{vis[0].chatID(), vis[1].chatID()})
	require.Len(t, vis, 2)
}

func TestVisibleRows_UnfilteredIsTheWholeList(t *testing.T) {
	m := New(Deps{Self: "ou_me"})
	m.chats = []store.Chat{chat("oc_1", "平台组"), chat("oc_2", "财务组")}
	require.Len(t, m.visibleRows(), 2)
}

func TestRenderChatRow_UnderlinesTheRunesTheFilterLandedOn(t *testing.T) {
	ix := newChatIndex()
	c := chat("oc_a", "项目协作群")
	pos, ok := ix.match(c, "协作")
	require.True(t, ok)

	row := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, "ou_me", testNow, 40, emojiPics{}, pos)
	require.Equal(t, "项目协作群", ansi.Strip(row.top)[:len("项目协作群")])
	// Each rune opens with the attributes it is drawn under, so the matched
	// ones carry the underline parameter and their neighbours do not.
	require.Regexp(t, `4m协`, row.top, "the matched runes carry the underline")
	require.Regexp(t, `4m作`, row.top)
	require.NotRegexp(t, `4m项`, row.top, "the rest of the name does not")
	require.NotRegexp(t, `4m群`, row.top)
}

func TestRenderChatRow_LeavesTheNameAloneOnAPinyinHit(t *testing.T) {
	ix := newChatIndex()
	c := chat("oc_a", "平台组")
	pos, ok := ix.match(c, "ptz")
	require.True(t, ok)

	row := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, "ou_me", testNow, 40, emojiPics{}, pos)
	require.NotRegexp(t, `4m[平台组]`, row.top)
}

func TestChatIndex_ChineseQueryMatchesARun(t *testing.T) {
	ix := newChatIndex()
	_, ok := ix.match(chat("oc_a", "部门年会邀请函"), "年会")
	require.True(t, ok)
	_, ok = ix.match(chat("oc_b", "2026年高途必赢新春启动会"), "年会")
	require.False(t, ok, "年会 is a word, not a 年 followed eventually by a 会")
}
