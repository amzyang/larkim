package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// todoBodyFromChat is a todo body the AI Assistant chat's task cards carry:
// the title is empty and the summary lives in the content paragraphs.
const todoBodyFromChat = `{"task_id":"task_a","summary":{"title":"","content":[[{"tag":"text","text":"From chat with AI Assistant"}]]}}`

// todoRaw is a task drawn from its own body, with no rendering behind it:
// that is the state every todo message is in when it lands.
func todoRaw(content string, done bool) []store.Message {
	m := store.Message{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a",
		MsgType: "todo", ContentRaw: todoBodyFromChat, CreateMs: msgAt(23, 9, 0), TodoDone: done}
	m.Content = content
	return []store.Message{m}
}

// todoLines is what the card draws, one string per row, past the day rule and
// the sender line.
func todoLines(t *testing.T, content string, done bool) []string {
	t.Helper()
	rows := renderRows(todoRaw(content, done), baseStyle())
	out := make([]string, 0, len(rows))
	for _, r := range rows[2:] {
		out = append(out, strings.TrimRight(ansi.Strip(segText(r)), " "))
	}
	return out
}

func TestTodoRows_DrawTheUncheckedBoxBeforeARenderingLands(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{todoBoxOpen + " From chat with AI Assistant"}, todoLines(t, "", false))
}

func TestTodoRows_DrawTheBoxTheRenderingCarries(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{todoBoxDone + " From chat with AI Assistant"},
		todoLines(t, "☑ From chat with AI Assistant", true))
	require.Equal(t, []string{todoBoxOpen + " 写周报", "Due: 2026-09-29 18:00:00"},
		todoLines(t, "☐ 写周报\nDue: 2026-09-29 18:00:00", false))
}

func TestTodoRows_SplitTheBoxFromTheSummaryItLeads(t *testing.T) {
	t.Parallel()
	rows := renderRows(todoRaw("☑ From chat with AI Assistant", true), baseStyle())
	zs := rowZones(rows)
	require.Len(t, zs, 2)
	// The circles alone toggle the task, in the state they are drawn in.
	require.Empty(t, zs[0].urls)
	require.Equal(t, "task_a", zs[0].task)
	require.True(t, zs[0].taskDone)
	require.Equal(t, 2, zs[0].x1-zs[0].x0, "the en-spaced glyph holds two columns")
	// The summary beside it opens the detail page, from past the space the
	// icon keeps between them.
	require.Equal(t, []string{applink.TodoLink("task_a")}, zs[1].urls)
	require.Equal(t, zs[0].x1+1, zs[1].x0)

	// The toggle zone answers a press through the same hit test a click
	// takes, which is the arm that decides the circle is reachable at all.
	z, ok := zoneAt(rows, 2, zs[0].x0)
	require.True(t, ok, "the circle is live to the hit test")
	require.Equal(t, "task_a", z.task)

	// The done circle wears the client's own green, the open one the bold
	// of the line it heads.
	doneRow := renderRows(todoRaw("☑ From chat with AI Assistant", true), baseStyle())[2]
	openRow := renderRows(todoRaw("", false), baseStyle())[2]
	require.Contains(t, doneRow.text, "\x1b[38;2;46;161;33m")
	require.NotContains(t, openRow.text, "\x1b[38;2;46;161;33m")
	// The finished summary is struck through and dimmed, the open one is
	// neither, and the strike stops where the words do: the padding wrap
	// added past them carries no style of the line's.
	require.Contains(t, doneRow.text, "\x1b[90;9m")
	require.NotContains(t, openRow.text, "\x1b[90;9m")
	require.False(t, strings.HasSuffix(doneRow.text, " \x1b[m\x1b]8;;\a"),
		"the strike stops at the last word: the padding wrap added carries none")
	// An unchecked box toggles the other way.
	for _, z := range rowZones(renderRows(todoRaw("", false), baseStyle())) {
		if z.task == "task_a" {
			require.False(t, z.taskDone)
		}
	}
}

func TestTodoRows_LeadTheDetailLinesToTheTask(t *testing.T) {
	t.Parallel()
	rows := renderRows(todoRaw("☐ 写周报\nDue: 2026-09-29 18:00:00", false), baseStyle())
	n := 0
	for _, r := range rows {
		for _, z := range r.zones {
			if len(z.urls) == 0 {
				continue
			}
			n++
			require.Equal(t, []string{applink.TodoLink("task_a")}, z.urls)
		}
	}
	require.Equal(t, 2, n, "the summary and the deadline each lead there")
}

func TestTodoRows_LeaveNothingClickableWhenTheBodyNamesNoTask(t *testing.T) {
	t.Parallel()
	rows := renderRows([]store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a",
		MsgType: "todo", ContentRaw: `{"summary":{"title":"写周报"}}`, CreateMs: msgAt(23, 9, 0)}},
		baseStyle())
	require.Empty(t, rowZones(rows))
}

func TestToggleTodo_PressesTheBoxTheOtherWay(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	d := Deps{Store: st, Syncer: &sync.Syncer{Store: st, Client: f}, Log: discardLog}

	cmd := toggleTodo(d, "task_a", false)
	require.Nil(t, cmd(), "a press that lands says nothing")
	require.True(t, f.Tasks["task_a"], "the unchecked box completes the task")

	cmd = toggleTodo(d, "task_a", true)
	require.Nil(t, cmd())
	require.False(t, f.Tasks["task_a"], "the checked one reopens it")
}

func TestToggleTodo_FailsLoudlyWhenFeishuRefuses(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	f.Err = errors.New("refused")
	d := Deps{Store: st, Syncer: &sync.Syncer{Store: st, Client: f}, Log: discardLog}
	require.Equal(t, noticeMsg{"could not toggle the task: refused"}, toggleTodo(d, "task_a", false)())
}
