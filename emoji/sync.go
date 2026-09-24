package emoji

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// DefaultAssetsDir is where the macOS Lark client keeps its emoji. Everything
// under it is plain data — one sprite sheet and the rectangles inside it — so
// cutting the pictures out costs no network call and no parsing of the
// client's JavaScript bundle. Versions/Current is a symlink the client
// maintains, so no version is spelled out here.
const DefaultAssetsDir = "/Applications/sagtjy516.app/Contents/Frameworks/Lark Framework.framework/" +
	"Versions/Current/Resources/assets/emoji"

// Dir is where Sync leaves the cut-out pictures, under larkim's data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "emoji") }

// Path is the picture one emoji was cut out to, whether or not it exists.
func Path(dataDir, key string) string { return filepath.Join(Dir(dataDir), Fold(key)+".png") }

// subImager is what every std-library decoder returns and nothing declares.
type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

// Sync cuts the client's sprite sheet into one picture per emoji. The result
// is derived data — delete it and run this again — so it is rewritten whole
// rather than reconciled, and a terminal that cannot draw pictures never needs
// it at all.
func Sync(assetsDir, dataDir string) (int, error) {
	f, err := os.Open(filepath.Join(assetsDir, "sprite-min.png"))
	if err != nil {
		return 0, fmt.Errorf("the Lark client's emoji sprite: %w", err)
	}
	defer f.Close()
	sheet, err := png.Decode(f)
	if err != nil {
		return 0, fmt.Errorf("decode sprite: %w", err)
	}
	cut, ok := sheet.(subImager)
	if !ok {
		return 0, fmt.Errorf("sprite decoded to %T, which cannot be cut up", sheet)
	}
	dir := Dir(dataDir)
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
	return n, nil
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
