package tui

import (
	"fmt"
	"image"
	"image/gif"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi/kitty"
	"golang.org/x/image/draw"
)

// picMaxFrames caps what one animation costs. Every frame is transmitted at
// the size it is drawn, so an overlong sticker would otherwise push megabytes
// through the terminal for motion nobody stays to watch.
const picMaxFrames = 60

// loadFrames decodes a file into the frames it will be drawn as, at the size
// it will occupy. GIF is the only animated format the standard library reads;
// everything else comes back as a single frame.
func loadFrames(path string, img, box image.Point) ([]*image.RGBA, []int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if g, err := gif.DecodeAll(f); err == nil && len(g.Image) > 1 {
		frames, gaps := gifFrames(g, img, box)
		return frames, gaps, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, nil, err
	}
	return []*image.RGBA{fitImage(src, img, box, draw.CatmullRom)}, []int{0}, nil
}

// gifFrames composites a GIF into whole canvases. A GIF stores each frame as a
// patch over the ones before it under a disposal rule, and the terminal's own
// patch protocol cannot be used for them: a patch scaled on its own to the
// drawn size no longer lands where it belongs.
func gifFrames(g *gif.GIF, img, box image.Point) ([]*image.RGBA, []int) {
	canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
	n := min(len(g.Image), picMaxFrames)
	frames, gaps := make([]*image.RGBA, n), make([]int, n)
	var prev []uint8
	for i := range n {
		patch := g.Image[i]
		if g.Disposal[i] == gif.DisposalPrevious {
			prev = append(prev[:0], canvas.Pix...)
		}
		draw.Draw(canvas, patch.Bounds(), patch, patch.Bounds().Min, draw.Over)
		// ApproxBiLinear, not the sharper kernel a still picture gets: at a
		// couple of dozen frames that is the difference between a hitch and a
		// freeze when a sticker scrolls into view.
		frames[i], gaps[i] = fitImage(canvas, img, box, draw.ApproxBiLinear), frameGap(g.Delay[i])
		switch g.Disposal[i] {
		case gif.DisposalBackground:
			draw.Draw(canvas, patch.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, prev)
		}
	}
	return frames, gaps
}

// frameGap is how long one frame is held, in milliseconds. Encoders wrote 0
// and 10ms to mean "as fast as you can", which browsers settled on playing at
// 100ms; matching them keeps a sticker off the terminal's render loop.
func frameGap(delay int) int {
	if ms := delay * 10; ms >= 20 {
		return ms
	}
	return 100
}

// transmitAnimation turns an already transmitted picture into a running one:
// the frames after the root, the gap the root frame cannot carry itself, and
// the loop. The terminal plays it from there, so redrawing the placeholder
// cells on every scroll costs nothing further.
func transmitAnimation(w *strings.Builder, id int, frames []*image.RGBA, gaps []int) error {
	for i := 1; i < len(frames); i++ {
		if err := kitty.EncodeGraphics(w, frames[i], &kitty.Options{
			Action:       kitty.Frame,
			ID:           id,
			Z:            gaps[i],
			Format:       kitty.PNG,
			Transmission: kitty.Direct,
			Quiet:        2,
			Chunk:        true,
		}); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "\x1b_Ga=a,i=%d,r=1,z=%d,q=2\x1b\\", id, gaps[0])
	// v=1 is how the protocol spells "loop forever".
	fmt.Fprintf(w, "\x1b_Ga=a,i=%d,s=3,v=1,q=2\x1b\\", id)
	return nil
}
