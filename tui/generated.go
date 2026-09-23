package tui

import (
	"image"
	"image/color"
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
// many share the square.
func avatarFace(glyphs int) font.Face {
	f := avatarFont()
	if f == nil {
		return nil
	}
	size := avatarPixels * 0.62 // one glyph, alone
	if glyphs > 1 {
		size = avatarPixels * 0.38 // a 2x2 cell, with room to breathe
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
func generateAvatar(name string, hash uint32) image.Image {
	text := []rune(initials(name))
	face := avatarFace(len(text))
	if face == nil {
		return nil
	}
	m := image.NewRGBA(image.Rect(0, 0, avatarPixels, avatarPixels))
	bg := generatedPalette[int(hash)%len(generatedPalette)]
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = bg.R, bg.G, bg.B, bg.A
	}
	for i, r := range text {
		drawGlyph(m, face, string(r), glyphCell(len(text), i))
	}
	return m
}

// glyphCell is the square one glyph owns: the whole avatar when it is alone,
// a half-width column for two, a 2x2 quadrant beyond that.
func glyphCell(count, i int) image.Rectangle {
	half := avatarPixels / 2
	switch {
	case count <= 1:
		return image.Rect(0, 0, avatarPixels, avatarPixels)
	case count == 2:
		return image.Rect(i*half, 0, (i+1)*half, avatarPixels)
	default:
		col, row := i%2, i/2
		return image.Rect(col*half, row*half, (col+1)*half, (row+1)*half)
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
