package emoji

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// picture writes a w×h source file, as PNG or, for .jpg, as JPEG — the
// clipboard and Finder hand over both.
func picture(t *testing.T, path string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	if filepath.Ext(path) == ".jpg" {
		require.NoError(t, jpeg.Encode(f, img, nil))
	} else {
		require.NoError(t, png.Encode(f, img))
	}
	return path
}

func sides(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	require.NoError(t, err)
	require.Equal(t, "png", format, "a picture is kept as a png whatever came in")
	return cfg.Width, cfg.Height
}

func TestAddCustom_ReachesThePictureByEveryNameItAnswersTo(t *testing.T) {
	dir := t.TempDir()
	c, err := AddCustom(dir, "摸鱼", []string{"划水", "slacking"}, picture(t, filepath.Join(dir, "src.png"), 100, 100))
	require.NoError(t, err)
	require.Equal(t, "custom:摸鱼", c.Key())
	require.Subset(t, c.Terms, []string{"摸鱼", "moyu", "my", "划水", "huashui", "hs", "slacking"})
	require.FileExists(t, c.Path(dir))
}

func TestAddCustom_HoldsAPictureToTheSizeAnEmojiIsDrawnAt(t *testing.T) {
	dir := t.TempDir()
	// Feishu draws a pasted picture at its own pixel size, so a screenshot
	// would arrive in the chat as a screenshot.
	c, err := AddCustom(dir, "宽图", nil, picture(t, filepath.Join(dir, "wide.png"), 1200, 600))
	require.NoError(t, err)
	w, h := sides(t, c.Path(dir))
	require.Equal(t, MaxCustomSide, w)
	require.Equal(t, MaxCustomSide/2, h, "the proportions are kept")
}

func TestAddCustom_LeavesAPictureThatIsAlreadySmallEnoughAlone(t *testing.T) {
	dir := t.TempDir()
	c, err := AddCustom(dir, "小图", nil, picture(t, filepath.Join(dir, "small.png"), 80, 60))
	require.NoError(t, err)
	w, h := sides(t, c.Path(dir))
	require.Equal(t, 80, w)
	require.Equal(t, 60, h)
}

func TestAddCustom_KeepsAJPEGAsAPNG(t *testing.T) {
	dir := t.TempDir()
	c, err := AddCustom(dir, "照片", nil, picture(t, filepath.Join(dir, "shot.jpg"), 300, 300))
	require.NoError(t, err)
	sides(t, c.Path(dir)) // asserts the format
}

func TestAddCustom_ReplacesTheEntryOfTheSameName(t *testing.T) {
	dir := t.TempDir()
	_, err := AddCustom(dir, "摸鱼", []string{"划水"}, picture(t, filepath.Join(dir, "a.png"), 50, 50))
	require.NoError(t, err)
	_, err = AddCustom(dir, "摸鱼", []string{"偷懒"}, picture(t, filepath.Join(dir, "b.png"), 50, 50))
	require.NoError(t, err)

	all, err := LoadCustom(dir)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, []string{"偷懒"}, all[0].Aliases, "the names it answers to are the ones last given")
	require.NotContains(t, all[0].Terms, "huashui")
}

func TestLoadCustom_DropsAnEntryWhosePictureWasDeleted(t *testing.T) {
	// Deleting the picture is how one of these is removed by hand, so an entry
	// pointing at nothing is an answer rather than a fault.
	dir := t.TempDir()
	kept, err := AddCustom(dir, "留着", nil, picture(t, filepath.Join(dir, "a.png"), 50, 50))
	require.NoError(t, err)
	gone, err := AddCustom(dir, "删了", nil, picture(t, filepath.Join(dir, "b.png"), 50, 50))
	require.NoError(t, err)
	require.NoError(t, os.Remove(gone.Path(dir)))

	all, err := LoadCustom(dir)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, kept.Name, all[0].Name)
}

func TestLoadCustom_AnswersNothingBeforeAnythingIsAdded(t *testing.T) {
	all, err := LoadCustom(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, all)
}

func TestRemoveCustom_TakesTheEntryAndItsPicture(t *testing.T) {
	dir := t.TempDir()
	c, err := AddCustom(dir, "摸鱼", nil, picture(t, filepath.Join(dir, "a.png"), 50, 50))
	require.NoError(t, err)

	require.NoError(t, RemoveCustom(dir, "摸鱼"))
	require.NoFileExists(t, c.Path(dir))
	all, err := LoadCustom(dir)
	require.NoError(t, err)
	require.Empty(t, all)
	require.ErrorContains(t, RemoveCustom(dir, "摸鱼"), "no custom emoji")
}

func TestAddCustom_RefusesANameThatWouldReachOutsideItsDirectory(t *testing.T) {
	dir := t.TempDir()
	src := picture(t, filepath.Join(dir, "a.png"), 50, 50)
	for _, name := range []string{"", ".", "..", "../escape", `a\b`} {
		_, err := AddCustom(dir, name, nil, src)
		require.ErrorIs(t, err, ErrBadName, "name %q", name)
	}
}

func TestCustomDir_IsNotTheDirectorySyncWritesWhole(t *testing.T) {
	// Sync removes its directory before cutting, so a picture kept there would
	// be lost the next time this binary carries a new sheet.
	require.NotEqual(t, Dir("/data"), CustomDir("/data"))
}
