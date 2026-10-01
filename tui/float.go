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
func (m Model) floatRoom(n, maxRows int) int {
	return clamp(n, 0, min(maxRows, m.floatCeiling()))
}

// floatCeiling is the most rows a float may stand over the pane with.
func (m Model) floatCeiling() int { return m.bodyHeight() - msgHeaderHeight - 1 }

// floatMenu is the list standing over the panes, if one is: the mode is what
// says which, so a list left behind by a mode the reader has moved on from is
// never drawn.
func (m Model) floatMenu() (menuView, bool) {
	var v menuView
	switch m.mode {
	case modeInsert:
		v = m.pum.menu.view(m.pumRows())
	case modeCommand:
		v = m.cmdcomp.menu.view(m.cmdCompRows())
	case modeCandidates:
		v = m.cand.view(m.candRows())
	case modeEmoji:
		v = m.picker.menu.view(m.reactRows())
	default:
		return menuView{}, false
	}
	return v, len(v.rows) > 0
}

// floatSegs is the offers the popup draws, each in the pieces it was measured
// from. Drawing and measuring come from one pass so the box cannot be sized for
// a row it does not draw.
func (m Model) floatSegs() [][]rowSeg {
	v, ok := m.floatMenu()
	if !ok {
		return nil
	}
	out := make([][]rowSeg, len(v.rows))
	for i, o := range v.rows {
		out[i] = m.offerRow(o, v.cols, i, i == v.sel)
	}
	return out
}

// floatAnchor is the screen column the run being completed starts at, which is
// the column the popup's offers line up under.
func (m Model) floatAnchor() int {
	if m.mode == modeCandidates || m.mode == modeEmoji {
		// Nothing is being completed, so the list lines up with the box it
		// stands over — the draft a pick replaces for the one, the query the
		// reactions are narrowed by for the other.
		return m.bandLeft(m.side) + 1
	}
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
		block: paneStyle(true).Render(strings.Join(lines, "\n")),
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

const (
	// infoMaxCols is as wide as the box beside a list gets: a line of prose,
	// not a pane.
	infoMaxCols = 48
	// infoMinCols is the narrowest that box is wrapped to when what it says
	// fits neither side. Under it a line holds a word or two, and the box is
	// dropped rather than read a word at a time, which is neovim's rule for its
	// own (it gives up under ten).
	infoMinCols = 12
)

// infoFloater places what the focused offer has to say beside the list:
// east of it where it fits, west where only that does, and otherwise on the
// side with more room. Its bottom border shares the list's, so the two grow
// up from one line.
func (m Model) infoFloater(menu floater) (floater, bool) {
	v, ok := m.floatMenu()
	if !ok || len(v.info) == 0 {
		return floater{}, false
	}
	want := 0
	for _, p := range v.info {
		want = max(want, lipgloss.Width(p))
	}
	want = min(want, infoMaxCols)
	// The room either side, less the box's own border.
	east := m.width - (menu.x + menu.w) - 2
	west := menu.x - 2
	cols, left := want, false
	switch {
	case east >= want:
	case west >= want:
		left = true
	case max(east, west) < infoMinCols:
		return floater{}, false
	case east >= west:
		cols = east
	default:
		cols, left = west, true
	}
	var lines []string
	for _, p := range v.info {
		lines = append(lines, wrap(p, cols)...)
	}
	room := m.floatCeiling()
	if room <= 0 {
		return floater{}, false
	}
	if len(lines) > room {
		lines = lines[:room]
		last := strings.TrimRight(lines[room-1], " ")
		lines[room-1] = fit(truncate(last+"…", cols), cols)
	}
	f := floater{
		block: paneStyle(true).Render(strings.Join(lines, "\n")),
		w:     cols + 2,
		h:     len(lines) + 2,
	}
	f.y = menu.y + menu.h - f.h
	f.x = menu.x + menu.w
	if left {
		f.x = menu.x - f.w
	}
	return f, true
}

// floatAt reports whether a screen cell belongs to an open popup, the box
// beside it included.
func (m Model) floatAt(x, y int) bool {
	f, ok := m.floater()
	if !ok {
		return false
	}
	info, ok := m.infoFloater(f)
	return f.covers(x, y) || ok && info.covers(x, y)
}

// walkFloat moves the popup's cursor, for the wheel. The keys reach the lists
// through their own handlers, which is where the rest of their keymaps live;
// the pointer has no mode to be in, so it comes here.
func (m Model) walkFloat(d int) Model {
	switch m.mode {
	case modeInsert:
		m.pum.menu.move(d, m.pumRows())
	case modeCommand:
		m = m.walkCmdComp(d)
	case modeCandidates:
		m.cand.move(d, m.candRows())
	case modeEmoji:
		m.picker.menu.move(d, m.reactRows())
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
	layers := []*lipgloss.Layer{
		lipgloss.NewLayer(frame),
		lipgloss.NewLayer(f.block).X(f.x).Y(f.y).Z(1),
	}
	if info, ok := m.infoFloater(f); ok {
		layers = append(layers, lipgloss.NewLayer(info.block).X(info.x).Y(info.y).Z(1))
	}
	cv := lipgloss.NewCanvas(m.width, m.height)
	cv.Compose(lipgloss.NewCompositor(layers...))
	return cv.Render()
}
