package tui

import "github.com/charmbracelet/x/ansi"

// A cut and a measurement disagree about one shape. ansi.Truncate starts a
// grapheme cluster only on a non-ASCII lead byte, so a keycap emoji — an
// ASCII digit, a variation selector and U+20E3 — is one cell to it and two to
// ansi.StringWidth, which is what lipgloss, the panes and the terminal all
// count in. A row cut by the first and padded to width by the second comes
// back a column too wide, and a pane is drawn as wide as its widest row: one
// such row pushes the whole frame past the terminal, where every line wraps.
// ansi.TruncateLeft breaks the same cluster the other way, dropping the whole
// of it for the one column it was asked for.
//
// Both are corrected the same way: ask, measure what came back, and ask again
// for as much as the answer was out by. Where the columns asked for fall
// inside a cluster, both err towards the smaller answer: half a keycap is not
// a thing a terminal can draw, and it is keeping a column too many that costs
// a pane its width.

// cut is ansi.Truncate measured in the columns the terminal will draw.
func cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	for n := w; n > 0; {
		out := ansi.Truncate(s, n, "")
		over := ansi.StringWidth(out) - w
		if over <= 0 {
			return out
		}
		n -= over
	}
	return ""
}

// cutLeft drops w columns off the front of s, measured the same way.
func cutLeft(s string, w int) string {
	if w <= 0 {
		return s
	}
	want := ansi.StringWidth(s) - w
	if want <= 0 {
		return ""
	}
	// What is left only shrinks as n grows, so the answer is the smallest n
	// whose remainder fits: ask for w, then walk whichever way it came out.
	n := w
	for ansi.StringWidth(ansi.TruncateLeft(s, n, "")) > want {
		n++
	}
	for n > 0 && ansi.StringWidth(ansi.TruncateLeft(s, n-1, "")) <= want {
		n--
	}
	return ansi.TruncateLeft(s, n, "")
}
