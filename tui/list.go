package tui

// A chooser — the completion popup, the forward list, the open-target list —
// is a list too long to draw whole, shown through a window onto it. idx is the
// row the cursor is on and top the row the window starts at; the two helpers
// here are the only things that have to agree on how they relate.

// moveCursor walks a cursor by d over n rows, scrolling the window only as far
// as it must to keep the cursor inside its rows.
func moveCursor(idx, top *int, d, n, rows int) {
	if n == 0 {
		return
	}
	*idx = clamp(*idx+d, 0, n-1)
	*top = clamp(*top, max(0, *idx-rows+1), *idx)
}

// window is the run of items a list scrolled to top shows in rows rows.
func window[T any](items []T, top, rows int) []T {
	lo := min(top, len(items))
	return items[lo:min(len(items), lo+rows)]
}
