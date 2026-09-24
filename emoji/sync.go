package emoji

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
)

// sheetPNG is the Lark client's emoji sprite, carried in the binary so a
// machine without the client installed still draws the pictures. The
// rectangles in table.go mean nothing against any other sheet, so internal/gen
// writes the two together.
//
//go:embed sprite-min.png
var sheetPNG []byte

// stampFile names the sheet a data dir's pictures were cut from. It lives
// inside the directory it describes, so deleting that directory is all it
// takes to ask for the cut again.
const stampFile = "sheet"

// sheetStamp identifies the sheet this binary carries.
var sheetStamp = sync.OnceValue(func() string {
	sum := sha256.Sum256(sheetPNG)
	return hex.EncodeToString(sum[:8])
})

// Dir is where Sync leaves the cut-out pictures, under larkim's data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "emoji") }

// Path is the picture one emoji was cut out to, whether or not it exists.
func Path(dataDir, key string) string { return filepath.Join(Dir(dataDir), Fold(key)+".png") }

func stampPath(dataDir string) string { return filepath.Join(Dir(dataDir), stampFile) }

// subImager is what every std-library decoder returns and nothing declares.
type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

// Ensure cuts the pictures out unless the data dir already holds this binary's
// sheet, and reports how many it cut. Deciding costs one small read, so this
// belongs on every start rather than in anyone's hands.
func Ensure(dataDir string) (int, error) {
	if b, err := os.ReadFile(stampPath(dataDir)); err == nil && string(b) == sheetStamp() {
		return 0, nil
	}
	return Sync(dataDir)
}

// Sync cuts the sprite sheet into one picture per emoji. The result is derived
// data — delete the directory and it is cut again — so it is written whole
// rather than reconciled, and a terminal that cannot draw pictures never needs
// it at all.
func Sync(dataDir string) (int, error) {
	sheet, err := png.Decode(bytes.NewReader(sheetPNG))
	if err != nil {
		return 0, fmt.Errorf("decode sprite: %w", err)
	}
	cut, ok := sheet.(subImager)
	if !ok {
		return 0, fmt.Errorf("sprite decoded to %T, which cannot be cut up", sheet)
	}
	dir := Dir(dataDir)
	// Whole: an emoji the new sheet dropped would otherwise leave its picture
	// behind for the message list to keep drawing.
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range All() {
		if e.Rect[2] <= 0 || e.Rect[3] <= 0 {
			continue
		}
		box := image.Rect(e.Rect[0], e.Rect[1], e.Rect[0]+e.Rect[2], e.Rect[1]+e.Rect[3])
		if !box.In(sheet.Bounds()) {
			return n, fmt.Errorf("%s names %v, which is outside the sprite %v", e.Key, box, sheet.Bounds())
		}
		if err := writePNG(Path(dataDir, e.Key), cut.SubImage(box)); err != nil {
			return n, err
		}
		n++
	}
	return n, os.WriteFile(stampPath(dataDir), []byte(sheetStamp()), 0o600)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
