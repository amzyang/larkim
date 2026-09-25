package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// helpHas reports whether the ? table documents something, read across the key
// and its prose the way a line of the panel reads.
func helpHas(s string) bool {
	for _, e := range helpEntries {
		if strings.Contains(strings.TrimSpace(e.keys+" "+e.desc), s) {
			return true
		}
	}
	return false
}

// trimmedLine is one laid-out row with its styling and its padding taken off,
// which is what a reader sees of it.
func trimmedLine(lines []string, i int) string {
	return strings.Join(strings.Fields(ansi.Strip(lines[i])), " ")
}

func helpModel(w, h int) Model {
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = w, h
	return m.openHelp()
}

func TestHelp_QuestionMarkOpensAndAnyKeyCloses(t *testing.T) {
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = 100, 30
	m = press(t, m, "?")
	require.True(t, m.help.open)
	require.False(t, press(t, m, "x").help.open)
}

func TestHelp_SlashFiltersToTheMatchingBindings(t *testing.T) {
	m := press(t, helpModel(100, 30), "/", "f", "o", "r", "w", "a", "r", "d")
	require.True(t, m.help.filtering)
	require.NotEmpty(t, m.help.hits)
	require.Less(t, len(m.help.hits), len(helpEntries))
	for _, h := range m.help.hits {
		require.Contains(t, strings.ToLower(h.entry.mode+" "+h.entry.keys+" "+h.entry.desc), "forward")
	}
}

func TestHelp_FilterReachesAKeyByItsProse(t *testing.T) {
	m := press(t, helpModel(100, 30), "/", "c", "l", "i", "p")
	var keys []string
	for _, h := range m.help.hits {
		keys = append(keys, h.entry.keys)
	}
	require.Contains(t, keys, "Ctrl+v")
}

func TestHelp_FilteredRowsNameTheirOwnMode(t *testing.T) {
	m := helpModel(100, 30)
	require.Equal(t, "NORMAL", trimmedLine(m.helpLines(), 0),
		"unfiltered, the mode is a heading of its own")

	m = press(t, m, "/", "r", "e", "c", "a", "l", "l")
	require.Equal(t, "NORMAL D recall your own message, after a y/n it asks for",
		trimmedLine(m.helpLines(), 0))
}

func TestHelp_EscapeDropsTheQueryBeforeThePanel(t *testing.T) {
	m := press(t, helpModel(100, 30), "/", "q")
	m = press(t, m, "esc")
	require.True(t, m.help.open)
	require.Empty(t, m.help.input.Value())
	require.Len(t, m.help.hits, len(helpEntries))
	require.False(t, press(t, m, "esc").help.open)
}

func TestHelp_ScrollsWithoutLosingTheColumns(t *testing.T) {
	m := helpModel(100, 20)
	require.Greater(t, len(m.helpLines()), m.helpRows(), "the table is taller than the box")
	m = press(t, m, "G")
	require.Equal(t, m.helpBottom(), m.help.top)
	m = press(t, m, "g")
	require.Equal(t, 0, m.help.top)
}

func TestHelp_WheelScrollsThePanelAndClicksAreInert(t *testing.T) {
	m := helpModel(100, 20)
	mm, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Equal(t, 3, mm.(Model).help.top)
	mm, _ = mm.(Model).Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	require.True(t, mm.(Model).help.open, "a click neither closes the panel nor reaches the panes")
	require.Equal(t, 3, mm.(Model).help.top)
}

func TestHelp_RenderFillsTheBoxAtEveryWidth(t *testing.T) {
	for _, w := range []int{minWidth, 100, 160} {
		m := helpModel(w, 24)
		for _, line := range strings.Split(ansi.Strip(m.renderHelp()), "\n") {
			require.Equal(t, w-4, len([]rune(line)), "width %d", w)
		}
	}
}

func TestHelp_KeysAreStyledApartFromTheirProse(t *testing.T) {
	m := helpModel(100, 30)
	out := m.renderHelp()
	require.Contains(t, out, stHelpKey.Render("Ctrl+d/Ctrl+u"))
}
