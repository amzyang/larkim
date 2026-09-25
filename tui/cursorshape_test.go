package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// cursorRow is the line of the frame the cursor was put on, stripped of the
// styling, so a position can be checked against what the reader sees rather
// than against the arithmetic that produced it.
func cursorRow(t *testing.T, v tea.View) string {
	t.Helper()
	require.NotNil(t, v.Cursor)
	lines := strings.Split(v.Content, "\n")
	require.Less(t, v.Cursor.Y, len(lines), "the cursor is on a row the frame has")
	return ansi.Strip(lines[v.Cursor.Y])
}

func TestCursorShape_BlockOnlyWhereKeysAreCommands(t *testing.T) {
	for _, tc := range []struct {
		md    mode
		shape tea.CursorShape
		blink bool
	}{
		{modeNormal, tea.CursorBlock, false},
		{modeVisual, tea.CursorBlock, false},
		{modeInsert, tea.CursorBar, true},
		{modeCommand, tea.CursorBar, true},
		{modeFilter, tea.CursorBar, true},
		{modeEmoji, tea.CursorBar, true},
	} {
		shape, blink := cursorShape(tc.md)
		require.Equal(t, tc.shape, shape, modeLabel(tc.md))
		require.Equal(t, tc.blink, blink, modeLabel(tc.md))
	}
}

func TestView_CursorShapeFollowsTheModeTheKeysPutItIn(t *testing.T) {
	m := pickerModel(t)
	require.Equal(t, tea.CursorBlock, m.View().Cursor.Shape, "the client opens in normal")

	for _, tc := range []struct {
		keys  []string
		md    mode
		shape tea.CursorShape
	}{
		{[]string{"i"}, modeInsert, tea.CursorBar},
		{[]string{"i", "esc"}, modeNormal, tea.CursorBlock},
		{[]string{"v"}, modeVisual, tea.CursorBlock},
		{[]string{":"}, modeCommand, tea.CursorBar},
		{[]string{"/"}, modeFilter, tea.CursorBar},
		{[]string{"e"}, modeEmoji, tea.CursorBar},
	} {
		got := press(t, m, tc.keys...)
		require.Equal(t, tc.md, got.mode, strings.Join(tc.keys, " "))
		v := got.View()
		require.NotNil(t, v.Cursor, strings.Join(tc.keys, " "))
		require.Equal(t, tc.shape, v.Cursor.Shape, strings.Join(tc.keys, " "))
		require.Nil(t, v.Cursor.Color, "the terminal's own cursor colour wins")
	}
}

func TestView_NormalModeParksTheBlockWhereWritingWouldResume(t *testing.T) {
	m := press(t, pickerModel(t), "i")
	m.input.SetValue("明天的评审挪到下午")
	m.replan()
	m.layout()

	ins := m.View().Cursor
	require.Equal(t, tea.CursorBar, ins.Shape)
	require.True(t, ins.Blink)

	nor := press(t, m, "esc").View().Cursor
	require.Equal(t, ins.Position, nor.Position, "esc reshapes the cursor, it does not move it")
	require.Equal(t, tea.CursorBlock, nor.Shape)
	require.False(t, nor.Blink, "a parked cursor is steady")
}

func TestView_CursorSitsAtTheEndOfTheDraftUnderTheQuote(t *testing.T) {
	m := pickerModel(t)
	mm, _ := m.startInsert(&m.msgs[0], false)
	m = mm.(Model)
	m.input.SetValue("收到")
	m.replan()
	m.layout()

	v := m.View()
	require.Contains(t, cursorRow(t, v), "收到")
	require.Equal(t, 1+lipgloss.Width("收到"), v.Cursor.X, "one cell of border, then the draft")
	above := ansi.Strip(strings.Split(v.Content, "\n")[v.Cursor.Y-1])
	require.Contains(t, above, "下周一发版", "the quote row is counted, not written over")
}

func TestView_CursorClearsThePreviewRowsAboveTheDraft(t *testing.T) {
	m := press(t, pickerModel(t), "i")
	m.input.SetValue("## 发布说明")
	m.replan()
	m.layout()
	require.NotEmpty(t, m.previewRows, "a post draft is previewed")

	v := m.View()
	require.Contains(t, cursorRow(t, v), "发布说明")
	require.Equal(t, 1+lipgloss.Width("## 发布说明"), v.Cursor.X)
}

func TestComposerAbove_CountsTheQuoteAndThePreviewRows(t *testing.T) {
	base := pickerModel(t)
	w := base.width - 2

	m := press(t, base, "i")
	require.Empty(t, m.composerAbove(w), "a plain draft has nothing over it")

	mm, _ := base.startInsert(&base.msgs[0], false)
	m = mm.(Model)
	require.Len(t, m.composerAbove(w), 1, "the quote takes one row")

	m.input.SetValue("## 发布说明")
	m.replan()
	m.layout()
	require.Len(t, m.composerAbove(w), len(m.previewRows)+2, "preview rows, the rule under them, then the quote")
}

func TestView_CursorFollowsTheCommandLinePrompt(t *testing.T) {
	top := pickerModel(t).bodyHeight() + 3

	cmd := press(t, pickerModel(t), ":").View()
	require.Equal(t, top, cmd.Cursor.Y, "the command line is the box's first row")
	require.Equal(t, 1+lipgloss.Width(":"), cmd.Cursor.X)

	typed := press(t, pickerModel(t), ":", "l", "s").View()
	require.Equal(t, 1+lipgloss.Width(":ls"), typed.Cursor.X)

	filter := press(t, pickerModel(t), "/", "平").View()
	require.Equal(t, top, filter.Cursor.Y)
	require.Equal(t, 1+lipgloss.Width("/平"), filter.Cursor.X)
}

func TestView_CursorSitsInThePickersQueryBox(t *testing.T) {
	m := press(t, pickerModel(t), "e", "z")
	v := m.View()
	require.Equal(t, m.bodyHeight()+3, v.Cursor.Y)
	require.Equal(t, 1+lipgloss.Width(pickerPrompt())+lipgloss.Width("z"), v.Cursor.X)
	require.Contains(t, cursorRow(t, v), "react")
}

func TestView_HidesTheCursorWhereThereIsNothingToWriteIn(t *testing.T) {
	require.Nil(t, press(t, pickerModel(t), "?").View().Cursor, "the help overlay covers the composer")

	loading := pickerModel(t)
	loading.width = 0
	require.Nil(t, loading.View().Cursor)

	small := pickerModel(t)
	small.width, small.height = minWidth-1, minHeight-1
	require.Nil(t, small.View().Cursor)
}

func TestView_CursorStaysInsideTheBoxWhenTheLineScrolls(t *testing.T) {
	m := press(t, pickerModel(t), ":")
	m.cmdline.SetValue("ai " + strings.Repeat("x", 200))
	m.cmdline.CursorEnd()

	v := m.View()
	require.Equal(t, m.width-2, v.Cursor.X, "the caret rides the right edge of the box, not off the screen")
	require.Equal(t, m.bodyHeight()+3, v.Cursor.Y)
}
