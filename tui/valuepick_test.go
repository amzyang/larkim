package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/config"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// jevModelPick is the config panel with the ai.jev_model chooser open, and
// the Jev callers it rebuilds recorded.
func jevModelPick(t *testing.T) (Model, *[]string) {
	t.Helper()
	m := configModel(t)
	var built []string
	m.deps.NewSuggest = func(ai config.AI) ReactSuggester {
		built = append(built, "suggest "+ai.JevModel)
		return nil
	}
	m.deps.SetJudge = func(ai config.AI) { built = append(built, "judge "+ai.JevModel) }
	m = press(t, m.openConfig("ai.jev_model"), "enter")
	require.True(t, m.config.value.open)
	return m, &built
}

// valueOffers is the values the chooser lists, in order.
func valueOffers(m Model) []string {
	var out []string
	for _, h := range m.config.value.menu.items {
		out = append(out, h.value)
	}
	return out
}

func TestValuePick_OffersTheJevModelsOnTheOneSet(t *testing.T) {
	t.Parallel()
	m, _ := jevModelPick(t)
	require.Equal(t, []string{"jev-preview", "jev-latest"}, valueOffers(m))
	h, ok := m.config.value.menu.focused()
	require.True(t, ok)
	require.Equal(t, "jev-preview", h.value, "enter alone keeps what is set")
}

func TestValuePick_EnterWritesTheModelAndRebuildsEveryJevCaller(t *testing.T) {
	t.Parallel()
	m, built := jevModelPick(t)
	m = press(t, m, "j", "enter")

	require.False(t, m.config.value.open)
	require.Equal(t, "jev-latest", m.cfg.AI.JevModel)
	require.Equal(t, "jev-latest", configFile(t, m).AI.JevModel)
	require.Equal(t, []string{"suggest jev-latest", "judge jev-latest"}, *built)
	require.Equal(t, "ai.jev_model=jev-latest", m.notice, "no restart to wait for")
	require.Equal(t, "❯ ai.jev_model jev-latest", configRow(m))
}

func TestValuePick_EscKeepsTheValue(t *testing.T) {
	t.Parallel()
	m, built := jevModelPick(t)
	m = press(t, m, "down", "esc")

	require.False(t, m.config.value.open)
	require.True(t, m.config.open, "esc backs out of the chooser, not the panel")
	require.Equal(t, "jev-preview", m.cfg.AI.JevModel)
	require.Empty(t, *built)
}

func TestValuePick_ListsTheValuesUnderTheRowWithTheCurrentOneTicked(t *testing.T) {
	t.Parallel()
	m, _ := jevModelPick(t)
	lines := m.configLines()
	at := m.config.idx - m.configTop()
	line := func(i int) string { return strings.Join(strings.Fields(ansi.Strip(lines[at+i])), " ") }
	require.Equal(t, "✓ jev-preview", line(1))
	require.Equal(t, "jev-latest", line(2))
}

func TestValuePick_TheWheelMovesTheChooserNotThePanel(t *testing.T) {
	t.Parallel()
	m, _ := jevModelPick(t)
	idx := m.config.idx
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(Model)
	require.Equal(t, idx, m.config.idx)
	h, _ := m.config.value.menu.focused()
	require.Equal(t, "jev-latest", h.value)
}

func TestValuePick_AStrayKeyLeavesTheChooserOpen(t *testing.T) {
	t.Parallel()
	m, built := jevModelPick(t)
	m = press(t, m, "x", "tab")
	require.True(t, m.config.value.open)
	require.Equal(t, tabGeneral, m.config.tab)
	require.Empty(t, *built)
}

func TestRunSet_ANewJevModelRebuildsEveryJevCaller(t *testing.T) {
	t.Parallel()
	m, built := jevModelPick(t)
	m = press(t, m, "esc").closeConfig().runSet("ai.jev_model=jev-latest")
	require.Equal(t, []string{"suggest jev-latest", "judge jev-latest"}, *built)
	require.Equal(t, "jev-latest", m.cfg.AI.JevModel)
}

func TestRunSet_RefusesAJevModelThatIsNotOffered(t *testing.T) {
	t.Parallel()
	m, built := jevModelPick(t)
	m = press(t, m, "esc").closeConfig().runSet("ai.jev_model=jev-1.13.0")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "jev-preview, jev-latest")
	require.Equal(t, "jev-preview", m.cfg.AI.JevModel)
	require.Empty(t, *built)
}
