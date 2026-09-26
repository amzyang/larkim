package tui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

const (
	// picIDBase and picIDs bound the image ids message pictures take. They sit
	// above the avatars' range so the two never evict each other.
	picIDBase = kittyIDBase + kittyIDs
	picIDs    = 120

	// defCellW and defCellH stand in until the terminal reports its cell size.
	// Only the ratio matters here, and 1:2 is what a terminal cell usually is.
	defCellW = 10
	defCellH = 20
)

// picture is one image placed in the message list, at the size it will occupy.
type picture struct {
	path       string // absolute
	cols, rows int
	// w, h are the pixels the picture itself is drawn at inside the cols×rows
	// box. The box is rounded to the cell grid; the difference between the two
	// is left transparent rather than stretched into.
	w, h int
	// chip tints the cells the picture is placed in. A terminal paints the
	// cell background under a placement, so an emoji reads as a reaction
	// without a second picture cut with the tint baked into it.
	chip bool
	// disc clips the picture to the circle the client draws an avatar in.
	// Unlike chip it changes the pixels, so it travels in the key.
	disc bool
}

// gap holds a picture's cells while it is still on its way to the terminal.
func (p picture) gap() string {
	s := strings.Repeat(" ", p.cols)
	if p.chip {
		return stChipCells.Render(s)
	}
	return s
}

func (p picture) key() string {
	k := fmt.Sprintf("%s|%dx%d", p.path, p.cols, p.rows)
	if p.disc {
		k += "|disc"
	}
	return k
}

// pictures draws message images through the kitty graphics protocol, the same
// virtual placements the chat avatars use. A nil *pictures is a terminal
// without graphics, where every method answers "no picture" and the message
// list falls back to a text stand-in.
type pictures struct {
	dataDir      string
	cellW, cellH int
	// size caches each file's pixel size, so laying a message out does not
	// re-read the file every frame; a file that cannot be decoded is absent
	// and recorded in failed.
	size   map[string]image.Point
	failed map[string]bool
	// id maps a placement to the image id holding it; clock and used drive the
	// eviction of the least recently prepared one.
	id    map[string]int
	used  map[string]int64
	clock int64
	// drew records which generated discs are on disk, so a person with no
	// avatar file has one drawn once rather than every time a pane lays out.
	// False is a machine with no usable font, where nothing can be drawn.
	drew map[string]bool
}

// newPictures picks the renderer the terminal can show, by the same narrow
// detection the avatars use: a wrong guess leaves placeholder glyphs on screen.
func newPictures(dataDir string, env func(string) string) *pictures {
	if dataDir == "" {
		return nil
	}
	if env("KITTY_WINDOW_ID") == "" && !strings.Contains(env("TERM"), "kitty") {
		return nil
	}
	return &pictures{dataDir: dataDir, size: map[string]image.Point{}, failed: map[string]bool{},
		id: map[string]int{}, used: map[string]int64{}, drew: map[string]bool{}}
}

// setCellSize records the terminal's cell size and drops every placement,
// because they were transmitted for the old one. Returns whether anything
// changed, so the caller knows to lay the panes out again.
func (p *pictures) setCellSize(w, h int) bool {
	if p == nil || w <= 0 || h <= 0 || (w == p.cellW && h == p.cellH) {
		return false
	}
	p.cellW, p.cellH = w, h
	p.id = map[string]int{}
	p.used = map[string]int64{}
	return true
}

func (p *pictures) cell() (w, h int) {
	if p.cellW <= 0 || p.cellH <= 0 {
		return defCellW, defCellH
	}
	return p.cellW, p.cellH
}

// place is the size a file gets inside a maxCols×maxRows pane, in cells. A
// zero size means there is nothing to draw: no graphics, no readable file, or
// no room.
func (p *pictures) place(path string, maxCols, maxRows int) picture {
	if p == nil || path == "" || maxCols < 2 || maxRows < 1 {
		return picture{}
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(p.dataDir, abs)
	}
	px, ok := p.pixels(abs)
	if !ok {
		return picture{}
	}
	cw, ch := p.cell()
	boxW, boxH := maxCols*cw, maxRows*ch
	// A picture is never drawn past its own pixels: enlarging a sticker only
	// spreads its edges. Each clamp takes the ratio from the source, so
	// clamping twice does not compound the rounding.
	w, h := px.X, px.Y
	if w > boxW {
		w, h = boxW, max(1, px.Y*boxW/px.X)
	}
	if h > boxH {
		w, h = max(1, px.X*boxH/px.Y), boxH
	}
	// The cells are rounded to the nearest whole one and the picture is then
	// fitted inside them, giving up at most half a cell of its own size.
	// Rounding up instead would keep every pixel, but a square emoji is taller
	// than a cell is wide, so the near-empty cell it left beside one line of
	// text read as a space nobody put there.
	cols := clamp((w+cw/2)/cw, 1, maxCols)
	rows := clamp((h+ch/2)/ch, 1, maxRows)
	if bw, bh := cols*cw, rows*ch; w > bw || h > bh {
		if w*bh > h*bw {
			w, h = bw, max(1, h*bw/w)
		} else {
			w, h = max(1, w*bh/h), bh
		}
	}
	return picture{path: abs, cols: cols, rows: rows, w: w, h: h}
}

// generatedDir holds the discs drawn for people who have no avatar file. It
// sits beside the downloaded ones and is rebuilt on demand, so deleting it
// costs nothing.
var generatedDir = filepath.Join("resources", "avatars", "generated")

// disc places a person's picture as the circle the client draws: their avatar
// file, or one drawn from their name when they have none. A zero size means
// neither could be had — no graphics, no file and no font — and the caller
// falls back to the colour block.
func (p *pictures) disc(file, id, name string, cols, rows int) picture {
	if p == nil {
		return picture{}
	}
	if file != "" {
		if pic := p.place(file, cols, rows); pic.cols > 0 {
			pic.disc = true
			return pic
		}
	}
	path, ok := p.drawn(id, name, cols, rows)
	if !ok {
		return picture{}
	}
	// A drawn disc carries its own circle, cut at the size it was drawn.
	return p.place(path, cols, rows)
}

// drawn is the file holding the disc drawn for a person with no avatar of
// their own, writing it the first time it is asked for. It is drawn square
// rather than to the whole box, so the circle stays a circle on a cell grid
// that is not exactly half as wide as it is tall.
func (p *pictures) drawn(id, name string, cols, rows int) (string, bool) {
	cw, ch := p.cell()
	side := min(cols*cw, rows*ch)
	path := filepath.Join(p.dataDir, generatedDir, fmt.Sprintf("%s-%d.png", id, side))
	if ok, seen := p.drew[path]; seen {
		return path, ok
	}
	p.drew[path] = p.writeDisc(path, id, name, side)
	return path, p.drew[path]
}

func (p *pictures) writeDisc(path, id, name string, side int) bool {
	if _, err := os.Stat(path); err == nil {
		return true // drawn on an earlier run, at this very cell size
	}
	// Filled, never outlined: this panel only ever draws a sender, and the
	// outlined style is what the chat list gives a group.
	img := generateAvatar(name, idHash(id), side, side, false)
	if img == nil {
		return false // no font on this machine; the colour block takes over
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	f, err := os.Create(path)
	if err != nil {
		return false
	}
	defer f.Close()
	return png.Encode(f, img) == nil
}

// pixels is the file's size, decoded from its header once.
func (p *pictures) pixels(abs string) (image.Point, bool) {
	if pt, ok := p.size[abs]; ok {
		return pt, true
	}
	if p.failed[abs] {
		return image.Point{}, false
	}
	f, err := os.Open(abs)
	if err != nil {
		p.failed[abs] = true
		return image.Point{}, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		p.failed[abs] = true
		return image.Point{}, false
	}
	pt := image.Point{X: cfg.Width, Y: cfg.Height}
	p.size[abs] = pt
	return pt, true
}

// cells is one row of a prepared picture, or "" while it has not been sent.
func (p *pictures) cells(pic picture, row int) string {
	if p == nil {
		return ""
	}
	id, ok := p.id[pic.key()]
	if !ok {
		return ""
	}
	cells := placeholderRow(id, row, pic.cols)
	if pic.chip {
		// The background alone: the foreground of a placeholder cell is what
		// names the image to the terminal.
		return stChipCells.Render(cells)
	}
	return cells
}

// prepare transmits the pictures that do not have an id yet, evicting the
// least recently prepared once every id is spoken for. Transmitting over a
// live id replaces that picture, frames and all, so no delete is needed.
func (p *pictures) prepare(pics []picture) string {
	if p == nil || len(pics) == 0 {
		return ""
	}
	p.clock++
	// Touch every placement this pass will draw before any of them can be
	// evicted, so the least recently prepared one is always a placement that
	// scrolled away rather than one the loop has not reached yet.
	for _, pic := range pics {
		key := pic.key()
		if _, live := p.id[key]; live {
			p.used[key] = p.clock
		}
	}
	var out strings.Builder
	for _, pic := range pics {
		key := pic.key()
		if _, ok := p.id[key]; ok {
			continue
		}
		if p.failed[pic.path] {
			continue
		}
		cw, ch := p.cell()
		frames, gaps, err := loadFrames(pic.path, image.Point{X: pic.w, Y: pic.h},
			image.Point{X: pic.cols * cw, Y: pic.rows * ch})
		if err != nil {
			p.failed[pic.path] = true
			continue
		}
		if pic.disc {
			// The circle is cut from the picture's own pixels, not from the
			// canvas: the box is rounded up to whole cells, and masking the
			// slack along with it would flatten the arc where the picture ends.
			drawn := image.Rect(0, 0, pic.w, pic.h)
			for _, f := range frames {
				maskDisc(f.SubImage(drawn).(*image.RGBA))
			}
		}
		id := p.take(key)
		err = transmitPicture(&out, id, frames[0], pic.cols, pic.rows)
		if err == nil && len(frames) > 1 {
			err = transmitAnimation(&out, id, frames, gaps)
		}
		if err != nil {
			// Both maps key the same placement; leaving one behind would let
			// take pick it as the oldest and hand out image id 0.
			delete(p.id, key)
			delete(p.used, key)
			p.failed[pic.path] = true
		}
	}
	return out.String()
}

// take assigns an image id to key, reclaiming the least recently prepared one
// once every id is spoken for.
func (p *pictures) take(key string) int {
	if len(p.id) < picIDs {
		id := picIDBase + len(p.id)
		p.id[key], p.used[key] = id, p.clock
		return id
	}
	oldest, oldestAt := "", int64(0)
	for k, at := range p.used {
		if oldest == "" || at < oldestAt {
			oldest, oldestAt = k, at
		}
	}
	id := p.id[oldest]
	delete(p.id, oldest)
	delete(p.used, oldest)
	p.id[key], p.used[key] = id, p.clock
	return id
}

// forget drops every placement without touching what was learned about the
// files themselves. Something else has owned the screen — an external editor,
// say — so the terminal no longer holds the images that were transmitted for
// it, while their sizes and the discs already on disk are still true.
func (p *pictures) forget() {
	if p == nil {
		return
	}
	p.id = map[string]int{}
	p.used = map[string]int64{}
}
