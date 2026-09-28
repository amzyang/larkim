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
	require.Zero(t, m.pum.idx)
	top := m.msgTop

	mm, _ := m.onWheel(tea.Mouse{X: f.x + 1, Y: f.y + 1, Button: tea.MouseWheelDown})
	next := mm.(Model)
	require.Equal(t, 1, next.pum.idx, "the offers move")
	require.Equal(t, top, next.msgTop, "the conversation behind does not")
}

func TestClick_IsSwallowedByThePopupItLandsOn(t *testing.T) {
	m := typeInto(newPumModel(t), ":ha")
	f, ok := m.floater()
	require.True(t, ok)
	require.True(t, m.floatAt(f.x, f.y))
	require.False(t, m.floatAt(f.x-1, f.y), "and nothing beside it")
}
