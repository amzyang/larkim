package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// bundleStyle renders a page holding one merged forward, with the collapsed
// line the store would have answered.
func bundleStyle(g store.ForwardGist) msgStyle {
	st := baseStyle()
	st.forwards = map[string]store.ForwardGist{"om_fwd": g}
	return st
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
		bundleStyle(store.ForwardGist{ChildCount: 2, Expanded: true, SenderName: "张三", MsgType: "text",
			ContentRaw: `{"text":"预算定了"}`})))

	require.NotContains(t, out, "forwarded_messages")
	require.NotContains(t, out, "2026-09-22T09:00:00", "an ISO timestamp is not something a reader scans for")
	require.Contains(t, out, "[合并转发] 2 条")
	require.Contains(t, out, "张三: 预算定了")
}

func TestRenderRows_AContainerTakesExactlyOneBodyRow(t *testing.T) {
	x := theBundle()
	small := renderRows([]store.Message{x}, bundleStyle(store.ForwardGist{ChildCount: 2, Expanded: true,
		SenderName: "张三", MsgType: "text", ContentRaw: `{"text":"预算定了"}`}))
	big := renderRows([]store.Message{x}, bundleStyle(store.ForwardGist{ChildCount: 40, Expanded: true,
		SenderName: "张三", MsgType: "text", ContentRaw: `{"text":"预算定了"}`}))

	require.Equal(t, len(small), len(big),
		"however many messages are inside, the list spends the same one line on them")
	require.Less(t, len(small), strings.Count(x.Content, "\n"),
		"and fewer lines than the rendering it replaces, which is the whole point")
}

func TestRenderRows_AForwardedBundleShowsItsFirstChild(t *testing.T) {
	// A forward is frozen, so what it opens with is the context it was
	// forwarded for — unlike a thread, where the last word is the state.
	out := rowText(renderRows([]store.Message{theBundle()},
		bundleStyle(store.ForwardGist{ChildCount: 2, Expanded: true, SenderName: "张三", MsgType: "text",
			ContentRaw: `{"text":"预算定了"}`})))

	require.Contains(t, out, "张三: 预算定了")
	require.NotContains(t, out, "收到")
}

func TestRenderRows_AnUnexpandedBundleNamesNoCount(t *testing.T) {
	out := rowText(renderRows([]store.Message{theBundle()}, bundleStyle(store.ForwardGist{})))

	require.Contains(t, out, "[合并转发]")
	require.NotContains(t, out, "条", "a number that changes once the children land is worse than none")
}

func TestRenderRows_ARefusedBundleSaysSo(t *testing.T) {
	out := rowText(renderRows([]store.Message{theBundle()}, bundleStyle(store.ForwardGist{Refused: true})))

	require.Contains(t, out, "无法展开")
}

func TestSummaryRow_KeepsTheCountWhenTheGistMustBeCut(t *testing.T) {
	st := bundleStyle(store.ForwardGist{ChildCount: 23, Expanded: true, SenderName: "张三",
		MsgType: "text", ContentRaw: `{"text":"` + strings.Repeat("很长的一句话", 20) + `"}`})
	st.width = 30

	out := rowText(renderRows([]store.Message{theBundle()}, st))

	require.Contains(t, out, "23 条", "how much is in there is what the reader is deciding on")
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), st.width)
	}
}

func TestRenderRows_ASummaryLineCarriesAnOpenZone(t *testing.T) {
	rows := renderRows([]store.Message{theBundle()},
		bundleStyle(store.ForwardGist{ChildCount: 2, Expanded: true, SenderName: "张三", MsgType: "text",
			ContentRaw: `{"text":"预算定了"}`}))

	zoned := 0
	for _, r := range rows {
		for _, z := range r.zones {
			if z.open == "" {
				continue
			}
			zoned++
			require.Equal(t, rightForward, z.openKind, "the kind is carried, not read off the id's prefix")
			require.Equal(t, "om_fwd", z.openRoot)
			require.Equal(t, r.lead.cols(), z.x0, "the whole line is the target")
		}
	}
	require.Equal(t, 1, zoned)
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
