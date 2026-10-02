package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestFloater_StandsUnderTheRunItCompletes(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	f, ok := m.floater()
	require.True(t, ok)
	// The box's border sits one column left of the `:`, so the offers
	// themselves line up under the run.
	require.Equal(t, m.floatAnchor()-1, f.x)

	// Writing ahead of the run carries the box along with it.
	wider := typeInto(newPumModel(t), "hello there :do")
	g, ok := wider.floater()
	require.True(t, ok)
	require.Greater(t, g.x, f.x, "the run starts further along the line")
	require.Equal(t, len("hello there "), g.x-f.x)
}

func TestFloater_IsHeldInsideTheScreen(t *testing.T) {
	m := typeInto(newPumModel(t), strings.Repeat("x", 200)+" :do")
	f, ok := m.floater()
	require.True(t, ok)
	require.GreaterOrEqual(t, f.x, 0)
	require.LessOrEqual(t, f.x+f.w, m.width, "a box that would hang off the edge is pushed back on")
}

func TestFloater_RestsOnThePanesBottomBorder(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	f, ok := m.floater()
	require.True(t, ok)
	require.Equal(t, m.bodyHeight()+2, f.y+f.h, "its border lands on the pane's, one row off the box")
	require.Equal(t, m.pumRows()+2, f.h)
	require.Greater(t, f.y, msgHeaderHeight, "and the pane keeps its head")
}

func TestPumRows_IsZeroOutsideInsertMode(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	require.Positive(t, m.pumRows())

	// The popup is never cleared on the way out of insert mode, so the mode is
	// what keeps a stale one off the pane.
	m.mode = modeNormal
	require.True(t, m.pum.open())
	require.Zero(t, m.pumRows())
	_, ok := m.floater()
	require.False(t, ok)
}

func TestFloater_TintsTheCursorRowAcrossItsWidth(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	f, ok := m.floater()
	require.True(t, ok)
	lines := strings.Split(f.block, "\n")
	require.Contains(t, lines[1], "\x1b[48;2;231;238;252m", "the cursor row wears the client's tint")
	if len(lines) > 2 {
		require.NotContains(t, lines[2], "\x1b[48;2;231;238;252m", "no other row does")
	}
}
func TestFloatOver_DrawsThePopupOverTheMessagesPane(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	f, ok := m.floater()
	require.True(t, ok)

	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	require.Greater(t, len(lines), f.y+f.h)
	row := lines[f.y+1] // the popup's first offer, past its top border
	want := ansi.Strip(m.joinSegs(m.floatSegs()[0], f.w-2))
	require.Contains(t, row, strings.TrimSpace(want))
}

func TestView_KeepsEveryLineInsideTheTerminalWithThePopupOpen(t *testing.T) {
	// `:ha` offers more than the popup can draw and every offer carries an
	// emoji, so the rows go through the picture column as well as the text.
	m := typeInto(newPumModel(t), ":ha")
	require.True(t, m.pum.open())
	for i, l := range strings.Split(m.View().Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), m.width, "line %d overflows: %q", i, l)
	}
}

func TestOfferSegs_MeasureWhatTheyDraw(t *testing.T) {
	m := typeInto(newPumModel(t), ":ha")
	for _, segs := range m.floatSegs() {
		w := segsWidth(segs)
		require.Equal(t, w, lipgloss.Width(m.joinSegs(segs, w)))
	}
}

func TestWheel_WalksThePopupRatherThanTheMessagesUnderIt(t *testing.T) {
	m := typeInto(newPumModel(t), ":ha")
	f, ok := m.floater()
	require.True(t, ok)
	require.Zero(t, m.pum.menu.idx)
	top := m.msgTop

	mm, _ := m.onWheel(tea.Mouse{X: f.x + 1, Y: f.y + 1, Button: tea.MouseWheelDown})
	next := mm.(Model)
	require.Equal(t, 1, next.pum.menu.idx, "the offers move")
	require.Equal(t, top, next.msgTop, "the conversation behind does not")
}

func TestClick_IsSwallowedByThePopupItLandsOn(t *testing.T) {
	m := typeInto(newPumModel(t), ":ha")
	f, ok := m.floater()
	require.True(t, ok)
	require.True(t, m.floatAt(f.x, f.y))
	require.False(t, m.floatAt(f.x-1, f.y), "and nothing beside it")
}

func TestFloater_KeepsItsWidthWhileTheListScrolls(t *testing.T) {
	m := typeInto(newPumModel(t), ":ha")
	require.Greater(t, len(m.pum.menu.items), pumMaxRows)
	f, ok := m.floater()
	require.True(t, ok)
	// A window's worth per step still shows every row in some window, and the
	// list holds thousands of them.
	rows := m.pumRows()
	for range len(m.pum.menu.items)/rows + 1 {
		m.pum.menu.move(rows, rows)
		g, ok := m.floater()
		require.True(t, ok)
		require.Equal(t, f.w, g.w, "row %d", m.pum.menu.idx)
	}
}

// infoPumModel is the popup open on a colleague whose box says something.
func infoPumModel(t *testing.T, draft string) Model {
	t.Helper()
	m := newPumModel(t)
	m.roster[1].Department = "平台组"
	m.roster[1].Email = "zhangsan@example.com"
	return typeInto(m, draft)
}

func TestInfoFloater_OpensBesideTheMenuForTheFocusedItem(t *testing.T) {
	m := infoPumModel(t, "@zs")
	f, _ := m.floater()
	info, ok := m.infoFloater(f)
	require.True(t, ok)
	require.Equal(t, f.x+f.w, info.x, "east of the list, where there is room")

	// A list pushed to the right edge has its box west of it.
	f.x = m.width - f.w
	info, ok = m.infoFloater(f)
	require.True(t, ok)
	require.Equal(t, f.x, info.x+info.w, "west, when only that side fits")
	require.GreaterOrEqual(t, info.x, 0)
}

func TestInfoFloater_IsDroppedWhereNeitherSideHasRoom(t *testing.T) {
	m := infoPumModel(t, "@zs")
	f, _ := m.floater()
	// A list as wide as the screen leaves nothing either side.
	f.x, f.w = 0, m.width
	_, ok := m.infoFloater(f)
	require.False(t, ok)
}

func TestInfoFloater_RestsOnTheListsBaseline(t *testing.T) {
	m := infoPumModel(t, "@zs")
	f, _ := m.floater()
	info, ok := m.infoFloater(f)
	require.True(t, ok)
	require.Equal(t, f.y+f.h, info.y+info.h, "the two grow up from one line")
	require.LessOrEqual(t, info.h-2, m.floatCeiling())
}

func TestInfoFloater_FollowsTheCursor(t *testing.T) {
	m := infoPumModel(t, "@")
	// @All leads and says nothing; the box opens once the cursor reaches 张三.
	f, _ := m.floater()
	_, ok := m.infoFloater(f)
	require.False(t, ok)
	m.pum.menu.move(2, m.pumRows())
	_, ok = m.infoFloater(f)
	require.True(t, ok)
	require.Contains(t, ansi.Strip(m.View().Content), "zhangsan@example.com")
}

func TestFloatAt_CoversTheInfoBox(t *testing.T) {
	m := infoPumModel(t, "@zs")
	f, _ := m.floater()
	info, ok := m.infoFloater(f)
	require.True(t, ok)
	require.True(t, m.floatAt(info.x+info.w-1, info.y))
	require.False(t, m.floatAt(info.x+info.w, info.y), "and nothing past it")
}

func TestInfoFloater_DrawsAShortAnswerAtItsOwnWidth(t *testing.T) {
	m := newPumModel(t)
	m.roster[1].Department = "QA"
	m = typeInto(m, "@zs")
	f, _ := m.floater()
	info, ok := m.infoFloater(f)
	require.True(t, ok, "an answer narrower than the floor still fits beside the list")
	require.Equal(t, len("QA")+2, info.w)
}
