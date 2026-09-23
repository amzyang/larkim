package tui

import (
	"fmt"
	"image"
	_ "image/jpeg" // avatars come back as whatever the CDN served
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi/kitty"
	"golang.org/x/image/draw"
)

// avatars fills the chat list's avatar column.
type avatars interface {
	// cells are the two lines the column occupies for one chat.
	cells(c store.Chat) (top, bottom string)
	// prepare returns an escape sequence the terminal needs before these
	// chats can be drawn, or "" when there is nothing to send. Sending it is
	// the caller's job, because only it can reach the terminal in frame order.
	prepare(chats []store.Chat) string
}

// textAvatars draws the colour block that stands in for a picture.
type textAvatars struct{}

func (textAvatars) cells(c store.Chat) (string, string) { return avatarBlock(c) }
func (textAvatars) prepare([]store.Chat) string         { return "" }

const (
	// avatarPixels is the transmitted size. The terminal scales it to
	// avatarWidth x chatRowHeight cells, so this only has to be big enough
	// not to look soft.
	avatarPixels = 64
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
	// failed remembers chats whose file could not be turned into a picture,
	// so a broken avatar is decoded once, not every frame.
	failed   map[string]bool
	fallback textAvatars
}

func newKittyAvatars(dataDir string) *kittyAvatars {
	return &kittyAvatars{
		dataDir: dataDir,
		id:      map[string]int{},
		used:    map[string]int64{},
		failed:  map[string]bool{},
	}
}

func (k *kittyAvatars) cells(c store.Chat) (string, string) {
	id, ok := k.id[c.ChatID]
	if !ok {
		return k.fallback.cells(c)
	}
	return placeholderLine(id, 0), placeholderLine(id, 1)
}

// placeholderLine is one row of a picture's cells.
func placeholderLine(id, row int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[38;5;%dm", id)
	for col := range avatarWidth {
		b.WriteRune(kitty.Placeholder)
		b.WriteRune(kitty.Diacritic(row))
		b.WriteRune(kitty.Diacritic(col))
	}
	b.WriteString("\x1b[39m")
	return b.String()
}

// prepare transmits the pictures of chats that do not have one yet, evicting
// the least recently prepared when the id space is full. Transmitting over a
// live id replaces that picture, so no delete is needed.
func (k *kittyAvatars) prepare(chats []store.Chat) string {
	k.clock++
	var out strings.Builder
	for _, c := range chats {
		if _, ok := k.id[c.ChatID]; ok {
			k.used[c.ChatID] = k.clock
			continue
		}
		if k.failed[c.ChatID] || c.AvatarFile() == "" {
			continue
		}
		img, err := loadAvatar(filepath.Join(k.dataDir, c.AvatarFile()))
		if err != nil {
			k.failed[c.ChatID] = true
			continue
		}
		id := k.take(c.ChatID)
		if err := transmitAvatar(&out, id, img); err != nil {
			// Both maps key the same chat; leaving one behind would let take
			// pick it as the oldest and hand out image id 0.
			delete(k.id, c.ChatID)
			delete(k.used, c.ChatID)
			k.failed[c.ChatID] = true
			continue
		}
	}
	return out.String()
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

// loadAvatar decodes and squares an avatar file. Formats the standard library
// cannot decode (webp) fail here and fall back to the colour block.
func loadAvatar(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	dst := image.NewRGBA(image.Rect(0, 0, avatarPixels, avatarPixels))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst, nil
}

func transmitAvatar(w *strings.Builder, id int, img image.Image) error {
	return kitty.EncodeGraphics(w, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		ID:               id,
		Format:           kitty.PNG,
		Transmission:     kitty.Direct,
		VirtualPlacement: true,
		Columns:          avatarWidth,
		Rows:             chatRowHeight,
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
