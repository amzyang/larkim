package tui

import (
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// systemFonts are tried in order for the glyphs drawn onto a generated
// avatar. They are read from the machine rather than embedded because a CJK
// face is tens of megabytes, and this only ever runs on macOS. STHeiti is
// left out: x/image/font/sfnt rejects its cmap.
//
// Each entry names the bold face of its collection. Four glyphs sharing a
// disc the terminal sized leaves each about eleven pixels, where a regular
// weight's stems fall under one pixel and grey out; x/image rasterises the
// outline without running the font's hinting bytecode, so nothing snaps them
// back onto the grid.
var systemFonts = []struct {
	path string
	face int
}{
	{"/System/Library/Fonts/Hiragino Sans GB.ttc", 2},    // W6
	{"/System/Library/Fonts/Supplemental/Songti.ttc", 1}, // SC Bold
	{"/System/Library/Fonts/Supplemental/Arial Unicode.ttf", 0},
}

// avatarInitials is how many characters of a name the picture carries. Up to
// four sit in a 2x2 grid, which is how the Feishu client draws one.
const avatarInitials = 4

// generatedPalette are the accents a drawn avatar takes, the ten the Feishu
// client offers a group picking its own disc. Four are darkened from the
// client's own values: it can afford a lighter accent because the glyph it
// pairs them with is a fat pictogram, not hairline CJK strokes at a third of
// the disc. Every one here clears 4.1:1 against white, which holds whichever
// side of the pair the white lands on.
var generatedPalette = []color.RGBA{
	{R: 0x35, G: 0x6F, B: 0xF5, A: 0xFF},
	{R: 0x60, G: 0x69, B: 0xEC, A: 0xFF},
	{R: 0x15, G: 0x85, B: 0xB2, A: 0xFF},
	{R: 0x17, G: 0x8C, B: 0x7B, A: 0xFF},
	{R: 0x2B, G: 0x8F, B: 0x38, A: 0xFF},
	{R: 0xC2, G: 0x60, B: 0x0E, A: 0xFF},
	{R: 0xE7, G: 0x3B, B: 0x34, A: 0xFF},
	{R: 0xD5, G: 0x3D, B: 0x90, A: 0xFF},
	{R: 0xC0, G: 0x42, B: 0xC1, A: 0xFF},
	{R: 0x8D, G: 0x53, B: 0xEF, A: 0xFF},
}

// avatarWhite is the other half of every pair: the glyphs of the filled
// style, the body of the outlined one.
var avatarWhite = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

// avatarFont is the parsed font, resolved once. A machine with none of them
// yields nil, which sends callers back to the colour block.
var avatarFont = sync.OnceValue(func() *sfnt.Font {
	for _, sf := range systemFonts {
		b, err := os.ReadFile(sf.path)
		if err != nil {
			continue
		}
		f, err := parseFace(b, sf.face)
		if err != nil {
			continue
		}
		return f
	}
	return nil
})

// avatarFace builds a face at the size one glyph gets and reports that size,
// which is also the cell the glyph is laid out in. Each tier keeps the block
// they form inside the disc: the more glyphs share one, the wider the block
// and so the smaller they go.
func avatarFace(glyphs, side int) (font.Face, int) {
	size := float64(side) * 0.54 // one glyph, alone
	switch {
	case glyphs > 2:
		size = float64(side) * 0.29 // one cell of the 2x2 grid
	case glyphs == 2:
		size = float64(side) * 0.34 // one of a pair, clear of the rim
	}
	return faceAt(size), int(math.Round(size))
}

// faceAt builds a face whose em box is px pixels tall.
func faceAt(px float64) font.Face {
	f := avatarFont()
	if f == nil {
		return nil
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	return face
}

// parseFace reads either a single font or one face of a collection.
func parseFace(b []byte, face int) (*sfnt.Font, error) {
	if f, err := sfnt.Parse(b); err == nil {
		return f, nil
	}
	col, err := sfnt.ParseCollection(b)
	if err != nil {
		return nil, err
	}
	return col.Font(face)
}

// generateAvatar draws a stand-in picture for a chat with no avatar file, a
// disc carrying the first characters of its name. outlined picks the second
// of the two styles the client offers — white body, accent rim, accent
// glyphs — which is what a group gets; a person or a bot gets the filled one,
// so a stand-in never reads as the wrong kind of chat. Returns nil when no
// usable font was found, which leaves the colour block in charge.
func generateAvatar(name string, hash uint32, w, h int, outlined bool) *image.RGBA {
	text := []rune(initials(name))
	face, em := avatarFace(len(text), min(w, h))
	if face == nil {
		return nil
	}
	accent := generatedPalette[int(hash)%len(generatedPalette)]
	body, ink := accent, avatarWhite
	if outlined {
		body, ink = ink, accent
	}
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = body.R, body.G, body.B, body.A
	}
	for i, r := range text {
		drawGlyph(m, face, string(r), ink, glyphCell(len(text), i, w, h, em))
	}
	maskDisc(m)
	if outlined {
		strokeDisc(m, accent)
	}
	return m
}

// discRadius is the mask's radius as a share of the shorter side: a half is
// the full disc, the shape the client gives every avatar.
const discRadius = 0.5

// discEdge is how far x, y lies from the rim of the disc a w by h picture
// carries, positive inside and negative out. A box the cell grid made oblong
// keeps the disc's radius and rounds only what the shorter side reaches,
// which is what stops the circle turning into an ellipse.
func discEdge(w, h float64, x, y int) float64 {
	r := math.Min(w, h) * discRadius
	// Half-extents of the rectangle the corner arcs are centred on.
	ex, ey := w/2-r, h/2-r
	dx := math.Max(math.Abs(float64(x)+0.5-w/2)-ex, 0)
	dy := math.Max(math.Abs(float64(y)+0.5-h/2)-ey, 0)
	return r - math.Hypot(dx, dy)
}

// maskDisc clips the picture to the disc the client draws, so the list reads
// as one column of circles whether a chat brought a picture or had one drawn
// for it. The terminal shows through what is cleared, which is why the rim
// fades rather than stepping: there is no background colour here to blend
// against.
func maskDisc(m *image.RGBA) {
	b := m.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	for y := range b.Dy() {
		for x := range b.Dx() {
			// Coverage over the last pixel of the edge, which is all the
			// anti-aliasing a shape this size needs.
			cover := discEdge(w, h, x, y) + 0.5
			if cover >= 1 {
				continue
			}
			i := m.PixOffset(x, y)
			if cover <= 0 {
				m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = 0, 0, 0, 0
				continue
			}
			// color.RGBA is alpha-premultiplied, so every channel scales.
			for c := range 4 {
				m.Pix[i+c] = uint8(float64(m.Pix[i+c]) * cover)
			}
		}
	}
}

// ringWidth is the outlined style's rim as a share of the disc, the client's
// own proportion. The floor keeps it drawn at all on a disc a few dozen
// pixels across, where the share alone rounds away.
const ringWidth = 0.028

// strokeDisc lays the outlined style's rim over an already masked picture.
// The band is clipped to the disc on its outer side, so the rim inherits the
// anti-aliased edge the mask cut rather than stepping past it.
func strokeDisc(m *image.RGBA, c color.RGBA) {
	b := m.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	t := math.Max(1, math.Min(w, h)*ringWidth)
	for y := range b.Dy() {
		for x := range b.Dx() {
			d := discEdge(w, h, x, y)
			cover := math.Min(math.Min(d+0.5, 1), math.Min(t-d+0.5, 1))
			if cover <= 0 {
				continue
			}
			i := m.PixOffset(x, y)
			src := [4]uint8{c.R, c.G, c.B, c.A}
			// color.RGBA is alpha-premultiplied, so every channel blends the
			// same way.
			for k := range 4 {
				m.Pix[i+k] = uint8(float64(src[k])*cover + float64(m.Pix[i+k])*(1-cover))
			}
		}
	}
}

// glyphCell is the box one glyph owns: a cell the size of its own em, in a
// grid of at most two columns centred on the picture. Sizing the cell to the
// glyph rather than to a share of the picture is what holds the block
// together in the middle; quartering the picture instead spreads four glyphs
// until they graze the rim and leave a hole between them. A row the grid
// leaves short is centred within the block, so three characters do not sit
// lopsided.
func glyphCell(count, i, w, h, em int) image.Rectangle {
	cols := min(count, 2)
	rows := (count + cols - 1) / cols
	row, col := i/cols, i%cols
	x := (w-min(cols, count-row*cols)*em)/2 + col*em
	y := (h-rows*em)/2 + row*em
	return image.Rect(x, y, x+em, y+em)
}

// drawGlyph centres one glyph in cell, on the face's own ascent and descent
// so a character without descenders still sits in the middle.
func drawGlyph(dst *image.RGBA, face font.Face, s string, ink color.RGBA, cell image.Rectangle) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(ink), Face: face}
	mt := face.Metrics()
	x := cell.Min.X + (cell.Dx()-d.MeasureString(s).Round())/2
	y := cell.Min.Y + (cell.Dy()+(mt.Ascent-mt.Descent).Round())/2
	d.Dot = fixed.P(x, y)
	d.DrawString(s)
}

// initials is the leading characters of a name, skipping the punctuation many
// group names open with so the glyphs carry meaning.
func initials(name string) string {
	var out []rune
	for _, r := range flatten(name) {
		if isAvatarPunct(r) {
			continue
		}
		out = append(out, r)
		if len(out) == avatarInitials {
			break
		}
	}
	return string(out)
}

func isAvatarPunct(r rune) bool {
	switch r {
	case '【', '】', '[', ']', '(', ')', '（', '）', '<', '>', '《', '》',
		'-', '_', '·', '•', '#', '@', '*', '"', '\'', '“', '”', '|', '/', '\\':
		return true
	}
	return r == ' '
}

// badgeRed is the unread counter's disc; badgeGrey replaces it when the chat
// is on do-not-disturb. Both are the Feishu client's own shades, sampled from
// it, so the two windows read as the same product.
var (
	badgeRed  = color.RGBA{R: 0xFF, G: 0x4E, B: 0x4C, A: 0xFF}
	badgeGrey = color.RGBA{R: 0xBC, G: 0xC0, B: 0xC5, A: 0xFF}
)

const (
	// badgeHeight is the counter's height as a share of the avatar's shorter
	// side. Larger swallows the picture it sits on; smaller cannot hold two
	// digits. The shorter side is what keeps the proportion when a terminal's
	// cells make the avatar box an oblong rather than a square.
	badgeHeight = 0.40
	// badgeGlyph is the digit size inside that height.
	badgeGlyph = 0.78
	// badgePad is the width one digit beyond the first adds, so a pill keeps
	// the same air around its text that the circle has.
	badgePad = 0.45
	// badgeRing is the gap cleared around the counter, in pixels. The
	// terminal shows through it, which is what separates the counter from a
	// picture of any colour.
	badgeRing = 1.5
)

// badgeLabel is what the counter carries: the count up to 99, "99+" beyond,
// nothing when there is nothing unread.
func badgeLabel(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n > 99:
		return "99+"
	default:
		return strconv.FormatInt(n, 10)
	}
}

// drawBadge stamps the unread counter onto the picture's top-right corner,
// widening from a circle into a pill when the digits need it. It reports
// whether it drew, because the row repeats the number in its text half when
// the picture could not carry it.
func drawBadge(m *image.RGBA, n int64, muted bool) bool {
	label := badgeLabel(n)
	if label == "" {
		return false
	}
	b := m.Bounds()
	h := math.Min(float64(b.Dx()), float64(b.Dy())) * badgeHeight
	face := faceAt(h * badgeGlyph)
	if face == nil {
		return false
	}
	d := &font.Drawer{Face: face}
	w := math.Max(h, float64(d.MeasureString(label).Round())+h*badgePad)
	rect := image.Rect(b.Max.X-int(math.Round(w)), b.Min.Y, b.Max.X, b.Min.Y+int(math.Round(h)))

	fill := badgeRed
	if muted {
		fill = badgeGrey
	}
	fillRounded(m, rect.Inset(-int(math.Ceil(badgeRing))), h/2+badgeRing, color.RGBA{})
	fillRounded(m, rect, h/2, fill)
	drawGlyph(m, face, label, avatarWhite, rect)
	return true
}

// fillRounded paints a rounded rectangle with an anti-aliased edge, clipped
// to the image. A transparent colour erases rather than paints, which is how
// the counter cuts its ring out of whatever it sits on.
func fillRounded(m *image.RGBA, r image.Rectangle, radius float64, c color.RGBA) {
	cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
	// Half-extents of the rectangle the corner arcs are centred on.
	ex, ey := float64(r.Dx())/2-radius, float64(r.Dy())/2-radius
	b := m.Bounds()
	for y := max(r.Min.Y, b.Min.Y); y < min(r.Max.Y, b.Max.Y); y++ {
		for x := max(r.Min.X, b.Min.X); x < min(r.Max.X, b.Max.X); x++ {
			dx := math.Max(math.Abs(float64(x)+0.5-cx)-ex, 0)
			dy := math.Max(math.Abs(float64(y)+0.5-cy)-ey, 0)
			cover := math.Min(radius-math.Hypot(dx, dy)+0.5, 1)
			if cover <= 0 {
				continue
			}
			i := m.PixOffset(x, y)
			src := [4]uint8{c.R, c.G, c.B, c.A}
			// color.RGBA is alpha-premultiplied, so every channel blends the
			// same way.
			for k := range 4 {
				m.Pix[i+k] = uint8(float64(src[k])*cover + float64(m.Pix[i+k])*(1-cover))
			}
		}
	}
}
