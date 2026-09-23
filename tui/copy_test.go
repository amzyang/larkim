package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/agentctx"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// shortMsgs replaces the fixture's long messages with one-line ones, so a
// whole selection fits on screen and its highlight can be read off.
func shortMsgs(m Model, n int) Model {
	m.msgs = nil
	for i := range n {
		m.msgs = append(m.msgs, store.Message{MessageID: fmt.Sprintf("om_%d", i), ChatID: "oc_1",
			SenderName: "张三", SenderID: "ou_a", Content: fmt.Sprintf("line %d", i), RenderedAt: 1, CreateMs: int64(i) * 1000})
	}
	m.layout()
	return m
}

// highlighted lists the message indices painted as selected in the messages
// pane, so a range selection can be told apart from a single cursor row.
func highlighted(m Model) []int {
	bg := ansi.Style{}.BackgroundColor(m.th.sel.GetBackground()).String()
	var out []int
	lines := strings.Split(m.renderMessages(m.bodyHeight()), "\n")
	for i, line := range lines[2:] { // past the top border and the header
		row := m.msgTop + i
		if row < len(m.msgRows) && strings.Contains(line, bg) {
			if idx := m.msgRows[row].idx; !slices.Contains(out, idx) {
				out = append(out, idx)
			}
		}
	}
	return out
}

func TestStartVisual_AnchorsAtTheCursorAndExtends(t *testing.T) {
	m := shortMsgs(sized(120, 36), 12)
	m.focus, m.msgIdx = paneMessages, 4
	m.scrollMessagesToSelection()
	require.Equal(t, []int{4}, highlighted(m), "NORMAL highlights the cursor alone")

	mm, _ := m.onNormalKey("v")
	m = mm.(Model)
	require.Equal(t, modeVisual, m.mode)
	require.Equal(t, "om_4", m.visualAnchor, "the anchor names its message, so a reload cannot move it")

	for range 3 {
		mm, _ = m.onVisualKey("j")
		m = mm.(Model)
	}
	require.Equal(t, 7, m.msgIdx)
	require.Equal(t, []int{4, 5, 6, 7}, highlighted(m), "the whole range is painted, not just the cursor")
	require.Contains(t, fmtStatus(m), "VISUAL 4 msgs")

	mm, _ = m.onVisualKey("k")
	m = mm.(Model)
	require.Contains(t, fmtStatus(m), "VISUAL 3 msgs", "the count follows the selection live")
}

func TestOnVisualKey_ExtendsBackwardsFromTheAnchor(t *testing.T) {
	m := shortMsgs(sized(120, 36), 12)
	m.focus, m.msgIdx = paneMessages, 6
	mm, _ := m.onNormalKey("v")
	m = mm.(Model)
	for range 2 {
		mm, _ = m.onVisualKey("k")
		m = mm.(Model)
	}
	require.Equal(t, 4, m.msgIdx)
	require.Equal(t, []int{4, 5, 6}, highlighted(m), "the anchor stays put while the cursor walks up")
}

func TestOnVisualKey_EscCancelsAndShiftYLeavesVisual(t *testing.T) {
	m := sized(120, 36)
	m.focus = paneMessages
	mm, _ := m.onNormalKey("v")
	mm, _ = mm.(Model).onVisualKey("esc")
	require.Equal(t, modeNormal, mm.(Model).mode)

	mm, _ = m.onNormalKey("v")
	mm, cmd := mm.(Model).onVisualKey("Y")
	require.Equal(t, modeNormal, mm.(Model).mode, "Y copies and returns to NORMAL")
	require.NotNil(t, cmd)
}

func TestStartVisual_UnsupportedWhereThereIsNothingToSelect(t *testing.T) {
	base := sized(120, 36)
	for _, tc := range []struct {
		name  string
		model func() Model
	}{
		{"chats pane", func() Model { m := base; m.focus = paneChats; return m }},
		{"search results", func() Model {
			m := base
			m.focus, m.searching = paneMessages, true
			return m
		}},
		{"assistant pane", func() Model {
			m := base
			m.focus, m.aiOpen = paneThread, true
			return m
		}},
		{"composer", func() Model { m := base; m.focus = paneInput; return m }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mm, cmd := tc.model().onNormalKey("v")
			m := mm.(Model)
			require.Equal(t, modeNormal, m.mode)
			require.Nil(t, cmd)
			require.True(t, m.noticeErr, "the status bar says this is not a place to select: %q", m.notice)
		})
	}
}

func TestCopySelection_RefusesWhereThereIsNoContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model func() Model
	}{
		{"no chat open", func() Model { return New(Deps{}) }},
		{"search results", func() Model {
			m := sized(120, 36)
			m.focus, m.searching = paneMessages, true
			return m
		}},
		{"assistant pane", func() Model {
			m := sized(120, 36)
			m.focus, m.aiOpen = paneThread, true
			return m
		}},
		{"composer", func() Model { m := sized(120, 36); m.focus = paneInput; return m }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mm, cmd := tc.model().onNormalKey("Y")
			require.Nil(t, cmd, "the clipboard is left alone")
			require.True(t, mm.(Model).noticeErr, "%q", mm.(Model).notice)
		})
	}
}

func TestRunCopy_RejectsAnUnparsableRange(t *testing.T) {
	m := sized(120, 36)
	mm, cmd := m.runCommand("copy whenever")
	require.Nil(t, cmd)
	require.Contains(t, mm.(Model).notice, "usage: :copy")

	m.chatID = ""
	mm, cmd = m.runCommand("copy all")
	require.Nil(t, cmd)
	require.Equal(t, "nothing to copy", mm.(Model).notice)
}

func TestCopyQuery_CountsWalkBackFromTheNewest(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	q := copyQuery("oc_a", agentctx.Range{Limit: 200}, now)
	require.True(t, q.Desc, "a count is taken from the newest end and flipped afterwards")
	require.Equal(t, 200, q.Limit)
	require.Zero(t, q.SinceMs)
	require.True(t, q.ExcludeThreadReplies, "the limit must count the messages that survive")

	q = copyQuery("oc_a", agentctx.Range{Since: 7 * 24 * time.Hour}, now)
	require.False(t, q.Desc)
	require.Equal(t, now.Add(-7*24*time.Hour).UnixMilli(), q.SinceMs)
	require.Equal(t, copyAllLimit, q.Limit)

	q = copyQuery("oc_a", agentctx.Range{All: true}, now)
	require.Equal(t, copyAllLimit, q.Limit, "all must state a limit; the store would otherwise cap at 100")

	q = copyQuery("oc_a", agentctx.Range{Since: chatsCopyAge, Limit: chatsCopyLimit}, now)
	require.Equal(t, chatsCopyLimit, q.Limit)
	require.Equal(t, now.Add(-chatsCopyAge).UnixMilli(), q.SinceMs)
}

func TestFollowUp_NamesTheOldestMessageOfTheRange(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_old"}, {MessageID: "om_new"}}
	got := followUp(Deps{ConfigPath: "/home/z/.larkim/config.yaml"}, "oc_a", msgs)
	require.Equal(t, "larkim --config /home/z/.larkim/config.yaml messages list \\\n"+
		"  --chat oc_a --before om_old --limit 100 --json", got)
	require.Equal(t, "larkim messages list \\\n  --chat oc_a --before om_old --limit 100 --json",
		followUp(Deps{}, "oc_a", msgs), "an unset config path is simply left out")
	require.Empty(t, followUp(Deps{}, "oc_a", nil), "an empty range has nothing to continue from")
	require.Contains(t, followUp(Deps{ConfigPath: "/home/z/my larkim/config.yaml"}, "oc_a", msgs),
		`--config '/home/z/my larkim/config.yaml'`, "a path with a space still pastes as one argument")
}

// copyFixture is a chat with a thread, an attachment and a contact.
func copyFixture(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	now := time.Now()
	ms := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }

	require.NoError(t, st.UpsertChats(ctx, []store.Chat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}, 1))
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_me", Name: "林岚", Email: "linlan@example.com"},
		{OpenID: "ou_a", Name: "张三", Email: "zhangsan@example.com"},
	}, 1))
	require.NoError(t, st.SetChatMembers(ctx, "oc_a", []store.Contact{{OpenID: "ou_me"}, {OpenID: "ou_a"}, {OpenID: "ou_b"}}, 1))
	msgs := []store.Message{
		{MessageID: "om_old", ChatID: "oc_a", CreateMs: ms(72 * time.Hour), MessagePosition: 1, SenderID: "ou_a", SenderName: "张三", RawJSON: "{}"},
		{MessageID: "om_root", ChatID: "oc_a", CreateMs: ms(3 * time.Hour), MessagePosition: 2, SenderID: "ou_a", SenderName: "张三", ThreadID: "omt_1", RawJSON: "{}"},
		{MessageID: "om_reply", ChatID: "oc_a", CreateMs: ms(2 * time.Hour), MessagePosition: -3, SenderID: "ou_me", SenderName: "林岚", ThreadID: "omt_1", RawJSON: "{}"},
		{MessageID: "om_img", ChatID: "oc_a", CreateMs: ms(time.Hour), MessagePosition: 3, SenderID: "ou_b", SenderName: "李四", RawJSON: "{}"},
	}
	_, err = st.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_old", "上周的消息", "", "", 2))
	require.NoError(t, st.UpdateRendered(ctx, "om_root", "发布单合了吗", "", "", 2))
	require.NoError(t, st.UpdateRendered(ctx, "om_reply", "合了", "", "", 2))
	require.NoError(t, st.UpdateRendered(ctx, "om_img", "看图", "", "", 2))
	require.NoError(t, st.AddPendingResources(ctx, []store.Resource{{MessageID: "om_img", FileKey: "img_1", Type: "image"}}))
	require.NoError(t, st.MarkResourceDone(ctx, "om_img", "img_1", "resources/img_1.png", 1024))

	return Deps{Store: st, Self: "ou_me", DataDir: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
}

func runCopy(t *testing.T, d Deps, spec copySpec) contextMsg {
	t.Helper()
	msg := copyContext(d, spec)()
	if e, ok := msg.(errMsg); ok {
		require.NoError(t, e.err)
	}
	got, ok := msg.(contextMsg)
	require.True(t, ok, "%T", msg)
	return got
}

func TestCopyContext_RangeDropsThreadRepliesAndResolvesEveryone(t *testing.T) {
	d := copyFixture(t)
	got := runCopy(t, d, copySpec{chatID: "oc_a", rng: agentctx.Range{All: true}})

	require.Equal(t, 3, got.n, "the thread reply is folded away, as it is in the messages pane")
	require.Equal(t, "平台组", got.chat)
	require.Contains(t, got.text, "## chat 平台组 oc_a group 3人 external:false")
	require.Contains(t, got.text, "林岚 <linlan@example.com> ou_me (me)")
	require.Contains(t, got.text, "李四 ou_b", "a sender with no contact row still gets a line from the message")
	require.Contains(t, got.text, `thread="1 reply"`, "the folded reply is counted on its root")
	require.NotContains(t, got.text, "id=om_reply")
	require.Contains(t, got.text, "[image "+filepath.Join(d.DataDir, "resources/img_1.png")+"]", "attachment paths are absolute")
	require.Contains(t, got.text, "--chat oc_a --before om_old --limit 100 --json")
}

func TestCopyContext_ThreadSelectionKeepsItsReplies(t *testing.T) {
	d := copyFixture(t)
	ctx := context.Background()
	thread, err := d.Store.ListMessages(ctx, store.MessageQuery{ThreadID: "omt_1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, thread, 2)

	got := runCopy(t, d, copySpec{chatID: "oc_a", msgs: thread})
	require.Equal(t, 2, got.n)
	require.Contains(t, got.text, "id=om_reply", "an explicit selection is copied as picked, replies and all")
}

func TestCopyContext_CountIsOfKeptMessages(t *testing.T) {
	d := copyFixture(t)
	ctx := context.Background()
	// Bury the chat in thread replies: a limit that counted rows before
	// filtering would return almost nothing.
	var noise []store.Message
	for i := range 20 {
		noise = append(noise, store.Message{MessageID: fmt.Sprintf("om_n%d", i), ChatID: "oc_a",
			CreateMs:        time.Now().Add(-time.Duration(i) * time.Minute).UnixMilli(),
			MessagePosition: -3, ThreadID: "omt_1", SenderID: "ou_a", RawJSON: "{}"})
	}
	_, err := d.Store.UpsertMessages(ctx, noise, 1)
	require.NoError(t, err)

	got := runCopy(t, d, copySpec{chatID: "oc_a", rng: agentctx.Range{Limit: 3}})
	require.Equal(t, 3, got.n, "three chat messages, not three rows of which most are replies")
	require.NotContains(t, got.text, "id=om_n")
}

func TestCopyContext_ChatsPaneCoversTheLastDayOnly(t *testing.T) {
	d := copyFixture(t)
	got := runCopy(t, d, copySpec{chatID: "oc_a", rng: agentctx.Range{Since: chatsCopyAge, Limit: chatsCopyLimit}})
	require.Equal(t, 2, got.n, "the three-day-old message is outside the window")
	require.NotContains(t, got.text, "id=om_old")
	require.Contains(t, got.text, "id=om_root")
	require.Contains(t, got.text, "id=om_img")
}

func TestCopyContext_EmptyRangeStillCarriesTheHeader(t *testing.T) {
	d := copyFixture(t)
	got := runCopy(t, d, copySpec{chatID: "oc_a", rng: agentctx.Range{Since: time.Second}})
	require.Zero(t, got.n)
	require.Contains(t, got.text, "## chat 平台组 oc_a group")
	require.NotContains(t, got.text, "更多上下文", "there is nothing older to page into")
}

func TestUpdate_ContextMessageReportsSizeAndSetsTheClipboard(t *testing.T) {
	m := New(Deps{})
	mm, cmd := m.Update(contextMsg{text: strings.Repeat("x", 31*1024), n: 24, chat: "平台组"})
	require.Equal(t, "copied 24 msgs · 31 KB · 平台组", mm.(Model).notice)
	require.NotNil(t, cmd)

	mm, _ = m.Update(contextMsg{text: strings.Repeat("x", 205), n: 0, chat: "平台组"})
	require.Equal(t, "copied 0 msgs · 0.2 KB · 平台组", mm.(Model).notice)
	mm, _ = m.Update(contextMsg{text: "x", n: 1, chat: "x"})
	require.Contains(t, mm.(Model).notice, "copied 1 msg ·")
}

func TestHelpText_DocumentsTheCopyKeys(t *testing.T) {
	require.Contains(t, helpText, "Y copy agent context")
	require.Contains(t, helpText, "yy id · yr raw json · yc content")
	require.Contains(t, helpText, "v select a range")
	require.Contains(t, helpText, ":copy <200|7d|all>")
}

var _ tea.Cmd = copyContext(Deps{}, copySpec{})

// loaded replays what a background sync tick delivers to the open chat.
func loaded(m Model, msgs []store.Message) Model {
	mm, _ := m.Update(messagesLoadedMsg{chatID: m.chatID, msgs: msgs})
	return mm.(Model)
}

func TestUpdate_ReloadDoesNotWidenALiveSelection(t *testing.T) {
	m := shortMsgs(sized(120, 36), 6)
	m.focus, m.msgIdx = paneMessages, 2
	mm, _ := m.onNormalKey("v")
	m = mm.(Model)
	lo, hi := m.selectionRange()
	require.Equal(t, [2]int{2, 2}, [2]int{lo, hi})

	// Three more messages arrive while the selection is open.
	grown := append(slices.Clone(m.msgs), store.Message{MessageID: "om_x", ChatID: "oc_1", Content: "new", RenderedAt: 1, CreateMs: 99_000})
	m = loaded(m, grown)
	lo, hi = m.selectionRange()
	require.Equal(t, [2]int{2, 2}, [2]int{lo, hi}, "a tick must not drag the cursor to the newest message and swallow the range")
	require.Equal(t, modeVisual, m.mode)

	_, cmd := m.copySelection()
	require.NotNil(t, cmd)
}

func TestUpdate_ReloadShorterThanTheAnchorLeavesVisual(t *testing.T) {
	m := shortMsgs(sized(120, 36), 12)
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	mm, _ := m.onNormalKey("v")
	m = mm.(Model)

	// A recall drops the anchored message: messageQuery filters deleted rows.
	m = loaded(m, slices.Clone(m.msgs[:len(m.msgs)-1]))
	require.Equal(t, modeNormal, m.mode, "a selection whose anchor is gone ends rather than pointing at nothing")
	lo, hi := m.selectionRange()
	require.Less(t, hi, len(m.msgs), "the span stays inside the list")
	require.LessOrEqual(t, lo, hi)
	require.NotPanics(t, func() { m.copySelection() })
}

func TestUpdate_ThreadReloadKeepsTheSelectionPinned(t *testing.T) {
	m := sized(120, 36)
	m.threadOpen, m.threadID = true, "omt_1"
	m.thread = []store.Message{
		{MessageID: "om_a", Content: "a", RenderedAt: 1},
		{MessageID: "om_b", Content: "b", RenderedAt: 1},
		{MessageID: "om_c", Content: "c", RenderedAt: 1},
	}
	m.focus, m.threadIdx = paneThread, 1
	m.layout()
	mm, _ := m.onNormalKey("v")
	m = mm.(Model)

	mm, _ = m.Update(threadLoadedMsg{threadID: "omt_1", msgs: append([]store.Message{{MessageID: "om_0", Content: "0", RenderedAt: 1}}, m.thread...)})
	m = mm.(Model)
	lo, hi := m.selectionRange()
	require.Equal(t, [2]int{2, 2}, [2]int{lo, hi}, "the anchor follows its message, not its old index")

	mm, _ = m.Update(threadLoadedMsg{threadID: "omt_1", msgs: []store.Message{{MessageID: "om_z", Content: "z", RenderedAt: 1}}})
	require.Equal(t, modeNormal, mm.(Model).mode)
}
