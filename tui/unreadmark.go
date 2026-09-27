package tui

import (
	"image"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// The Unread row's mark is the client's own ChatUnreadOutlined, the glyph its
// sidebar gives the Unread entry, from
//
//	Lark Framework.framework/Versions/Current/Resources/webcontent/main-window.asar.
//
// The client ships it as an SVG path. The numbers below are that path's own
// geometry in the 24-unit box the icon set draws in, kept as geometry rather
// than as a rendering so the mark stays crisp at whatever pixel size the cell
// grid asks for.
const (
	markOuter = 10.5
	markInner = 8.5
	markDotR  = 3
	// markGapR is how far the ring is cut back around the dot. The gap is
	// what tells this glyph from a plain ring, so it is wide enough that the
	// two read as separate ink instead of one thickened band.
	markGapR = 6.5
	// markSpan is the glyph's own content box, (1.5, 1.5) to (22.5, 22.5) of
	// the 24-unit one. The mark is drawn to fill its cells rather than to
	// keep the padding an icon beside a label needs.
	markSpan = 21
)

// markPt is a point in that 24-unit box.
type markPt struct{ x, y float64 }

var (
	markCentre = markPt{12, 12}
	markDotAt  = markPt{19.5, 5.5}
	// markTailOut and markTailIn are the bubble's lower-left corner, the one
	// part of the outline that is not its circle: a straight run out to the
	// tip, the tip's own rounding flattened to the three points that carry
	// it, and a flat bottom back. Each closes through the centre so that
	// taking it together with the circle leaves no seam along the join.
	markTailOut = []markPt{
		{4.446, 19.224}, {3.39, 20.732}, {3.188, 21.325}, {3.324, 21.898},
		{3.735, 22.33}, {4.353, 22.5}, {11.765, 22.5}, {12, 12},
	}
	markTailIn = []markPt{{5.88, 17.829}, {7.033, 19.014}, {5.995, 20.5}, {12, 20.5}, {12, 12}}
)

// markSamples is the grid a pixel is sampled on. The mark is a few dozen
// pixels across, where a rim that steps is the whole of what a reader sees.
const markSamples = 4

// markGrey is the ink of a mark standing for no chat and no person. It is the
// client's own tertiary text colour, fixed rather than shaded from the
// terminal's palette: a picture carries its own pixels, so this one has to
// read on a light background and a dark one alike.
var markGrey = color.RGBA{R: 0x8F, G: 0x95, B: 0x9E, A: 0xFF}

// unreadMark is the Unread row's picture. The glyph is drawn on nothing, so
// the terminal's own background fills it whatever the reader's theme makes
// that — the same reason the thread mark is matted off its white disc.
//
// It carries no counter although the row has one. The number is the sum over
// the rows below, each of which already badges its own picture, so a badge
// here would count the same messages a second time on the same screen; the
// row states the total in its text half instead.
func unreadMark(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	unit := float64(min(w, h)) / markSpan
	ox, oy := float64(w)/2-markCentre.x*unit, float64(h)/2-markCentre.y*unit
	for y := range h {
		for x := range w {
			n := 0
			for sy := range markSamples {
				for sx := range markSamples {
					p := markPt{
						(float64(x) + (float64(sx)+0.5)/markSamples - ox) / unit,
						(float64(y) + (float64(sy)+0.5)/markSamples - oy) / unit,
					}
					if markInk(p) {
						n++
					}
				}
			}
			if n == 0 {
				continue
			}
			cover := float64(n) / (markSamples * markSamples)
			i := m.PixOffset(x, y)
			// color.RGBA is alpha-premultiplied, so every channel scales.
			for k, c := range [4]uint8{markGrey.R, markGrey.G, markGrey.B, markGrey.A} {
				m.Pix[i+k] = uint8(float64(c) * cover)
			}
		}
	}
	return m
}

// markInk reports whether p is ink. The dot is tested before the ring because
// it sits on the ring's own midline: the gap that separates them is cut out
// of the band around the dot, not beside it.
func markInk(p markPt) bool {
	switch d := math.Hypot(p.x-markDotAt.x, p.y-markDotAt.y); {
	case d <= markDotR:
		return true
	case d <= markGapR:
		return false
	}
	return markBody(p, markOuter, markTailOut) && !markBody(p, markInner, markTailIn)
}

// markBody reports whether p lies within one of the bubble's two outlines,
// each a circle of radius r with its tail added.
func markBody(p markPt, r float64, tail []markPt) bool {
	return math.Hypot(p.x-markCentre.x, p.y-markCentre.y) <= r || markInPoly(p, tail)
}

// markInPoly counts what a ray cast to p's right crosses.
func markInPoly(p markPt, poly []markPt) bool {
	in := false
	for i, a := range poly {
		b := poly[(i+1)%len(poly)]
		if (a.y > p.y) == (b.y > p.y) {
			continue
		}
		if p.x < a.x+(p.y-a.y)/(b.y-a.y)*(b.x-a.x) {
			in = !in
		}
	}
	return in
}

// unreadGlyph stands in for the mark on a terminal that cannot be sent a
// picture. No single codepoint carries the bubble and its dot, so the ring
// goes alone. The colour block a chat falls back to is not available here: it
// is shaded from a chat id this row does not have and carries the first
// letter of a name it does not have either.
const unreadGlyph = "○"

// unreadTextCells are the two lines the glyph occupies, the ring sitting on
// the line the title is on.
func unreadTextCells() (top, bottom string) {
	pad := avatarWidth - lipgloss.Width(unreadGlyph)
	left := pad / 2
	return stDim.Render(strings.Repeat(" ", left) + unreadGlyph + strings.Repeat(" ", pad-left)),
		strings.Repeat(" ", avatarWidth)
}
