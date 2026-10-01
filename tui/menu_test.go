package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// wordSpec draws each word as itself, with an icon only where iconFor says.
func wordSpec(iconFor func(string) string) menuSpec[string] {
	return menuSpec[string]{row: func(s string) offer {
		return offer{icon: offerIcon{text: iconFor(s)}, name: s}
	}}
}

func noIcon(string) string { return "" }

func TestMenu_DropsTheIconColumnWhenNoRowHasOne(t *testing.T) {
	m := newPumModel(t)
	plain := fillMenu([]string{"copy", "goto"}, wordSpec(noIcon))
	require.False(t, plain.cols.icon)
	require.Equal(t, "copy", ansi.Strip(m.joinSegsWidth(m.offerRow(plain.rows[0], plain.cols, 0, true))))

	// One row with an icon is enough for the column; the others keep its
	// cells blank so the names still start in one column.
	mixed := fillMenu([]string{"copy", "goto"}, wordSpec(func(s string) string {
		if s == "goto" {
			return ">"
		}
		return ""
	}))
	require.True(t, mixed.cols.icon)
	require.Equal(t, "   copy", ansi.Strip(m.joinSegsWidth(m.offerRow(mixed.rows[0], mixed.cols, 0, false))))
	require.Equal(t, ">  goto", ansi.Strip(m.joinSegsWidth(m.offerRow(mixed.rows[1], mixed.cols, 1, false))))
}

func TestMenu_MeasuresEveryItemNotTheWindow(t *testing.T) {
	words := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "a much longer word"}
	u := fillMenu(words, wordSpec(noIcon))
	require.Equal(t, len("a much longer word"), u.cols.name, "the longest name is past the window, and still counts")

	m := newPumModel(t)
	first := m.offerRow(u.rows[0], u.cols, 0, false)
	require.Equal(t, len("a much longer word"), segsWidth(first), "a short row is padded to the column")
}

func TestMenu_CutsALongNameWithAnEllipsis(t *testing.T) {
	m := newPumModel(t)
	u := fillMenu([]string{strings.Repeat("x", offerNameMax+10)}, wordSpec(noIcon))
	require.Equal(t, offerNameMax, u.cols.name)
	line := ansi.Strip(m.joinSegsWidth(m.offerRow(u.rows[0], u.cols, 0, false)))
	require.True(t, strings.HasSuffix(line, "…"), line)
	require.Equal(t, offerNameMax, len([]rune(line)))
}

func TestMenu_NoselectStartsUnfocusedAndWalksBackToIt(t *testing.T) {
	spec := wordSpec(noIcon)
	spec.noselect = true
	u := fillMenu([]string{"a", "b"}, spec)
	_, ok := u.focused()
	require.False(t, ok)

	u.move(1, 8)
	got, ok := u.focused()
	require.True(t, ok)
	require.Equal(t, "a", got)

	u.move(-1, 8)
	_, ok = u.focused()
	require.False(t, ok, "walking up off the first row lands back on nothing")

	plain := fillMenu([]string{"a", "b"}, wordSpec(noIcon))
	plain.move(-1, 8)
	require.Zero(t, plain.idx, "a menu that always focuses stops at its first row")
}

func TestMenu_InfoIsResolvedForTheFocusedItemOnly(t *testing.T) {
	var asked []string
	spec := wordSpec(noIcon)
	spec.info = func(s string) []string {
		asked = append(asked, s)
		return []string{s}
	}
	u := fillMenu([]string{"a", "b", "c"}, spec)
	require.Empty(t, asked, "filling the menu resolves nothing")

	u.move(1, 8)
	v := u.view(8)
	require.Equal(t, []string{"b"}, v.info)
	require.Equal(t, []string{"b"}, asked)
}

func TestMenu_PicksTheVisibleRowADigitIsDrawnBeside(t *testing.T) {
	spec := wordSpec(noIcon)
	spec.digits = digitRow
	items := make([]string, 12)
	u := fillMenu(items, spec)

	i, ok := u.pick("2", 10)
	require.True(t, ok)
	require.Equal(t, 1, i)
	i, ok = u.pick("0", 10)
	require.True(t, ok)
	require.Equal(t, 9, i, "0 is the tenth row")

	u.top = 2
	i, ok = u.pick("1", 10)
	require.True(t, ok)
	require.Equal(t, 2, i, "a digit means the line it is drawn on")

	_, ok = u.pick("5", 3)
	require.False(t, ok, "a digit past the rows on screen reaches nothing")
	_, ok = u.pick("x", 10)
	require.False(t, ok)

	short := fillMenu([]string{"a", "b"}, spec)
	_, ok = short.pick("3", 10)
	require.False(t, ok, "nor does one past the list")
	_, ok = fillMenu([]string{"a"}, wordSpec(noIcon)).pick("1", 10)
	require.False(t, ok, "a menu without digits reads none")
}

func TestOfferRow_TintsTheCursorRowRatherThanMarkingIt(t *testing.T) {
	m := newPumModel(t)
	u := fillMenu([]string{"copy"}, wordSpec(noIcon))
	marked := m.joinSegsWidth(m.offerRow(u.rows[0], u.cols, 0, true))
	require.Contains(t, marked, "\x1b[38;2;31;35;41;48;2;231;238;252m", "the selected row's text wears the client's selection colour")
	require.Contains(t, marked, "231;238;252")
	plain := m.joinSegsWidth(m.offerRow(u.rows[0], u.cols, 0, false))
	require.NotContains(t, plain, "231;238;252", "an unselected row wears no tint")
}
func TestOfferRow_DrawsTheNumberColumnOnlyWithDigits(t *testing.T) {
	m := newPumModel(t)
	spec := wordSpec(noIcon)
	spec.digits = digitRow
	u := fillMenu([]string{"a", "b"}, spec)
	require.Equal(t, "2 b", ansi.Strip(m.joinSegsWidth(m.offerRow(u.rows[1], u.cols, 1, false))))
	require.Equal(t, "0 b", ansi.Strip(m.joinSegsWidth(m.offerRow(u.rows[1], u.cols, 9, false))), "the tenth row is 0")
	require.Equal(t, "  b", ansi.Strip(m.joinSegsWidth(m.offerRow(u.rows[1], u.cols, 10, false))), "past ten no digit reaches it")
}
