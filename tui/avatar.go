package tui

import (
	"fmt"
	"image"
	_ "image/jpeg" // pictures come back as whatever Feishu stored
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi/kitty"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// avatars fills the chat list's avatar column.
type avatars interface {
	// cells are the two lines the column occupies for one chat. badged
	// reports that the picture already carries the unread counter, which is
	// what keeps the row from printing the number a second time.
	cells(c store.Chat, unread int64) (top, bottom string, badged bool)
	// prepare returns an escape sequence the terminal needs before these
	// chats can be drawn, or "" when there is nothing to send. Sending it is
	// the caller's job, because only it can reach the terminal in frame order.
	prepare(chats []store.Chat, unread map[string]int64) string
}

// textAvatars draws the colour block that stands in for a picture.
type textAvatars struct{}

func (textAvatars) cells(c store.Chat, _ int64) (string, string, bool) {
	top, bottom := avatarBlock(c)
	return top, bottom, false
}
func (textAvatars) prepare([]store.Chat, map[string]int64) string { return "" }

const (
	// avatarPixels is the fallback transmitted size, used until the terminal
	// reports how large a cell is. Drawing at the exact size the cells will
	// occupy is what keeps glyphs crisp; anything else gets resampled.
	avatarPixels = 128
	// kittyIDBase and kittyIDs bound the image ids. The id travels in the
	// cell's foreground colour as a 256-colour index, and indices under 16
	// are named colours a palette downgrade could fold together.
	kittyIDBase = 16
	kittyIDs    = 64
)

// kittyAvatars draws real pictures through the kitty graphics protocol.
//
// Images are transmitted once as virtual placements (a=T, U=1); a cell that
// shows one holds only U+10EEEE plus row/column diacritics, with the image id
// in its foreground colour. The picture then follows its cells as the list
// scrolls, with nothing to redraw.
//
// A virtual placement belongs to the screen buffer it was made on, so the
// transmission has to reach the terminal after the alternate screen is up —
// see Model.avatarPrepare.
type kittyAvatars struct {
	dataDir string
	// id maps a chat to the image id holding its picture; clock and used
	// drive the eviction of the least recently prepared one.
	id    map[string]int
	used  map[string]int64
	clock int64
	// badge is the unread count drawn into each live picture. The counter is
	// part of the image, so a chat whose count moved needs a new one.
	badge map[string]int64
	// failed remembers chats whose file could not be turned into a picture,
	// so a broken avatar is decoded once, not every frame.
	failed   map[string]bool
	fallback textAvatars
	// cellW, cellH are the terminal's cell size in pixels, zero until it
	// reports one.
	cellW, cellH int
}

// setCellSize records the terminal's cell size and drops every cached
// picture, because they were drawn for the old one. Returns whether anything
// changed, so the caller knows to transmit again.
func (k *kittyAvatars) setCellSize(w, h int) bool {
	if w <= 0 || h <= 0 || (w == k.cellW && h == k.cellH) {
		return false
	}
	k.cellW, k.cellH = w, h
	k.id = map[string]int{}
	k.used = map[string]int64{}
	k.badge = map[string]int64{}
	k.failed = map[string]bool{}
	return true
}

// box is the pixel size an avatar occupies on screen. Before the terminal
// says, a square guess is the best available.
func (k *kittyAvatars) box() (w, h int) {
	if k.cellW <= 0 || k.cellH <= 0 {
		return avatarPixels, avatarPixels
	}
	return avatarWidth * k.cellW, chatRowHeight * k.cellH
}

func newKittyAvatars(dataDir string) *kittyAvatars {
	return &kittyAvatars{
		dataDir: dataDir,
		id:      map[string]int{},
		used:    map[string]int64{},
		badge:   map[string]int64{},
		failed:  map[string]bool{},
	}
}

func (k *kittyAvatars) cells(c store.Chat, unread int64) (string, string, bool) {
	id, ok := k.id[c.ChatID]
	if !ok {
		return k.fallback.cells(c, unread)
	}
	// Until the next prepare redraws it, the live picture still carries the
	// previous count, so the row has to print the new one itself.
	return placeholderRow(id, 0, avatarWidth), placeholderRow(id, 1, avatarWidth), k.badge[c.ChatID] == unread
}

// placeholderRow is one row of a picture's cells: the image id travels in the
// foreground colour and every cell names its own row and column.
func placeholderRow(id, row, cols int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[38;5;%dm", id)
	for col := range cols {
		b.WriteRune(kitty.Placeholder)
		b.WriteRune(kitty.Diacritic(row))
		b.WriteRune(kitty.Diacritic(col))
	}
	b.WriteString("\x1b[39m")
	return b.String()
}

// prepare transmits the pictures of chats that do not have one yet or whose
// unread counter has moved, evicting the least recently prepared when the id
// space is full. Transmitting over a live id replaces that picture, so no
// delete is needed and a redraw keeps the id its cells already name.
func (k *kittyAvatars) prepare(chats []store.Chat, unread map[string]int64) string {
	k.clock++
	// Touch every picture this pass will draw before any of them can be
	// evicted, so the least recently prepared one is always a chat outside the
	// window rather than a neighbour the loop has not reached yet.
	for _, c := range chats {
		if _, live := k.id[c.ChatID]; live {
			k.used[c.ChatID] = k.clock
		}
	}
	var out strings.Builder
	for _, c := range chats {
		n := unread[c.ChatID]
		id, live := k.id[c.ChatID]
		if live {
			if k.badge[c.ChatID] == n {
				continue
			}
		} else if k.failed[c.ChatID] {
			continue
		}
		img := k.picture(c)
		if img == nil {
			// No file and no font: the colour block takes over.
			k.failed[c.ChatID] = true
			continue
		}
		drawBadge(img, n, c.Muted)
		if !live {
			id = k.take(c.ChatID)
		}
		if err := transmitPicture(&out, id, img, avatarWidth, chatRowHeight); err != nil {
			// The maps key the same chat; leaving one behind would let take
			// pick it as the oldest and hand out image id 0.
			delete(k.id, c.ChatID)
			delete(k.used, c.ChatID)
			delete(k.badge, c.ChatID)
			k.failed[c.ChatID] = true
			continue
		}
		k.badge[c.ChatID] = n
	}
	return out.String()
}

// picture is the chat's own avatar file, or one drawn from its name when
// there is no file to read — a chat with no picture still deserves to look
// like the ones that have one.
func (k *kittyAvatars) picture(c store.Chat) *image.RGBA {
	w, h := k.box()
	if f := c.AvatarFile(); f != "" {
		if img, err := loadImage(filepath.Join(k.dataDir, f), w, h); err == nil {
			// Masked after the scale, never before: a disc cut at the file's
			// own resolution would have its rim resampled into a halo, and a
			// circle cut there lands as an ellipse in an oblong box.
			maskDisc(img)
			return img
		}
	}
	return generateAvatar(c.Name, chatHash(c.ChatID), w, h)
}

// take assigns an image id to chatID, reclaiming the least recently prepared
// one once every id is spoken for.
func (k *kittyAvatars) take(chatID string) int {
	if len(k.id) < kittyIDs {
		id := kittyIDBase + len(k.id)
		k.id[chatID], k.used[chatID] = id, k.clock
		return id
	}
	oldest, oldestAt := "", int64(0)
	for c, at := range k.used {
		if oldest == "" || at < oldestAt {
			oldest, oldestAt = c, at
		}
	}
	id := k.id[oldest]
	delete(k.id, oldest)
	delete(k.used, oldest)
	k.id[chatID], k.used[chatID] = id, k.clock
	return id
}

// loadImage decodes a file at the size it will be shown, taking the first
// frame of an animated one. A format no registered decoder handles fails here,
// and the caller falls back to what it can draw without a picture.
func loadImage(path string, w, h int) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return scaleImage(src, w, h, draw.CatmullRom), nil
}

// scaleImage redraws src to fill exactly w×h through the kernel k.
func scaleImage(src image.Image, w, h int, k draw.Interpolator) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	k.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

// fitImage draws src at img pixels into the top left of a box-sized canvas.
// The terminal fills the cells it is given, so a box rounded to the cell grid
// would stretch the picture; the pixels the rounding left over stay
// transparent instead. Top left rather than centred, so every picture in the
// list starts at the same column.
func fitImage(src image.Image, img, box image.Point, k draw.Interpolator) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, box.X, box.Y))
	k.Scale(dst, image.Rect(0, 0, img.X, img.Y), src, src.Bounds(), draw.Src, nil)
	return dst
}

func transmitPicture(w *strings.Builder, id int, img image.Image, cols, rows int) error {
	return kitty.EncodeGraphics(w, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		ID:               id,
		Format:           kitty.PNG,
		Transmission:     kitty.Direct,
		VirtualPlacement: true,
		Columns:          cols,
		Rows:             rows,
		Quiet:            2,
		Chunk:            true,
	})
}

// newAvatars picks the renderer the terminal can actually show. Detection is
// deliberately narrow: a wrong guess leaves placeholder glyphs on screen,
// while the colour block always works.
func newAvatars(dataDir string, env func(string) string) avatars {
	if dataDir == "" {
		return textAvatars{}
	}
	if env("KITTY_WINDOW_ID") != "" || strings.Contains(env("TERM"), "kitty") {
		return newKittyAvatars(dataDir)
	}
	return textAvatars{}
}
