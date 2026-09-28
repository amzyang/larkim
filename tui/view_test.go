package tui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

// rowOf is where the nth chat sits in the chats pane. The Unread row stands
// ahead of every chat, so a row index is never a chat index.
func rowOf(n int) int { return n + 1 }

func sized(w, h int) Model {
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = w, h
	for i := range 80 {
		name := fmt.Sprintf("群 %d 【语言】示例问题及需求沟通群很长很长的名字 %d", i, i)
		if i%3 == 0 {
			name = "line1\nline2 " + name
		}
		m.chats = append(m.chats, store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: name, ChatMode: "group"})
	}
	m.chatID = "oc_1"
	for i := range 40 {
		m.msgsBase = append(m.msgsBase, store.Message{MessageID: fmt.Sprintf("om_%d", i), ChatID: "oc_1", SenderName: "林岚", SenderID: "ou_me",
			Content: strings.Repeat("内容很长 content ", 12), RenderedAt: 1, CreateMs: int64(i) * 60_000})
	}
	m.applyOutbox()
	m.layout()
	return m
}

func withThread(m Model) Model {
	m.rightKind, m.threadID, m.thread = rightThread, "omt_1", m.msgs[:3]
	m.layout()
	return m
}

func TestView_PanesShareHeight(t *testing.T) {
	m := sized(120, 40)
	h := m.bodyHeight()
	require.Equal(t, h+2, lipgloss.Height(m.renderMessages(h)), "messages pane = body + border")
	m = withThread(m)
	require.Equal(t, h+2, lipgloss.Height(m.renderThread(h)))
	// The chats pane has a column of its own, so it takes the rows the box
	// beside it takes as well.
	require.Equal(t, m.height-statusHeight, lipgloss.Height(m.renderChats(m.chatsBodyHeight())),
		"chats pane = the screen less the status bar")
	v := m.View()
	require.Equal(t, m.height, lipgloss.Height(v.Content), "whole view fits the terminal exactly")
	for _, line := range strings.Split(v.Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "no line wider than the terminal: %q", line)
	}
}

func TestChatsPane_RunsPastTheComposerBand(t *testing.T) {
	m := sized(120, 40)
	require.Equal(t, m.bodyHeight()+m.composerHeight()+2, m.chatsBodyHeight(),
		"the chats pane takes the body and the box beside it")
	tall := m.chatsBodyHeight()

	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue(strings.Repeat("一行\n", composerMaxRows))
	m.replan()
	m.layout()
	require.Greater(t, m.composerHeight(), restingComposer, "the box grew with the draft")
	require.Equal(t, composerMaxRows, m.composerRows().input, "and stopped where the panes need the rows more")
	require.Equal(t, tall, m.chatsBodyHeight(), "and the chats pane did not move")
	require.Equal(t, m.height-statusHeight, lipgloss.Height(m.renderChats(m.chatsBodyHeight())))
}

func TestHit_ChatsPaneAnswersBesideTheComposer(t *testing.T) {
	m := sized(120, 30)
	for _, y := range []int{m.bodyHeight() + 2, m.chatsBodyHeight()} {
		p, row := m.hit(2, y)
		require.Equal(t, paneChats, p, "row %d is still the chats pane", y)
		require.GreaterOrEqual(t, row, 0)
		p, _ = m.hit(chatsWidth+2, y)
		require.Equal(t, paneInput, p, "row %d is the box beside it", y)
	}
	p, _ := m.hit(2, m.chatsBodyHeight()+1)
	require.Equal(t, pane(-1), p, "the pane's bottom border belongs to nothing")
}

func TestCursorAt_SitsInsideTheNarrowedBand(t *testing.T) {
	m := sized(120, 30)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("hi")
	m.replan()
	m.layout()
	c := m.View().Cursor
	require.NotNil(t, c)
	require.Equal(t, chatsWidth+1+lipgloss.Width("hi"), c.X, "the caret is past the chats column")
	require.Less(t, c.X, m.width)
}

func TestRenderChats_OneRowPerChat(t *testing.T) {
	m := sized(120, 30)
	h := m.chatsBodyHeight()
	lines := strings.Split(m.renderChats(h), "\n")
	require.Len(t, lines, h+2)
	require.Contains(t, lines[2], "Unread", "the Unread row leads the pane")
	require.Contains(t, lines[5], "line1 line2", "multi-line names are flattened onto one row")
}

func TestScrollTo_KeepsSelectionVisible(t *testing.T) {
	m := sized(120, 20)
	m.msgs = append(m.msgs, store.Message{MessageID: "om_last", ChatID: "oc_1", SenderName: "林岚", Content: "LASTLINE", RenderedAt: 1, CreateMs: 99 * 60_000})
	m.layout()
	m.msgIdx = len(m.msgs) - 1
	m.scrollMessagesToSelection()
	require.Contains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), "LASTLINE", "the selected message's last row is on screen")
	m.msgIdx = 0
	m.scrollMessagesToSelection()
	require.Zero(t, m.msgTop)
}

func TestMove_LastChatStaysVisibleAfterG(t *testing.T) {
	m := sized(120, 36)
	m.focus = paneChats
	mm, _ := m.move(1 << 30)
	m = mm.(Model)
	require.Equal(t, rowOf(len(m.chats)-1), m.chatIdx)
	require.Contains(t, ansi.Strip(m.renderChats(m.chatsBodyHeight())), "群 79 ", "the selected last chat is rendered")
}

func TestHit_MapsPanesBelowTitles(t *testing.T) {
	m := sized(120, 30)
	p, row := m.hit(2, 2)
	require.Equal(t, paneChats, p)
	require.Equal(t, 0, row, "the first chat starts on the line below the title")
	_, row = m.hit(2, 3)
	require.Equal(t, 0, row, "its second line maps to the same chat")
	_, row = m.hit(2, 4)
	require.Equal(t, 0, row, "so does the blank line that holds off the next chat")
	_, row = m.hit(2, 5)
	require.Equal(t, 1, row, "the next chat starts a stride on")
	p, row = m.hit(chatsWidth+5, 3)
	require.Equal(t, paneMessages, p)
	require.Equal(t, 0, row, "message body rows start below the rule, not below the title")
	_, row = m.hit(chatsWidth+5, 4)
	require.Equal(t, 1, row, "and step one at a time from there")
	m = withThread(m)
	p, row = m.hit(m.width-5, 3)
	require.Equal(t, paneThread, p)
	require.Equal(t, 1, row, "thread rows start below the title")
	p, _ = m.hit(chatsWidth+5, m.bodyHeight()+3)
	require.Equal(t, paneInput, p)
	p, row = m.hit(2, m.bodyHeight()+3)
	require.Equal(t, paneChats, p, "the chats pane goes on beside the box")
	require.Equal(t, (m.bodyHeight()+2-headerHeight)/chatRowStride, row)
}

func TestHighlight_SurvivesInnerResets(t *testing.T) {
	m := sized(120, 30)
	line := stDim.Render("12:00") + " sender " + stBold.Render("om_1")
	out := m.highlight(line, true)
	bg := ansi.Style{}.BackgroundColor(m.th.sel.GetBackground()).String()
	require.GreaterOrEqual(t, strings.Count(out, bg), 3, "background re-applied after every embedded style: %q", out)
}

func TestOnFilterKey_EnterOpensHighlightedChat(t *testing.T) {
	m := sized(120, 36)
	m.mode, m.chatFilter, m.chatIdx = modeFilter, "群 70 ", 0
	mm, _ := m.onFilterKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)
	require.Equal(t, "oc_70", m.pendingChat, "the page is on its way; the panes change over when it lands")
	require.Equal(t, modeNormal, m.mode)
}

func TestOnNormalKey_EscClearsFilterAndKeepsCurrentChat(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.chatFilter = "oc_5", "群 7"
	m.clampChat()
	mm, _ := m.onNormalKey("esc")
	m = mm.(Model)
	require.Empty(t, m.chatFilter)
	require.Equal(t, "oc_5", m.visibleRows()[m.chatIdx].chatID())
}

func TestOpenChat_DropsFilterHidingIt(t *testing.T) {
	m := sized(120, 36)
	m.chatFilter = "群 7"
	m.openChat("oc_1")
	require.Empty(t, m.chatFilter)
	require.Equal(t, "oc_1", m.visibleRows()[m.chatIdx].chatID())
}

func TestOnNormalKey_TabCyclesListPanesOnly(t *testing.T) {
	m := sized(130, 36) // wide enough for three panes: chats + messages + thread
	m.focus = paneMessages
	mm, _ := m.onNormalKey("tab")
	m = mm.(Model)
	require.Equal(t, paneChats, m.focus)
	require.Equal(t, modeNormal, m.mode)
	m = withThread(m)
	m.focus = paneMessages
	mm, _ = m.onNormalKey("tab")
	m = mm.(Model)
	require.Equal(t, paneThread, m.focus)
	mm, _ = m.onNormalKey("l")
	m = mm.(Model)
	require.Equal(t, paneThread, m.focus, "l stops at the right edge")
	mm, _ = m.onNormalKey("h")
	m = mm.(Model)
	require.Equal(t, paneMessages, m.focus)
}

func TestOnNormalKey_GPrefixDiesWithTheKeyAfterIt(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.msgIdx = paneMessages, 20

	mm, _ := m.onNormalKey("g")
	require.True(t, mm.(Model).pendingG)
	mm, _ = mm.(Model).onNormalKey("k")
	require.False(t, mm.(Model).pendingG, "an unrelated key cancels the half-typed gg")

	mm, _ = mm.(Model).onNormalKey("g")
	require.Equal(t, 19, mm.(Model).msgIdx, "the second g arms a fresh prefix rather than completing the cancelled one")

	mm, _ = mm.(Model).onNormalKey("g")
	require.Zero(t, mm.(Model).msgIdx, "gg typed back to back still jumps to the top")
}

func TestView_FoldsRightPaneOnNarrowTerminal(t *testing.T) {
	m := sized(82, 35)
	m = withThread(m)
	m.focus = paneThread
	require.True(t, m.foldRight())
	v := m.View()
	for _, line := range strings.Split(v.Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "%q", line)
	}
	require.Contains(t, ansi.Strip(v.Content), "Thread omt_1")
	p, _ := m.hit(chatsWidth+5, 3)
	require.Equal(t, paneThread, p)
	mm, _ := m.onNormalKey("h")
	m = mm.(Model)
	require.Equal(t, paneChats, m.focus, "h skips the hidden messages pane")
}

func TestOnInsertKey_EscOnFoldedLayoutShowsMessages(t *testing.T) {
	// A forwarded bundle has no box of its own, so the folded column is
	// carrying the chat's — and leaving it is leaving the column.
	m := sized(82, 35)
	m.rightKind, m.threadID, m.thread = rightForward, "om_bundle", m.msgs[:3]
	m.layout()
	require.True(t, m.foldRight())
	m.mode, m.focus = modeInsert, paneInput
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)
	require.Equal(t, paneMessages, m.focus)
	require.False(t, m.threadOpen(), "the right pane that covered the messages closes")
}

func TestOnInsertKey_EscLeavesTheThreadBoxForItsOwnColumn(t *testing.T) {
	m := withThread(sized(82, 35))
	require.True(t, m.foldRight())
	require.Equal(t, sideRight, m.side, "the folded column carries its own box")
	m.mode, m.focus = modeInsert, paneInput
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)
	require.Equal(t, paneThread, m.focus, "the pane behind the box is the column it belongs to")
	require.True(t, m.threadOpen(), "leaving the box is not leaving the frame")
}

func TestView_TooSmallTerminal(t *testing.T) {
	m := sized(50, 10)
	require.Contains(t, m.View().Content, "too small")
}

func TestUpdate_BackgroundColorDrivesSelectionShade(t *testing.T) {
	m := New(Deps{})
	light := lipgloss.Color("#eff1f5")
	mm, _ := m.Update(tea.BackgroundColorMsg{Color: light})
	m = mm.(Model)
	require.Less(t, luma(m.th.sel.GetBackground()), luma(light), "light theme: selection darker than the background")
	dark := lipgloss.Color("#1e1e2e")
	mm, _ = m.Update(tea.BackgroundColorMsg{Color: dark})
	m = mm.(Model)
	require.Greater(t, luma(m.th.sel.GetBackground()), luma(dark), "dark theme: selection lighter than the background")
}

func TestComposerStyles_FollowPalette(t *testing.T) {
	for _, dark := range []bool{true, false} {
		st := composerStyles(dark)
		require.Equal(t, lipgloss.NoColor{}, st.Focused.CursorLine.GetBackground(), "no cursor-line shade")
		require.Equal(t, colDim, st.Focused.Placeholder.GetForeground())
	}
}

func luma(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
}

func TestActivate_SearchHitAnchorsMessagePage(t *testing.T) {
	m := sized(120, 36)
	m.searching, m.focus, m.msgIdx = true, paneMessages, 0
	m.searchHits = messageHits(store.Message{MessageID: "om_old", ChatID: "oc_2", CreateMs: 123})
	m = m.notify("1 hit", false)
	mm, _ := m.activate()
	m = mm.(Model)
	require.Equal(t, "oc_2", m.pendingChat)
	require.Empty(t, m.notice, "the search notice does not outlive the search")
	require.Equal(t, "om_old", m.pendingSelect.id)
	require.EqualValues(t, 123, m.pendingSince)
	require.Equal(t, anchoredPageSize, m.pendingLimit, "a page anchored at a hit has to reach from it to the tail")
	q := messageQuery("oc_2", 123, anchoredPageSize)
	require.EqualValues(t, 123, q.SinceMs)
	require.False(t, q.Desc)
	q = messageQuery("oc_2", 0, messagePageSize)
	require.True(t, q.Desc)
	require.Equal(t, messagePageSize, q.Limit)
}

func TestRenderStatus_StaysOneLine(t *testing.T) {
	m := sized(120, 36)
	m = m.notify("no messages match "+strings.Repeat("z", 200), true)
	s := m.renderStatus()
	require.Equal(t, 1, lipgloss.Height(s))
	require.Equal(t, m.width, lipgloss.Width(s))
}

func TestChatsLoaded_CursorFollowsItsOwnChat(t *testing.T) {
	m := sized(120, 36)
	m.chatID = "oc_3"
	m.repinChat(m.chatID, rowKeyAt(m.visibleRows(), m.chatTop))
	require.Equal(t, "oc_3", m.visibleRows()[m.chatIdx].chatID())

	// A message in another chat re-sorts the list; the cursor belongs to the
	// chat, not to the row it happened to be on.
	reordered := append([]store.Chat{m.chats[7]}, slices.Delete(slices.Clone(m.chats), 7, 8)...)
	mm, _ := m.update(chatsLoadedMsg{chats: reordered})
	m = mm.(Model)
	require.Equal(t, "oc_3", m.visibleRows()[m.chatIdx].chatID())
}

func TestChatsLoaded_FilterBeingTypedKeepsItsCursor(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.mode, m.chatFilter, m.chatIdx = "oc_3", modeFilter, "群 1", 2
	mm, _ := m.update(chatsLoadedMsg{chats: m.chats})
	m = mm.(Model)
	require.Equal(t, 2, m.chatIdx)
}

func TestSelection_TheSelectedMessageShowsItsTime(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.msgIdx = paneMessages, 0
	m.rebuildMessages()
	stamp := msgTime(m.msgs[3].CreateMs, time.Now())
	require.NotContains(t, fmtStatus(m), stamp)

	mm, _ := m.move(3)
	m = mm.(Model)
	require.Contains(t, fmtStatus(m), stamp, "moving the cursor spells out the new time")
	require.NotContains(t, ansi.Strip(m.renderMessages(m.bodyHeight())), stamp,
		"no row carries a clock, so the rows do not move as the cursor does")

	mm, _ = m.onClick(tea.Mouse{Button: tea.MouseLeft, X: chatsWidth + 5, Y: 2})
	m = mm.(Model)
	require.Contains(t, fmtStatus(m), msgTime(m.msgs[m.msgIdx].CreateMs, time.Now()),
		"clicking a message spells out its time too")
}

func TestMove_StepsThroughEveryMessageAcrossMergedBlocks(t *testing.T) {
	m := sized(120, 24)
	m.focus, m.msgIdx = paneMessages, 0
	// A pane with nothing in it yet counts as sitting at its tail, so the first
	// build lands on the newest message; this walk starts at the oldest.
	m.scrollMessagesToSelection()
	for i := range m.msgs {
		require.Equal(t, i, m.msgIdx, "j stops on every message, not on every block")
		require.GreaterOrEqual(t, firstRow(m.msgRows, i), m.msgTop, "the selected message is on screen")
		require.Less(t, lastRow(m.msgRows, i), m.msgTop+m.msgListHeight())
		mm, _ := m.move(1)
		m = mm.(Model)
	}
}

func TestModelPicHeight_DoesNotMoveWithTheReplyBar(t *testing.T) {
	m := sized(120, 40)
	was := m.picHeight()
	require.Equal(t, m.msgListHeight(), was, "a picture may fill the pane it is drawn in")

	m.replyTo = &store.Message{MessageID: "om_1"}
	m.layout()
	require.Equal(t, was, m.picHeight(),
		"opening the reply bar must not resize every picture on screen")
	require.Equal(t, m.msgListHeight(), m.picHeight(),
		"and costs no row either, since the band claims one for the quote in every state")
}

func TestHighlightChat_FocusedRowTakesTheFixedTint(t *testing.T) {
	tint := "48;2;231;238;252"
	m := sized(120, 36)
	m.focus = paneChats
	require.Contains(t, m.renderChats(m.chatsBodyHeight()), tint, "the row under the cursor carries the client's tint")
	require.NotContains(t, m.highlightChat("plain", false), tint,
		"without focus the row falls back to the shaded selection")
}

func TestRenderChats_TintsTheAvatarColumnOfTheRowUnderTheCursor(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.chatIdx = paneChats, rowOf(0) // a chat: the Unread row draws no disc
	m.avatars = badgedAvatars{}

	require.Contains(t, m.renderChats(m.chatsBodyHeight()), "48;2;231;238;252m····",
		"the tint reaches the avatar cells, so the cleared corners of a disc take it too")
}

func TestHighlightChat_KeepsARunsOwnColours(t *testing.T) {
	m := sized(120, 36)
	line := m.highlightChat(stUnread.Render("3")+" "+stBold.Render("群"), true)
	require.Contains(t, ansi.Strip(line), "3 群")
	require.Contains(t, line, "31m3", "a run naming its own colour keeps it")
	require.GreaterOrEqual(t, strings.Count(line, "48;2;231;238;252"), 3,
		"the tint is re-applied after every embedded style: %q", line)
}

func TestRenderSearchRows_DoesNotLendOneChatsPeerToAnothers(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", ChatID: "oc_g", SenderID: "ou_x", SenderName: "孙琪",
		Content: "@李四 看下", MentionsJSON: `[{"id":"ou_a","key":"@_user_1","name":"李四"}]`,
		CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.p2p, st.peer = true, "ou_peer"

	var out strings.Builder
	for _, r := range renderSearchRows(messageHits(msgs...), []store.Chat{{ChatID: "oc_g", Name: "平台组"}}, "Messages", st) {
		out.WriteString(segText(r))
	}
	require.Contains(t, out.String(), stAccent.Render("@李四"),
		"hits run across chats, so the one the cursor sits on lends them nothing")
}

func TestOnFilterKey_CancellingAnEmptyFilterChangesNothing(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.chatID, m.chatIdx, m.chatTop = paneMessages, "oc_0", 0, 0
	m = wheelChats(m, 3, tea.MouseWheelDown)
	before := [3]int{int(m.focus), m.chatIdx, m.chatTop}

	mm, _ := m.onNormalKey("/")
	m = mm.(Model)
	mm, _ = m.onFilterKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)

	require.Equal(t, modeNormal, m.mode)
	require.Empty(t, m.chatFilter)
	require.Equal(t, before, [3]int{int(m.focus), m.chatIdx, m.chatTop},
		"opening the filter and leaving it without typing must change nothing")
}

func TestOnFilterKey_CancellingPutsTheReaderBackWhereTheyWere(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.chatID, m.chatIdx, m.chatTop = paneMessages, "oc_0", rowOf(40), rowOf(34)
	onCursor, onTop := "oc_40", "oc_34"

	mm, _ := m.onNormalKey("/")
	m = mm.(Model)
	for _, r := range "群 7" {
		mm, _ = m.onFilterKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(Model)
	}
	require.NotEmpty(t, m.chatFilter, "the filter has to have taken the keys")
	mm, _ = m.onFilterKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)

	require.Empty(t, m.chatFilter, "esc drops the filter")
	require.Equal(t, paneMessages, m.focus, "and hands the pane back")
	require.Equal(t, onCursor, rowKeyAt(m.visibleRows(), m.chatIdx), "the cursor is on the chat it was on")
	require.Equal(t, onTop, rowKeyAt(m.visibleRows(), m.chatTop), "and the list is where it was")
}

// A rule reaches the pane's edge whatever its label is spelled in. The dashes
// are counted against the columns the label occupies, so a CJK chat name — two
// columns a character, three bytes a character — does not cut the rule short.
func TestSearchRule_FillsThePaneUnderALabelOfAnyScript(t *testing.T) {
	for _, label := range []string{"Messages", "平台组", "项目协作群"} {
		plain := ansi.Strip(searchRule(label, 60).text)

		require.Equal(t, 60, ansi.StringWidth(plain), "label %q", label)
		require.Equal(t, plain, strings.TrimRight(plain, " "),
			"the rule is drawn to the edge, not padded out to it: label %q", label)
	}
}

// A picture under the cursor has nothing but its own rows to say so: one
// sender's run collapses the sender line into the message that opened it, so
// the picture in the middle of a run carries no text row of its own.
func TestRenderMessages_PaintsThePictureRowsOfTheSelectedMessage(t *testing.T) {
	m := sized(106, 40)
	p := testPictures(t)
	m.pics = p
	m.msgsBase = []store.Message{
		{MessageID: "om_1", ChatID: "oc_1", SenderID: "ou_a", SenderName: "张三", Content: "4", RenderedAt: 1, CreateMs: 1_000},
		{MessageID: "om_2", ChatID: "oc_1", SenderID: "ou_a", SenderName: "张三", MsgType: "image",
			Content: "[Image: img_a]", RenderedAt: 1, CreateMs: 2_000},
		{MessageID: "om_3", ChatID: "oc_1", SenderID: "ou_a", SenderName: "张三", Content: "5", RenderedAt: 1, CreateMs: 3_000},
	}
	m.meta.res = map[string][]store.Resource{"om_2": {
		{FileKey: "img_a", LocalPath: writePNG(t, p.dataDir, "a.png", 300, 200), Status: "done"}}}
	m.applyOutbox()
	m.layout()
	m.picturePrepare()
	m.focus, m.msgIdx = paneMessages, 1
	m.scrollMessagesToSelection()

	require.Equal(t, []int{1}, highlighted(m))
}

func TestRowLine_APictureKeepsItsCellsUnderTheTint(t *testing.T) {
	m := sized(106, 40)
	p := testPictures(t)
	m.pics = p
	pic := p.place(writePNG(t, p.dataDir, "a.png", 300, 200), 30, 20)
	require.NotZero(t, pic.cols)
	require.NotEmpty(t, p.prepare([]picture{pic}))

	line, tint := m.rowLine(msgRow{pic: pic, lead: lead{box: strings.Repeat(" ", avatarWidth), mark: " "}}, 60)
	require.True(t, tint)
	out := m.highlight(line, true)
	require.Contains(t, out, ansi.Style{}.BackgroundColor(m.th.sel.GetBackground()).String())
	require.Equal(t, pic.cols, strings.Count(ansi.Strip(out), string(kitty.Placeholder)),
		"the placement names its image in the foreground, which the tint leaves alone")
}
