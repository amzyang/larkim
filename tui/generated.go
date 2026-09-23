package tui

import (
	"image"
	"image/color"
	"math"
	"os"
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
	f := avatarFont()
	if f == nil {
		return nil
	}
	size := float64(side) * 0.56 // one glyph, alone
	if glyphs > 1 {
		size = float64(side) * 0.33 // a 2x2 cell, clear of the rounded edge
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
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
func generateAvatar(name string, hash uint32, w, h int) image.Image {
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
