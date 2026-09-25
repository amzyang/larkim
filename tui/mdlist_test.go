package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// mdBody draws one markdown body at the width the messages pane leaves it.
func mdBody(body string, inner int) []msgRow {
	x := store.Message{MessageID: "om_a", MsgType: "post", Content: body, RenderedAt: 1}
	st := msgStyle{width: inner + leadWidth, height: 40}
	var b block
	g := leads{b: &b}
	return mdRows(body, x, 0, st, &g, mentions{})
}

// rowPlain is a row's text without the styling, however the row holds it.
func rowPlain(r msgRow) string {
	if len(r.segs) == 0 {
		return ansi.Strip(r.text)
	}
	var b strings.Builder
	for _, s := range r.segs {
		b.WriteString(ansi.Strip(s.text))
	}
	return b.String()
}

func TestMdRows_ANumberedItemKeepsEveryColumnItsMarkerTakes(t *testing.T) {
	const inner = 56
	// The link is what puts the item on the piece-wise path, which is where a
	// marker wider than the indent it replaces costs the row a column.
	body := "1. " + strings.Repeat("字", 30) + " https://example.com/a"
	var text strings.Builder
	for _, r := range mdBody(body, inner) {
		require.LessOrEqual(t, ansi.StringWidth(rowPlain(r)), inner, "row %q", rowPlain(r))
		text.WriteString(rowPlain(r))
	}
	require.Equal(t, 30, strings.Count(text.String(), "字"), "no character may be cut off the item")
	require.Contains(t, text.String(), "https://example.com/a")
}

func TestMdRows_EveryItemOfAListStartsAtOneColumn(t *testing.T) {
	var body strings.Builder
	for i := 1; i <= 11; i++ {
		body.WriteString(strconv.Itoa(i) + ". 项目\n")
	}
	rows := mdBody(body.String(), 56)
	require.Len(t, rows, 11)
	col := -1
	for _, r := range rows {
		w := textColumn(t, rowPlain(r), "项目")
		if col < 0 {
			col = w
		}
		require.Equal(t, col, w, "every item's text starts at the same column: %q", rowPlain(r))
	}
}

// textColumn is the column want starts at in a drawn row.
func textColumn(t *testing.T, line, want string) int {
	t.Helper()
	at := strings.Index(line, want)
	require.GreaterOrEqual(t, at, 0, "row %q holds %q", line, want)
	return ansi.StringWidth(line[:at])
}

func TestMdRows_ABulletedItemIsIndentedAsItWas(t *testing.T) {
	rows := mdBody("- 项目\n- 项目\n", 56)
	require.Len(t, rows, 2)
	for _, r := range rows {
		require.Equal(t, mdIndent, textColumn(t, rowPlain(r), "项目"),
			"a bullet costs the same columns it always did")
	}
}

func TestMdRows_ANestedListIsIndentedByItsParentsMarker(t *testing.T) {
	rows := mdBody("1. 项目\n   1. 内层\n", 56)
	require.Len(t, rows, 2)
	require.Equal(t, 3, textColumn(t, rowPlain(rows[1]), "1."),
		"the nested list opens where its parent's text does")
}
