package tui

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/amzyang/larkim/store"
)

// feedModel is the panel up over the backlog fixture, the way pressing Enter
// on the Unread row leaves it.
func feedModel(t *testing.T) Model {
	t.Helper()
	st, chats := backlog(t)
	m := New(Deps{Store: st, Self: "ou_me",
		ClearBadge: func(_ context.Context, _ store.ChatUnread) error { return nil }})
	m.width, m.height = 120, 40
	m.chats = chats
	m.unread = map[string]int64{"oc_platform": 2, "oc_project": 1}
	m.focused = true
	next, cmd := m.openUnread()
	m = next.(Model)
	m.layout()
	return applyAll(t, m, cmd)
}

// applyAll runs the pages a command answers with back through Update, so a
// test sees the state the panel settles in rather than one it passes through.
// Only the loads are followed: the ticks and the refreshes behind them belong
// to a running app, not to what the panel does with a page.
func applyAll(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for range 8 {
		if cmd == nil {
			return m
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				m = applyAll(t, m, c)
			}
			return m
		}
		switch msg.(type) {
		case unreadFeedLoadedMsg, chatSideMsg, messagesLoadedMsg, chatsLoadedMsg, sectionDotMsg, clearDueMsg, clearFiredMsg:
		case errMsg:
			require.NoError(t, msg.(errMsg).err)
			return m
		default:
			return m
		}
		next, out := m.Update(msg)
		m, cmd = next.(Model), out
	}
	return m
}

func paneText(m Model) string {
	return ansi.Strip(m.renderMessages(m.bodyHeight()))
}

func TestFeed_PartsThePageByChat(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	out := paneText(m)

	require.Contains(t, out, "─ 平台组 · 2 ─", "each chat's stretch opens under a rule naming it and counting what waits")
	require.Contains(t, out, "─ 项目协作群 · 1 ─")
	require.Less(t, strings.Index(out, "平台组"), strings.Index(out, "项目协作群"),
		"the chat that has waited longest opens the page")
	require.Contains(t, out, "接口什么时候好")
	require.Contains(t, out, "发布推迟到周四")
}

func TestFeed_TitleNamesThePanelAndTheChatUnderTheCursor(t *testing.T) {
	t.Parallel()
	m := feedModel(t)

	require.Contains(t, ansi.Strip(m.renderHeader(80)), "Unread · 平台组 · 3 in 2 chats")
	require.Equal(t, "oc_platform", m.chatID, "which is where a reply would go")
}

func TestFeed_ThePinnedRuleNamesTheSectionAtTheTopOfTheViewport(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	line, _ := m.feedRuleLine(60)
	require.Contains(t, ansi.Strip(line), "平台组")

	// Scroll until the second section's messages are what the top row belongs
	// to; the rule has to have followed.
	for m.msgTop < len(m.msgRows)-1 && m.feedChatAt(m.msgRows[m.msgTop].idx) != "oc_project" {
		m.msgTop++
	}
	line, _ = m.feedRuleLine(60)
	require.Contains(t, ansi.Strip(line), "项目协作群")
}

// The rule is pinned above the rows, so the row it stands for is not drawn a
// second time — but it keeps its line, so nothing under it moves.
func TestFeed_TheTopRowIsBlankWhenItIsTheSectionRuleItself(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.msgTop = -1
	for i, r := range m.msgRows {
		if r.rule {
			m.msgTop = i
		}
	}
	require.Positive(t, m.msgTop, "the last section's rule")

	lines := strings.Split(paneText(m), "\n")
	pinned, first := lines[2], lines[3]

	require.Contains(t, pinned, "─ 项目协作群 · 1 ─")
	require.NotContains(t, first, "项目协作群", "the rule is pinned, not drawn twice")
	require.Empty(t, strings.TrimSpace(strings.Trim(first, "│")), "and its line is held open")
}

func TestFeed_BlocksDoNotMergeAcrossASectionBoundary(t *testing.T) {
	t.Parallel()
	st, chats := backlog(t)
	// The same sender, inside the run span, either side of the boundary.
	say(t, st, "om_j2", "oc_project", 250, "同一个人，同一分钟")
	owing(t, st, "om_j2")
	chats[1].UnreadCount = 2

	_, msgs, meta, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)
	feed := &unreadFeed{sections: []unreadSection{
		{chatID: "oc_platform", name: "平台组", anchorMs: 100},
		{chatID: "oc_project", name: "项目协作群", anchorMs: 250},
	}}
	m := sized(120, 40)
	m.meta = meta
	rows := renderFeedRows(msgs, feed, chats, m.msgStyleFor(80, meta))

	out := ansi.Strip(strings.Join(rowTexts(rows), "\n"))
	require.Equal(t, 2, strings.Count(out, "张三"), "a block opens on each side of the rule")
}

// The page runs across chats, so the other half of a chat of two says nothing
// about the messages above and below it.
func TestFeed_TheStyleCarriesNoChatOfTwo(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.chats = append(m.chats, store.Chat{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a"})
	m.chatID = "oc_peer"

	st := m.msgStyleFor(80, m.meta)
	require.False(t, st.p2p)
	require.Empty(t, st.peer)
	require.Nil(t, st.names, "the rule already said which chat this is")
}

func TestFeed_SaysHowManyChatsThePageLeavesOut(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.chats = append(m.chats, store.Chat{ChatID: "oc_late", Name: "后来的", UnreadCount: 4})
	m.rebuildMessages()

	require.Contains(t, paneText(m), "1 more chat waiting")
}

func TestFeed_AnEmptyPanelSaysSo(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.feed.sections, m.msgs, m.msgsBase = nil, nil, nil
	m.chats = nil
	m.rebuildMessages()

	require.Contains(t, paneText(m), "nothing waiting")
}

// rowTexts is what the rows say, for the tests whose subject is the layout
// rather than the terminal. The model places nothing, so every row draws the
// stand-in renderRows already wrote.
func rowTexts(rows []msgRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		line, _ := Model{}.rowLine(r, 80)
		out = append(out, line)
	}
	return out
}

func TestListRows_TheUnreadRowLeadsTheChatsPane(t *testing.T) {
	t.Parallel()
	c := newRowsCache()
	rows := c.all([]store.Chat{chat("oc_1", "平台组")}, nil)

	require.True(t, rows[0].isFeed())
	require.Equal(t, unreadFeedRowID, rows[0].key())
	require.False(t, rows[1].isFeed())
}

func TestNextUnread_SkipsTheUnreadRow(t *testing.T) {
	t.Parallel()
	rows := []listRow{
		{chat: store.Chat{ChatID: unreadFeedRowID}},
		{chat: store.Chat{ChatID: "oc_a"}},
	}
	unread := map[string]int64{"oc_a": 2}

	require.Equal(t, 1, nextUnread(rows, unread, 1, 1), "the queue wraps past it, not onto it")
	require.False(t, waitingFor(rows[0], unread))
}

func TestRenderUnreadRow_CountsTheChatsWaiting(t *testing.T) {
	t.Parallel()
	rows := []listRow{
		{chat: store.Chat{ChatID: unreadFeedRowID}},
		{chat: store.Chat{ChatID: "oc_platform"}},
		{chat: store.Chat{ChatID: "oc_project"}},
		{chat: store.Chat{ChatID: "oc_muted", Muted: true}},
	}
	unread := map[string]int64{"oc_platform": 2, "oc_project": 1, "oc_muted": 9}

	r := renderUnreadRow(textAvatars{}, rows[0], rows, unread, 38)

	require.Contains(t, ansi.Strip(r.top), "Unread")
	require.Contains(t, ansi.Strip(r.bottom), "3 chats waiting",
		"the silenced chat counts here, as it does on the page")
	require.NotContains(t, ansi.Strip(r.top), "12",
		"the messages are badged on the rows below; this row would be counting them twice")
	require.Equal(t, "Unread", strings.TrimSpace(ansi.Strip(r.top)))
}

func TestRenderUnreadRow_SaysSoWhenNothingIsWaiting(t *testing.T) {
	t.Parallel()
	row := listRow{chat: store.Chat{ChatID: unreadFeedRowID}}
	r := renderUnreadRow(textAvatars{}, row, []listRow{row}, nil, 38)
	require.Contains(t, ansi.Strip(r.bottom), "nothing waiting")
}

func TestRenderUnreadRow_TakesItsAvatarFromTheRenderer(t *testing.T) {
	t.Parallel()
	row := listRow{chat: store.Chat{ChatID: unreadFeedRowID}}
	r := renderUnreadRow(textAvatars{}, row, []listRow{row}, nil, 38)

	require.Contains(t, ansi.Strip(r.avatarTop), unreadGlyph)
	require.Equal(t, avatarWidth, lipgloss.Width(r.avatarTop))
	require.Equal(t, avatarWidth, lipgloss.Width(r.avatarBottom))
}

// The row is the way into the panel, so what the cursor stands on is what the
// message pane draws — as it is for every other row of the list. The focus is
// the difference: a press goes to the page, the cursor walking on stays in the
// list so the next j is still the reader's.
func TestOpenRow_TheUnreadRowOpensWithoutTakingFocus(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.feed, m.focus = nil, paneChats
	row := listRow{chat: store.Chat{ChatID: unreadFeedRowID}}

	require.NotNil(t, m.openRow(row, false))
	require.NotNil(t, m.feed)
	require.Equal(t, paneChats, m.focus)

	m.feed = nil
	require.NotNil(t, m.openRow(row, true))
	require.Equal(t, paneMessages, m.focus)
}

// Landing on the row a second time has nothing left to load, which is what
// keeps a held ctrl+u at the top of the list from asking for the page again.
func TestHighlightedRow_TheUnreadRowIsOpenOnceThePanelIs(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.focus, m.chatIdx = paneChats, 0
	require.True(t, m.visibleRows()[0].isFeed())

	_, ok := m.highlightedRow()
	require.False(t, ok, "the panel is up, so there is nothing to spend a page on")

	m.feed = nil
	_, ok = m.highlightedRow()
	require.True(t, ok)
}

// With every section emptied out the page holds only the line counting the
// chats it left out, and that line is not a page.
func TestFeed_AnEmptiedPageStillSaysNothingIsOnIt(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	// gatherUnread answers with a slice, never nil, so that is what a page
	// left with no section to draw actually holds.
	m.msgs, m.msgsBase, m.feed.sections = nil, nil, []unreadSection{}
	m.chats = []store.Chat{{ChatID: "oc_late", Name: "后来的", UnreadCount: 4}}
	m.rebuildMessages()

	out := paneText(m)
	require.Contains(t, out, "All caught up · nothing waiting")
	require.Contains(t, out, "1 more chat waiting")
}

// The Unread row is where the chats pane opens, and it stands for no chat, so
// the y family has nothing to take from it.
func TestYank_TheUnreadRowHasNothingToCopy(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	m.feed = nil
	m.focus, m.chatIdx = paneChats, 0
	require.True(t, m.visibleRows()[0].isFeed())

	src, ok := m.yankSources()
	require.True(t, ok)
	require.Empty(t, src, "yy has no id to put on the clipboard")

	next, cmd := m.copySelection()
	require.Nil(t, cmd, "and Y has no chat to gather context from")
	require.Equal(t, "nothing to copy", next.notice)
}

// The note counts the chats the page leaves out, so it counts over the
// listing the chats pane beside it is drawing.
func TestFeed_TheNoteFollowsTheChatsListing(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	require.NotContains(t, paneText(m), "more chat")

	late := append(slices.Clone(m.chats), store.Chat{ChatID: "oc_late", Name: "后来的", UnreadCount: 1})
	unread := maps.Clone(m.unread)
	unread["oc_late"] = 1
	next, _ := m.Update(chatsLoadedMsg{chats: late, unread: unread})

	require.Contains(t, paneText(next.(Model)), "1 more chat waiting")
}

// The reported defect: the panel goes up over an empty backlog, a chat starts
// waiting, and the page it is showing has to be the page of what is waiting.
func TestFeed_AChatThatStartsWaitingLandsOnThePage(t *testing.T) {
	t.Parallel()
	st := feedStore(t)
	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 120, 40
	m.focused = true
	next, cmd := m.openUnread()
	m = next.(Model)
	m.layout()
	m = applyAll(t, m, cmd)
	require.Contains(t, paneText(m), "All caught up · nothing waiting")

	require.NoError(t, st.EnsureChat(t.Context(), "oc_late", 1))
	say(t, st, "om_l1", "oc_late", 100, "新消息")
	owing(t, st, "om_l1")

	m = applyAll(t, m, m.reloadCurrent())

	out := paneText(m)
	require.Contains(t, out, "新消息")
	require.Contains(t, out, "1 in 1 chats")
	require.NotContains(t, out, "All caught up · nothing waiting")
	require.NotContains(t, out, "more chat waiting")
}

func TestFeedRule_CentresTheChatNameBetweenTheEdgeAndTheButton(t *testing.T) {
	t.Parallel()
	for w := 40; w < 48; w++ { // both remainders of the odd-column split
		plain := ansi.Strip(feedRule("平台组", 0, w).text)

		require.Equal(t, w, ansi.StringWidth(plain), "width %d", w)
		require.True(t, strings.HasPrefix(plain, "─"), "width %d: %q", w, plain)
		arms, ok := strings.CutSuffix(plain, " "+markChatGlyph)
		require.True(t, ok, "width %d: %q", w, plain)
		require.True(t, strings.HasSuffix(arms, "─"), "width %d: %q", w, plain)
		split := strings.Split(arms, " 平台组 ")
		require.Len(t, split, 2, "width %d: %q", w, plain)
		left, right := ansi.StringWidth(split[0]), ansi.StringWidth(split[1])
		require.LessOrEqual(t, left, right, "width %d: the odd column goes to the right arm", w)
		require.LessOrEqual(t, right-left, 1, "width %d: %q", w, plain)
	}
}

// The button is the last thing on the line, so the strip the click lands in has
// to be the last columns of it.
func TestInMarkChat_AnswersForTheStripTheButtonCloses(t *testing.T) {
	t.Parallel()
	const w = 40
	plain := ansi.Strip(feedRule("平台组", 0, w).text)

	require.True(t, strings.HasSuffix(plain, markChatGlyph), "%q", plain)

	for col := range w {
		require.Equal(t, col >= w-markChatWidth, inMarkChat(col, w), "column %d", col)
	}
	require.False(t, inMarkChat(w, w), "past the pane's own edge")
}

// The two rules the page alternates between are the same shape, so the chat
// name is what has to be told from the day under it.
func TestFeedRule_DrawsTheChatNameBrighterThanTheDayRule(t *testing.T) {
	t.Parallel()
	require.NotEqual(t, daySeparator("平台组", 40), feedRule("平台组", 0, 40).text)
}

// The rule names a chat whose row in the list is out of sight, so the number on
// it has to keep up with the badge the reader would have seen there.
func TestFeed_ASectionsRuleFollowsTheChatsBadge(t *testing.T) {
	t.Parallel()
	m := feedModel(t)
	require.Contains(t, paneText(m), "平台组 · 2")

	st := m.deps.Store
	say(t, st, "om_p3", "oc_platform", 400, "又来一条")
	owing(t, st, "om_p3")

	m = applyAll(t, m, m.reloadCurrent())

	out := paneText(m)
	require.Contains(t, out, "平台组 · 3")
	require.NotContains(t, out, "平台组 · 2")
	require.Contains(t, out, "4 in 2 chats", "and the title counts the same messages")
}
