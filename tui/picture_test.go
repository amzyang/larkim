package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testPictures(t *testing.T) *pictures {
	t.Helper()
	p := newPictures(t.TempDir(), func(string) string { return "xterm-kitty" })
	require.NotNil(t, p)
	p.setCellSize(10, 20)
	return p
}

func TestNewPictures_OnlyOnATerminalThatDrawsThem(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	require.Nil(t, newPictures("/tmp", env(map[string]string{"TERM": "xterm-256color"})))
	require.Nil(t, newPictures("", env(map[string]string{"TERM": "xterm-kitty"})), "no data dir, no files to draw")
	require.NotNil(t, newPictures("/tmp", env(map[string]string{"KITTY_WINDOW_ID": "1"})))
	require.NotNil(t, newPictures("/tmp", env(map[string]string{"TERM": "xterm-kitty"})))
}

func TestPictures_PlaceKeepsTheAspectRatioWithinThePane(t *testing.T) {
	p := testPictures(t)
	wide := writePNG(t, p.dataDir, "wide.png", 1000, 500)
	pic := p.place(wide, 30)
	require.Equal(t, 30, pic.cols, "a wide picture takes the columns it is offered")
	require.Equal(t, 8, pic.rows, "30 cols × 10px, halved by the aspect, over a 20px cell")

	tall := writePNG(t, p.dataDir, "tall.png", 200, 4000)
	pic = p.place(tall, 30)
	require.Equal(t, picMaxRows, pic.rows, "a tall picture is capped, not allowed to take the pane")
	require.LessOrEqual(t, pic.cols, 30)
}

func TestPictures_PlaceGivesNothingItCannotDraw(t *testing.T) {
	p := testPictures(t)
	require.Zero(t, p.place("", 30).cols, "a file that was never downloaded")
	require.Zero(t, p.place("missing.png", 30).cols)
	require.NoError(t, os.WriteFile(filepath.Join(p.dataDir, "bad.png"), []byte("not an image"), 0o600))
	require.Zero(t, p.place("bad.png", 30).cols)
	require.True(t, p.failed[filepath.Join(p.dataDir, "bad.png")], "an undecodable file is read once, not every frame")

	var none *pictures
	require.Zero(t, none.place("whatever.png", 30).cols, "a terminal without graphics draws no picture")
	require.Empty(t, none.prepare([]picture{{path: "x", cols: 2, rows: 2}}))
	require.Empty(t, none.cells(picture{cols: 2}, 0))
}

func TestPictures_PrepareTransmitsOnceAndThenPlaces(t *testing.T) {
	p := testPictures(t)
	pic := p.place(writePNG(t, p.dataDir, "a.png", 100, 100), 30)
	require.NotZero(t, pic.cols)
	require.Empty(t, p.cells(pic, 0), "nothing to place before it is transmitted")

	require.NotEmpty(t, p.prepare([]picture{pic}), "the first pass sends the picture")
	require.Empty(t, p.prepare([]picture{pic}), "a picture the terminal holds is not sent again")

	line := p.cells(pic, 0)
	require.NotEmpty(t, line)
	require.Len(t, []rune(ansi.Strip(line)), pic.cols*3, "one placeholder plus two diacritics per column")
}

func TestPictures_IDsDoNotCollideWithTheAvatars(t *testing.T) {
	p := testPictures(t)
	require.GreaterOrEqual(t, picIDBase, kittyIDBase+kittyIDs)
	require.Less(t, picIDBase+picIDs, 256, "the id travels in a 256-colour index")

	for i := range picIDs + 5 {
		p.take(fmt.Sprintf("pic-%d", i))
	}
	require.Len(t, p.id, picIDs, "the id space is reclaimed rather than grown")
}
