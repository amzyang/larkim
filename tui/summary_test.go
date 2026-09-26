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

// bundleStyle renders a page holding one merged forward, with the collapsed
// card the store would have answered.
func bundleStyle(g store.ForwardGist) msgStyle {
	st := baseStyle()
	st.selfName = "林岚"
	st.forwards = map[string]store.ForwardGist{"om_fwd": g}
	return st
}

// kids is the preview the store hands a card, one text message per name.
func kids(pairs ...string) []store.ForwardChild {
	var out []store.ForwardChild
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, store.ForwardChild{SenderName: pairs[i], MsgType: "text",
			ContentRaw: `{"text":"` + pairs[i+1] + `"}`})
	}
	return out
}

// fromGroup is a bundle drawn from one group chat larkim knows.
func fromGroup(children ...store.ForwardChild) store.ForwardGist {
	return store.ForwardGist{ChildCount: len(children), Expanded: true, Preview: children,
		Sources: 1, SourceChatID: "oc_team", SourceChatMode: "group", SourceChatName: "平台组"}
}

// theBundle is a merge_forward as the chat holds it: lark-cli's rendering is
// the whole tree, which is what the list must not print.
func theBundle() store.Message {
	return store.Message{MessageID: "om_fwd", SenderName: "王五", MsgType: "merge_forward",
		CreateMs: msgAt(22, 9, 0), RenderedAt: 1,
		ContentRaw: `{"text":"Merged and Forwarded Message"}`,
		Content: "<forwarded_messages>\n" +
			"[2026-09-22T09:00:00+08:00] 张三:\n    预算定了\n" +
			"[2026-09-22T09:01:00+08:00] 李四:\n    收到\n" +
			"</forwarded_messages>"}
}

func TestRenderRows_AForwardedBundleNeverPrintsItsTags(t *testing.T) {
	out := rowText(renderRows([]store.Message{theBundle()},
		bundleStyle(fromGroup(kids("张三", "预算定了", "李四", "收到")...))))

	require.NotContains(t, out, "forwarded_messages")
	require.NotContains(t, out, "2026-09-22T09:00:00", "an ISO timestamp is not something a reader scans for")
	require.Contains(t, out, "Group Chat History")
	require.Contains(t, out, "张三: 预算定了")
}

func TestForwardTitle_NamesTheConversationTheWayTheClientDoes(t *testing.T) {
	for _, tc := range []struct {
		name string
		gist store.ForwardGist
		want string
	}{
		{"a group", fromGroup(), "Group Chat History"},
		{"a direct chat", store.ForwardGist{Sources: 1, SourceChatID: "oc_zhang", SourceChatMode: "p2p",
			SourceChatName: "张三", SourcePeerID: "ou_a"}, "林岚 and 张三's Chat History"},
		{"the reader's own chat", store.ForwardGist{Sources: 1, SourceChatID: "oc_self", SourceChatMode: "p2p",
			SourceChatName: "林岚", SourcePeerID: "ou_me"}, "林岚's Chat History"},
		{"a chat larkim was never in", store.ForwardGist{Sources: 1, SourceChatID: "oc_elsewhere"}, "Chat History"},
		{"several chats at once", store.ForwardGist{Sources: 3}, "Chat History"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, forwardTitle(tc.gist, "ou_me", "林岚"))
		})
	}
}

func TestRenderRows_AForwardCardShowsAtMostFourChildren(t *testing.T) {
	g := fromGroup(kids("张三", "一", "李四", "二", "王五", "三", "张三", "四")...)
	g.ChildCount = 9

	out := rowText(renderRows([]store.Message{theBundle()}, bundleStyle(g)))

	for _, want := range []string{"张三: 一", "李四: 二", "王五: 三", "张三: 四"} {
		require.Contains(t, out, want)
	}
	require.Contains(t, out, "…", "the frame holds more than the card shows")
}

func TestRenderRows_AForwardCardCountsNothing(t *testing.T) {
	g := fromGroup(kids("张三", "预算定了")...)
	g.ChildCount = 9

	out := rowText(renderRows([]store.Message{theBundle()}, bundleStyle(g)))

	require.NotContains(t, out, "9 条", "the client's card carries no number either")
	require.NotContains(t, out, "条")
}

func TestRenderRows_AForwardCardEndsWithoutAnEllipsisWhenItShowsEverything(t *testing.T) {
	out := rowText(renderRows([]store.Message{theBundle()},
		bundleStyle(fromGroup(kids("张三", "预算定了", "李四", "收到")...))))

	require.NotContains(t, out, "…")
}

func TestRenderRows_AForwardCardIsBoundedHoweverBigTheBundleIs(t *testing.T) {
	x := theBundle()
	small := renderRows([]store.Message{x}, bundleStyle(fromGroup(kids("张三", "预算定了")...)))
	big := fromGroup(kids("张三", "一", "李四", "二", "王五", "三", "张三", "四")...)
	big.ChildCount = 400

	require.Equal(t, store.ForwardPreview+2, forwardCardRows(renderRows([]store.Message{x}, bundleStyle(big))),
		"a title, four children and the ellipsis, whatever is behind them")
	require.Less(t, len(small), strings.Count(x.Content, "\n"),
		"and fewer lines than the rendering it replaces, which is the whole point")
}

func TestRenderRows_AForwardedBundleOpensWithItsFirstChild(t *testing.T) {
	// A forward is frozen, so what it opens with is the context it was
	// forwarded for — unlike a thread, where the last word is the state.
	rows := renderRows([]store.Message{theBundle()},
		bundleStyle(fromGroup(kids("张三", "预算定了", "李四", "收到")...)))

	require.Contains(t, rowText(rows[len(rows)-2:]), "张三: 预算定了")
}

func TestRenderRows_AnUnexpandedBundleDrawsTheTitleAlone(t *testing.T) {
	out := rowText(renderRows([]store.Message{theBundle()}, bundleStyle(store.ForwardGist{})))

	require.Contains(t, out, "Chat History")
	require.NotContains(t, out, "条", "a number that changes once the children land is worse than none")
	require.NotContains(t, out, "…")
}

func TestRenderRows_ARefusedBundleSaysSo(t *testing.T) {
	out := rowText(renderRows([]store.Message{theBundle()}, bundleStyle(store.ForwardGist{Refused: true})))

	require.Contains(t, out, "cannot be expanded")
}

func TestSummaryRow_CutsEveryCardLineToTheWidth(t *testing.T) {
	st := bundleStyle(fromGroup(kids("张三", strings.Repeat("很长的一句话", 20))...))
	st.width = 30

	out := rowText(renderRows([]store.Message{theBundle()}, st))

	require.Contains(t, out, "Chat History")
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), st.width)
	}
}

// forwardCardRows counts the rows a forward's card took: the ones leading into it.
func forwardCardRows(rows []msgRow) int {
	n := 0
	for _, r := range rows {
		for _, z := range r.zones {
			if z.openKind == rightForward {
				n++
			}
		}
	}
	return n
}

func TestRenderRows_EveryCardLineOpensTheSameFrame(t *testing.T) {
	rows := renderRows([]store.Message{theBundle()},
		bundleStyle(fromGroup(kids("张三", "预算定了", "李四", "收到")...)))

	zoned := 0
	for _, r := range rows {
		for _, z := range r.zones {
			if z.open == "" {
				continue
			}
			zoned++
			require.Equal(t, rightForward, z.openKind, "the kind is carried, not read off the id's prefix")
			require.Equal(t, "om_fwd", z.openRoot)
			require.Equal(t, "Group Chat History", z.openName, "the frame opens under the name the card drew")
			require.Equal(t, r.lead.cols(), z.x0, "the whole line is the target")
		}
	}
	require.Equal(t, store.ForwardPreview-1, zoned, "the title and both children, one target")
}

func TestSelectedZones_LeavesTheSummaryLineToTheKeyboardsOwnKeys(t *testing.T) {
	m := sized(140, 36)
	m.msgsBase = []store.Message{theBundle()}
	m.meta.forwards = map[string]store.ForwardGist{"om_fwd": {ChildCount: 2, Expanded: true}}
	m.applyOutbox()
	m.layout()
	m.msgIdx = 0
	m.rebuildMessages()

	// o means "open what this message carries" — a link, a file, Feishu
	// itself. The right pane is larkim's own, and Enter and t reach it.
	require.Empty(t, m.selectedZones())
}

// clickSummary presses the row the summary is drawn on in the given pane.
func clickSummary(t *testing.T, m Model, p pane) Model {
	t.Helper()
	rows, top, x0 := m.msgRows, m.msgTop, chatsWidth+1
	if p == paneThread {
		rows, top, x0 = m.threadRows, m.threadTop, m.width-m.rightWidth()+1
	}
	for i := top; i < len(rows); i++ {
		for _, z := range rows[i].zones {
			if z.open == "" {
				continue
			}
			next, _ := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: x0 + z.x0, Y: headerRow(p) + i - top})
			return next.(Model)
		}
	}
	t.Fatal("no summary line on screen")
	return m
}

// headerRow is the first body line of a pane, below its title.
func headerRow(p pane) int {
	if p == paneMessages {
		return msgHeaderHeight + 1
	}
	return headerHeight + 1
}

func TestOnClick_ASummaryLineOpensTheContainerInTheRightPane(t *testing.T) {
	m := sized(140, 36)
	m.msgsBase = []store.Message{theBundle()}
	m.meta.forwards = map[string]store.ForwardGist{"om_fwd": {ChildCount: 2, Expanded: true}}
	m.applyOutbox()
	m.layout()

	m = clickSummary(t, m, paneMessages)

	require.Equal(t, rightForward, m.rightKind)
	require.Equal(t, "om_fwd", m.threadID)
	require.Equal(t, paneThread, m.focus, "the press moved the reader into the pane it opened")
}

func TestOnClick_ASummaryPressedInsideTheRightPanePushesAFrame(t *testing.T) {
	m := onThread(t)
	m.threadMeta.forwards = map[string]store.ForwardGist{"om_inner": {ChildCount: 3, Expanded: true}}
	m.thread = []store.Message{
		{MessageID: "om_root", SenderName: "张三", Content: "根", RenderedAt: 1, CreateMs: 1},
		{MessageID: "om_inner", SenderName: "李四", MsgType: "merge_forward", CreateMs: 2},
	}
	m.rebuildThread()

	m = clickSummary(t, m, paneThread)

	require.Equal(t, rightForward, m.rightKind)
	require.Equal(t, "om_inner", m.threadID)
	require.Len(t, m.rightStack, 1, "the same press in the other pane replaces; here it deepens")
}

func TestOnClick_ASummaryPressFiresOnTheFirstClick(t *testing.T) {
	m := sized(140, 36)
	m.msgsBase = []store.Message{
		{MessageID: "om_a", SenderName: "张三", Content: "先", RenderedAt: 1, CreateMs: 1},
		theBundle(),
	}
	m.meta.forwards = map[string]store.ForwardGist{"om_fwd": {ChildCount: 2, Expanded: true}}
	m.applyOutbox()
	m.layout()
	m.msgIdx = 0

	m = clickSummary(t, m, paneMessages)

	require.Equal(t, rightForward, m.rightKind, "one press, like a link or a quote")
	require.Equal(t, 0, m.msgIdx, "and the cursor stays where it was: the press asked for the pane")
}

// theRoot is a thread root as the chat holds it.
func theRoot() store.Message {
	return store.Message{MessageID: "om_root", SenderName: "张三", Content: "hello", RenderedAt: 1,
		CreateMs: msgAt(22, 9, 0), ThreadID: "omt_1", MessagePosition: 3}
}

// threadStyle renders a page holding one thread root.
func threadStyle(g store.ThreadGist) msgStyle {
	st := baseStyle()
	if g.Replies > 0 || g.SenderName != "" {
		st.threads = map[string]store.ThreadGist{"omt_1": g}
	}
	return st
}

func TestRenderRows_AThreadRootShowsItsLastReply(t *testing.T) {
	// A thread is alive, so the newest word is where it stands — the
	// opposite of a forward, which is frozen and opens with its first.
	out := rowText(renderRows([]store.Message{theRoot()}, threadStyle(store.ThreadGist{
		Replies: 23, SenderName: "李四", MsgType: "text", ContentRaw: `{"text":"1234"}`})))

	require.Contains(t, out, "⤷ 23 replies")
	require.Contains(t, out, "李四: 1234")
}

func TestRenderRows_AThreadRootWithNoReplyStillGetsItsLine(t *testing.T) {
	// Without it the root reads as an ordinary message, and nothing says
	// that Enter opens a thread there rather than answering.
	out := rowText(renderRows([]store.Message{theRoot()}, threadStyle(store.ThreadGist{})))

	require.Contains(t, out, "⤷ No replies yet")
}

func TestRenderRows_AThreadSummaryIsNotDrawnInsideItsOwnPane(t *testing.T) {
	st := threadStyle(store.ThreadGist{Replies: 23, SenderName: "李四", MsgType: "text",
		ContentRaw: `{"text":"1234"}`})
	st.inFrame = true

	out := rowText(renderRows([]store.Message{theRoot()}, st))

	require.NotContains(t, out, "replies", "the replies stand right below the root there")
	require.Contains(t, out, "hello")
}

func TestRenderRows_AForwardedThreadRootShowsTheThreadInTheChatAndTheForwardInTheFrame(t *testing.T) {
	// One message, two containers. The live one wins in the chat; inside the
	// thread the same message is the forward's own line, so it opens in turn.
	x := theBundle()
	x.ThreadID, x.MessagePosition = "omt_1", 3
	st := bundleStyle(fromGroup(kids("张三", "预算定了")...))
	st.threads = map[string]store.ThreadGist{"omt_1": {Replies: 18, SenderName: "李四",
		MsgType: "text", ContentRaw: `{"text":"收到"}`}}

	inChat := rowText(renderRows([]store.Message{x}, st))
	require.Contains(t, inChat, "⤷ 18 replies")
	require.NotContains(t, inChat, "Chat History")

	st.inFrame = true
	inFrame := rowText(renderRows([]store.Message{x}, st))
	require.Contains(t, inFrame, "Group Chat History")
	require.NotContains(t, inFrame, "replies")
}

func TestRenderRows_AThreadSummaryCarriesAnOpenZone(t *testing.T) {
	rows := renderRows([]store.Message{theRoot()}, threadStyle(store.ThreadGist{Replies: 2,
		SenderName: "李四", MsgType: "text", ContentRaw: `{"text":"1234"}`}))

	zoned := 0
	for _, r := range rows {
		for _, z := range r.zones {
			if z.open == "" {
				continue
			}
			zoned++
			require.Equal(t, rightThread, z.openKind)
			require.Equal(t, "omt_1", z.open)
		}
	}
	require.Equal(t, 1, zoned)
}

func TestMessageQuery_FoldsThreadRepliesOutOfTheChatFlow(t *testing.T) {
	// Both shapes: the newest page, and one cut around an anchor.
	require.True(t, messageQuery("oc_a", 0).ExcludeThreadReplies)
	require.True(t, messageQuery("oc_a", 1000).ExcludeThreadReplies,
		"a limit spent on rows the page will not draw is a page short of messages")
}

func TestRenderRows_AThreadSummaryCarriesTheUnreadDot(t *testing.T) {
	waiting := store.ThreadGist{Replies: 3, Waiting: true, SenderName: "李四",
		MsgType: "text", ContentRaw: `{"text":"1234"}`}
	rows := renderRows([]store.Message{theRoot()}, threadStyle(waiting))
	require.Contains(t, marks(rows), "●", "the replies are off the page, so this line speaks for them")

	quiet := waiting
	quiet.Waiting = false
	rows = renderRows([]store.Message{theRoot()}, threadStyle(quiet))
	require.NotContains(t, marks(rows), "●")
}

func TestClearBlockDots_LeavesAThreadSummaryLit(t *testing.T) {
	// The dot comes from the read flags, not from the dots this visit
	// gathered, so walking the cursor onto the root cannot wipe a reply
	// nobody has seen.
	m := sized(140, 36)
	m.msgsBase = []store.Message{theRoot()}
	m.meta.threads = map[string]store.ThreadGist{"omt_1": {Replies: 3, Waiting: true,
		SenderName: "李四", MsgType: "text", ContentRaw: `{"text":"1234"}`}}
	m.applyOutbox()
	m.msgIdx = 0
	m.clearDotsAtCursor()
	m.rebuildMessages()

	require.Contains(t, marks(m.msgRows), "●")
}

// marks is the marker column of every row, styles stripped.
func marks(rows []msgRow) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(markOf(r))
	}
	return b.String()
}

func TestReplyGist_NamesAForwardRatherThanQuotingItsTree(t *testing.T) {
	// The rendering lark-cli leaves on a bundle is the whole of another chat.
	// A quote line that flattened it would read as tags and ISO timestamps.
	require.Equal(t, "[Chat History]", replyGist(theBundle()))
}

func TestRightTitle_NamesAForwardFrameAfterTheCardThatOpenedIt(t *testing.T) {
	m := sized(140, 36)
	m.rightKind, m.threadID, m.rightRoot = rightForward, "om_fwd", "om_fwd"
	m.rightName = "Group Chat History"

	require.Contains(t, ansi.Strip(m.rightTitle(40)), "Forwarded Group Chat History")
	require.NotContains(t, ansi.Strip(m.rightTitle(40)), "om_fwd",
		"a bundle's id is not something a reader recognises it by")
}

func TestContainerAtCursor_CarriesTheCardsNameIntoTheFrame(t *testing.T) {
	m := sized(140, 36)
	m.selfName = "林岚"
	m.msgsBase = []store.Message{theBundle()}
	m.meta.forwards = map[string]store.ForwardGist{"om_fwd": fromGroup(kids("张三", "预算定了")...)}
	m.applyOutbox()
	m.layout()

	f, ok := m.containerAtCursor()

	require.True(t, ok)
	require.Equal(t, "Group Chat History", f.name, "the keyboard opens under the same name the mouse does")
}

func TestShowRight_TitlesTheFrameBeforeItsListLands(t *testing.T) {
	m := sized(140, 36)

	m, _ = m.openRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd", name: "Group Chat History"})

	require.Equal(t, "Group Chat History", m.rightName,
		"the summary had already drawn the name, so the header never flickers through the fallback")
}
