package emoji

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSync_CutsOneFilePerEmoji(t *testing.T) {
	data := t.TempDir()

	n, err := Sync(data)
	require.NoError(t, err)
	require.Equal(t, len(All())-countGlyphOnly(), n)

	entries, err := os.ReadDir(Dir(data))
	require.NoError(t, err)
	require.Len(t, entries, n+1, "every emoji is cut out once, beside the stamp naming the sheet")

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

func TestEnsure_LeavesAlreadyCutPicturesAlone(t *testing.T) {
	data := t.TempDir()
	_, err := Ensure(data)
	require.NoError(t, err)

	// Every later start reads the stamp and stops there, so starting costs
	// nothing once the sheet in this binary has been cut.
	untouched := filepath.Join(Dir(data), "untouched")
	require.NoError(t, os.WriteFile(untouched, nil, 0o600))
	n, err := Ensure(data)
	require.NoError(t, err)
	require.Zero(t, n)
	require.FileExists(t, untouched)
}

func TestEnsure_CutsAgainWhenTheStampNamesAnotherSheet(t *testing.T) {
	data := t.TempDir()
	_, err := Ensure(data)
	require.NoError(t, err)
	stale := filepath.Join(Dir(data), "STALE.png")
	require.NoError(t, os.WriteFile(stale, nil, 0o600))
	require.NoError(t, os.WriteFile(stampPath(data), []byte("an older sheet"), 0o600))

	n, err := Ensure(data)
	require.NoError(t, err)
	require.Positive(t, n)
	require.NoFileExists(t, stale, "the cut is written whole, so a dropped emoji keeps no picture")
}

func TestEnsure_CutsAgainWhenThePicturesWereDeleted(t *testing.T) {
	data := t.TempDir()
	_, err := Ensure(data)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(Dir(data)))

	n, err := Ensure(data)
	require.NoError(t, err)
	require.Positive(t, n)
	require.FileExists(t, Path(data, "THUMBSUP"))
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
