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
var systemFonts = []string{
	"/System/Library/Fonts/Hiragino Sans GB.ttc",
	"/System/Library/Fonts/Supplemental/Songti.ttc",
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
}

// avatarInitials is how many characters a generated avatar carries. Up to
// four sit in a 2x2 grid, which is how the Feishu client draws a group with
// no picture of its own.
const avatarInitials = 4

// generatedPalette are the background colours, dark enough for white glyphs.
var generatedPalette = []color.RGBA{
	{R: 0xC0, G: 0x39, B: 0x2B, A: 0xFF},
	{R: 0x27, G: 0x8B, B: 0x50, A: 0xFF},
	{R: 0xB7, G: 0x7A, B: 0x1F, A: 0xFF},
	{R: 0x2E, G: 0x6D, B: 0xB8, A: 0xFF},
	{R: 0x8E, G: 0x44, B: 0xAD, A: 0xFF},
	{R: 0x16, G: 0x8A, B: 0x8A, A: 0xFF},
}

// avatarFont is the parsed font, resolved once. A machine with none of them
// yields nil, which sends callers back to the colour block.
var avatarFont = sync.OnceValue(func() *sfnt.Font {
	for _, path := range systemFonts {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := parseFace(b)
		if err != nil {
			continue
		}
		return f
	}
	return nil
})

// avatarFace builds a face at the size one glyph gets, which depends on how
// many share the square and how large that square is on screen.
func avatarFace(glyphs, side int) font.Face {
	size := float64(side) * 0.56 // one glyph, alone
	if glyphs > 1 {
		size = float64(side) * 0.33 // a 2x2 cell, clear of the rounded edge
	}
	return faceAt(size)
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

// parseFace reads either a single font or the first face of a collection.
func parseFace(b []byte) (*sfnt.Font, error) {
	if f, err := sfnt.Parse(b); err == nil {
		return f, nil
	}
	col, err := sfnt.ParseCollection(b)
	if err != nil {
		return nil, err
	}
	return col.Font(0)
}

// generateAvatar draws a stand-in picture for a chat with no avatar file: a
// colour square carrying the first characters of its name. Returns nil when
// no usable font was found, which leaves the colour block in charge.
func generateAvatar(name string, hash uint32, w, h int) *image.RGBA {
	text := []rune(initials(name))
	face := avatarFace(len(text), min(w, h))
	if face == nil {
		return nil
	}
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := generatedPalette[int(hash)%len(generatedPalette)]
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = bg.R, bg.G, bg.B, bg.A
	}
	for i, r := range text {
		drawGlyph(m, face, string(r), glyphCell(len(text), i, w, h))
	}
	roundCorners(m)
	return m
}

// cornerRadius is how far the corners are rounded, as a share of the shorter
// side. A full disc would cut into a 2x2 grid of glyphs; this leaves them
// whole while still softening the square.
const cornerRadius = 0.22

// roundCorners clips the rectangle to a rounded one, so a drawn avatar sits
// among the real ones rather than standing out as the only hard square. The
// terminal shows through the cleared corners, which is why the edge fades
// rather than stepping: there is no background colour here to blend against.
func roundCorners(m *image.RGBA) {
	b := m.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	r := math.Min(w, h) * cornerRadius
	// Half-extents of the rectangle the corner arcs are centred on.
	ex, ey := w/2-r, h/2-r
	for y := range b.Dy() {
		for x := range b.Dx() {
			dx := math.Max(math.Abs(float64(x)+0.5-w/2)-ex, 0)
			dy := math.Max(math.Abs(float64(y)+0.5-h/2)-ey, 0)
			// Coverage over the last pixel of the edge, which is all the
			// anti-aliasing a shape this size needs.
			cover := r - math.Hypot(dx, dy) + 0.5
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

// glyphCell is the square one glyph owns: the whole avatar when it is alone,
// a half-width column for two, a 2x2 quadrant beyond that.
func glyphCell(count, i, w, h int) image.Rectangle {
	switch {
	case count <= 1:
		return image.Rect(0, 0, w, h)
	case count == 2:
		return image.Rect(i*w/2, 0, (i+1)*w/2, h)
	default:
		col, row := i%2, i/2
		return image.Rect(col*w/2, row*h/2, (col+1)*w/2, (row+1)*h/2)
	}
}

// drawGlyph centres one glyph in cell, on the face's own ascent and descent
// so a character without descenders still sits in the middle.
func drawGlyph(dst *image.RGBA, face font.Face, s string, cell image.Rectangle) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(color.White), Face: face}
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
	drawGlyph(m, face, label, rect)
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
