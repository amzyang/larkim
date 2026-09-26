package tui

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"sync"

	"golang.org/x/image/draw"
)

// threadMarkPNG is the mark the client gives a thread in its own list: its
// topic glyph inside a ring, with the chat's picture on the corner. It is the
// client's own file, from
//
//	Lark Framework.framework/Versions/Current/Resources/webcontent/resource.asar,
//	assets/img/80f6791e2e.png
//
// matted off the white disc it was drawn on — one teal over white, so each
// pixel's coverage comes off the red channel — because a terminal has a
// background of its own and a white disc in the avatar column would be a hole
// punched in the reader's theme.
//
//go:embed threadmark.png
var threadMarkPNG []byte

// threadMark decodes that file once. A mark that will not decode leaves the
// row its colour block, the same fallback a chat with no picture takes.
var threadMark = sync.OnceValue(func() image.Image {
	img, err := png.Decode(bytes.NewReader(threadMarkPNG))
	if err != nil {
		return nil
	}
	return img
})

const (
	// threadBadge is the chat's share of the mark's shorter side. The client
	// draws the badge at half the ring's own diameter; the ring fills this box
	// rather than sitting inside a larger one, so its share of the box is that
	// same half, less the clearing punched around it.
	threadBadge = 0.50
	// threadBadgeGap is the ring punched around that badge, as a share of it.
	// The client draws a white ring there; a hole lets the terminal's own
	// background be the ring, whatever the reader's theme makes it.
	threadBadgeGap = 0.08
)

// threadAvatar is the client's thread mark carrying one chat's picture: a
// conversation inside a chat, drawn as the one thing it is. chat is drawn at
// the badge's own size rather than scaled down from the column's, so its rim
// is cut once instead of resampled twice.
func threadAvatar(mark image.Image, chat *image.RGBA, w, h int) *image.RGBA {
	m := scaleImage(mark, w, h, draw.CatmullRom)
	if chat == nil {
		return m
	}
	side := chat.Bounds().Dx()
	gap := max(1, int(float64(side)*threadBadgeGap))
	// Inset by the gap, so the ring punched around the badge is drawn on the
	// outer side too and the badge sits in a clearing rather than against the
	// edge of its cells.
	at := image.Pt(w-side-gap, h-side-gap)
	// A transparent fill erases, which is how the counter cuts its own ring;
	// here it cuts the gap that parts the badge from the ring behind it.
	hole := image.Rect(at.X, at.Y, at.X+side, at.Y+side).Inset(-gap)
	fillRounded(m, hole, float64(hole.Dx())/2, color.RGBA{})
	draw.Draw(m, image.Rect(at.X, at.Y, at.X+side, at.Y+side), chat, image.Point{}, draw.Over)
	return m
}

// threadBadgeSide is how wide the chat's picture is drawn inside a w by h
// mark.
func threadBadgeSide(w, h int) int { return int(float64(min(w, h)) * threadBadge) }
