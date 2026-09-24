package emoji

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeSprite writes a sheet big enough to hold every rectangle the table names.
func fakeSprite(t *testing.T, dir string) {
	t.Helper()
	w, h := 0, 0
	for _, e := range All() {
		w = max(w, e.Rect[0]+e.Rect[2])
		h = max(h, e.Rect[1]+e.Rect[3])
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, A: 255})
	require.NoError(t, os.MkdirAll(dir, 0o700))
	f, err := os.Create(filepath.Join(dir, "sprite-min.png"))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, img))
}

func TestSync_CutsOneFilePerEmoji(t *testing.T) {
	assets, data := filepath.Join(t.TempDir(), "assets"), t.TempDir()
	fakeSprite(t, assets)

	n, err := Sync(assets, data)
	require.NoError(t, err)
	require.Equal(t, len(All())-countGlyphOnly(), n)

	entries, err := os.ReadDir(Dir(data))
	require.NoError(t, err)
	require.Len(t, entries, n, "every emoji is cut out once, under its own name")

	// The picture keeps the rectangle the client gave it.
	e, ok := ByKey("THUMBSUP")
	require.True(t, ok)
	f, err := os.Open(Path(data, "THUMBSUP"))
	require.NoError(t, err)
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	require.NoError(t, err)
	require.Equal(t, e.Rect[2], cfg.Width)
	require.Equal(t, e.Rect[3], cfg.Height)
}

func TestSync_SaysWhichFileIsMissingRatherThanCarryingOn(t *testing.T) {
	// Without the pictures the message list falls back to naming the emoji,
	// which already works, so a failed cut has to be loud rather than silent.
	_, err := Sync(t.TempDir(), t.TempDir())
	require.ErrorContains(t, err, "emoji sprite")
}

func TestPath_IsTheFoldedKeySoNoTwoEmojiShareAFile(t *testing.T) {
	seen := map[string]string{}
	for _, e := range All() {
		p := Path("/data", e.Key)
		require.NotContains(t, seen, p, "%s and %s would share %s", seen[p], e.Key, p)
		seen[p] = e.Key
	}
	require.Equal(t, filepath.Join("/data", "emoji", "ROSE.png"), Path("/data", "Lark_Emoji_Rose_0"))
}

// countGlyphOnly is how many entries carry no rectangle: the spellings that
// live in glyphs.go alone and were never in the client's own table.
func countGlyphOnly() int {
	n := 0
	for _, e := range All() {
		if e.Rect[2] <= 0 {
			n++
		}
	}
	return n
}
