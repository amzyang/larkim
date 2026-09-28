package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// A completion list is drawn as a layer over the panes rather than as rows
// inside the composer, so the conversation stands still while the reader types
// into it. The list grows upward out of the word it completes — the composer is
// at the bottom of the screen, which is the one direction a popup menu there
// can open in.

// floater is a completion list placed on the screen: the block to draw and the
// cell it starts at. A list with nothing to offer, or no room above the box to
// offer it in, produces none.
type floater struct {
	block string
	x, y  int
	w, h  int
}

// covers reports whether a screen cell belongs to the popup. The mouse asks,
// because hit() reads the grid the popup is drawn over rather than the popup.
func (f floater) covers(x, y int) bool {
	return x >= f.x && x < f.x+f.w && y >= f.y && y < f.y+f.h
}

// floatRoom is how many of n offers the pane can spare room for. They come out
// of the pane the popup covers rather than out of the composer's budget, so
// opening the popup neither moves the panes nor starves the preview. The pane
// keeps its head — the title row is the only thing naming the chat the popup
// stands over — and the popup pays for its own border.
func (m Model) floatRoom(n int) int {
	return clamp(n, 0, min(pumMaxRows, m.bodyHeight()-msgHeaderHeight-1))
}

// floatSegs is the offers the popup draws, each in the pieces it was measured
// from. Drawing and measuring come from one pass so the box cannot be sized for
// a row it does not draw.
func (m Model) floatSegs() [][]rowSeg {
	var out [][]rowSeg
	switch m.mode {
	case modeInsert:
		for i, h := range m.pumVisible() {
			out = append(out, m.offerSegs(h.emoji, h.label, m.pum.top+i == m.pum.idx))
		}
	case modeCommand:
		for i, h := range m.cmdCompVisible() {
			out = append(out, m.offerSegs(h.emoji, h.label, m.cmdcomp.top+i == m.cmdcomp.idx))
		}
	}
	return out
}

// floatAnchor is the screen column the run being completed starts at, which is
// the column the popup's offers line up under.
func (m Model) floatAnchor() int {
	if m.mode == modeCommand {
		// The field the list rewrites, rather than the caret: walking the list
		// writes into the line, and a box that moved with what it wrote would
		// chase the reader's own choices across the screen.
		line := []rune(m.cmdline.Value())
		head := string(line[:min(m.cmdcomp.start, len(line))])
		return m.bandLeft(m.cmdSide()) + 1 + lipgloss.Width(m.cmdline.Prompt) + lipgloss.Width(head)
	}
	// bubbles reports no cursor for a blurred widget, and the popup is asked
	// for while the frame is drawn rather than while the key is handled.
	ta := m.area()
	ta.Focus()
	c := ta.Cursor()
	if c == nil {
		return m.bandLeft(m.side) + 1
	}
	left, w := m.bandLeft(m.side), m.bandWidth(m.side)-2
	caret := min(c.X+left+1, left+w)
	line := []rune(lineBeforeCursor(ta))
	run := string(line[max(0, len(line)-m.pum.run.runes):])
	return caret - lipgloss.Width(run)
}

// floater places the popup. It is the last thing drawn, over everything the
// frame put under it.
func (m Model) floater() (floater, bool) {
	segs := m.floatSegs()
	if len(segs) == 0 {
		return floater{}, false
	}
	w := 0
	for _, s := range segs {
		w = max(w, segsWidth(s))
	}
	w = min(w, m.width-2)
	lines := make([]string, len(segs))
	for i, s := range segs {
		lines[i] = m.joinSegs(s, w)
	}
	f := floater{
		block: paneStyle(true, w).Render(strings.Join(lines, "\n")),
		w:     w + 2,
		h:     len(lines) + 2,
	}
	// The popup's bottom border lands on the pane's, so the two read as one
	// line with a notch in it and the offers stand one row off the composer.
	// Dropping it a row further would put a row of offers inside the pane's
	// border instead, and a row further still would break the box the reader
	// is typing in.
	f.y = m.bodyHeight() + 2 - f.h
	// One column left of the anchor, so the offers — not the border — line up
	// under the run. A box that would hang off the edge is pushed back on.
	f.x = clamp(m.floatAnchor()-1, 0, max(0, m.width-f.w))
	return f, true
}

// floatAt reports whether a screen cell belongs to an open popup.
func (m Model) floatAt(x, y int) bool {
	f, ok := m.floater()
	return ok && f.covers(x, y)
}

// walkFloat moves the popup's cursor, for the wheel. The keys reach the lists
// through their own handlers, which is where the rest of their keymaps live;
// the pointer has no mode to be in, so it comes here.
func (m Model) walkFloat(d int) Model {
	switch m.mode {
	case modeInsert:
		m.pum.move(d, m.pumRows())
	case modeCommand:
		m = m.walkCmdComp(d)
	}
	return m
}

// floatOver draws the popup over the frame. Compositing runs only while a list
// is open: every other frame is the plain join it has always been.
func (m Model) floatOver(frame string) string {
	f, ok := m.floater()
	if !ok {
		return frame
	}
	cv := lipgloss.NewCanvas(m.width, m.height)
	cv.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(frame),
		lipgloss.NewLayer(f.block).X(f.x).Y(f.y).Z(1),
	))
	return cv.Render()
}
