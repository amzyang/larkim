package tui

import (
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	blank = color.RGBA{}
	red   = color.RGBA{R: 255, A: 255}
	blue  = color.RGBA{B: 255, A: 255}

	animPalette = color.Palette{blank, red, blue}
)

// patch is one GIF frame: the rectangle r painted in the palette colour idx.
func patch(r image.Rectangle, idx uint8) *image.Paletted {
	m := image.NewPaletted(r, animPalette)
	for i := range m.Pix {
		m.Pix[i] = idx
	}
	return m
}

func writeAnimGIF(t *testing.T, dir, name string, frames, w, h int) string {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: w, Height: h}}
	for i := range frames {
		g.Image = append(g.Image, patch(image.Rect(0, 0, w, h), uint8(1+i%2)))
		g.Delay = append(g.Delay, 6)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	f, err := os.Create(filepath.Join(dir, name))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, gif.EncodeAll(f, g))
	return name
}

func TestGIFFrames_CompositesEachPatchOntoTheOneBefore(t *testing.T) {
	g := &gif.GIF{
		Config:   image.Config{Width: 4, Height: 4},
		Image:    []*image.Paletted{patch(image.Rect(0, 0, 4, 4), 1), patch(image.Rect(2, 2, 4, 4), 2)},
		Delay:    []int{6, 6},
		Disposal: []byte{gif.DisposalNone, gif.DisposalNone},
	}
	frames, gaps := gifFrames(g, image.Pt(4, 4), image.Pt(4, 4))
	require.Len(t, frames, 2)
	require.Equal(t, []int{60, 60}, gaps)
	require.Equal(t, red, frames[1].At(0, 0), "the second frame keeps what the first painted")
	require.Equal(t, blue, frames[1].At(3, 3), "and carries its own patch")
}

func TestGIFFrames_HonoursTheDisposalRules(t *testing.T) {
	background := &gif.GIF{
		Config:   image.Config{Width: 4, Height: 4},
		Image:    []*image.Paletted{patch(image.Rect(0, 0, 4, 4), 1), patch(image.Rect(2, 2, 4, 4), 2)},
		Delay:    []int{6, 6},
		Disposal: []byte{gif.DisposalBackground, gif.DisposalNone},
	}
	frames, _ := gifFrames(background, image.Pt(4, 4), image.Pt(4, 4))
	require.Equal(t, blank, frames[1].At(0, 0), "a disposed frame is wiped before the next one lands")
	require.Equal(t, blue, frames[1].At(3, 3))

	previous := &gif.GIF{
		Config: image.Config{Width: 4, Height: 4},
		Image: []*image.Paletted{
			patch(image.Rect(0, 0, 4, 4), 1),
			patch(image.Rect(2, 2, 4, 4), 2),
			patch(image.Rect(0, 0, 1, 1), 2),
		},
		Delay:    []int{6, 6, 6},
		Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
	}
	frames, _ = gifFrames(previous, image.Pt(4, 4), image.Pt(4, 4))
	require.Equal(t, blue, frames[1].At(3, 3))
	require.Equal(t, red, frames[2].At(3, 3), "the canvas goes back to what it was before the disposed frame")
}

func TestGIFFrames_StopsAtTheFrameCap(t *testing.T) {
	g := &gif.GIF{Config: image.Config{Width: 2, Height: 2}}
	for range picMaxFrames + 10 {
		g.Image = append(g.Image, patch(image.Rect(0, 0, 2, 2), 1))
		g.Delay = append(g.Delay, 6)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	frames, gaps := gifFrames(g, image.Pt(2, 2), image.Pt(2, 2))
	require.Len(t, frames, picMaxFrames)
	require.Len(t, gaps, picMaxFrames)
}

func TestFrameGap_HoldsTheImpatientFramesAsABrowserWould(t *testing.T) {
	require.Equal(t, 60, frameGap(6))
	require.Equal(t, 20, frameGap(2))
	require.Equal(t, 100, frameGap(1), "10ms means as fast as possible, which browsers settled on at 100ms")
	require.Equal(t, 100, frameGap(0))
}

func TestLoadFrames_AStillPictureIsOneFrame(t *testing.T) {
	dir := t.TempDir()
	still := filepath.Join(dir, writePNG(t, dir, "a.png", 20, 10))
	frames, gaps, err := loadFrames(still, image.Pt(20, 10), image.Pt(20, 10))
	require.NoError(t, err)
	require.Len(t, frames, 1)
	require.Len(t, gaps, 1)

	one := filepath.Join(dir, writeAnimGIF(t, dir, "one.gif", 1, 20, 10))
	frames, _, err = loadFrames(one, image.Pt(20, 10), image.Pt(20, 10))
	require.NoError(t, err)
	require.Len(t, frames, 1, "a GIF with a single frame is a still picture")

	_, _, err = loadFrames(filepath.Join(dir, "missing.png"), image.Pt(20, 10), image.Pt(20, 10))
	require.Error(t, err)
}

func TestPictures_PrepareRunsAnAnimatedGIF(t *testing.T) {
	p := testPictures(t)
	pic := p.place(writeAnimGIF(t, p.dataDir, "a.gif", 3, 100, 100), 30, 20)
	require.NotZero(t, pic.cols)

	out := p.prepare([]picture{pic})
	id := p.id[pic.key()]
	require.Equal(t, 2, strings.Count(out, "a=f"), "the frames after the root one")
	require.Contains(t, out, fmt.Sprintf("\x1b_Ga=a,i=%d,r=1,z=60,q=2\x1b\\", id), "the gap the root frame cannot carry")
	require.Contains(t, out, fmt.Sprintf("\x1b_Ga=a,i=%d,s=3,v=1,q=2\x1b\\", id), "run it, looping forever")

	still := p.prepare([]picture{p.place(writePNG(t, p.dataDir, "b.png", 100, 100), 30, 20)})
	require.NotEmpty(t, still)
	require.NotContains(t, still, "a=f", "a still picture has no frames")
	require.NotContains(t, still, "a=a", "and nothing to run")
}
