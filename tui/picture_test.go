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

func TestPictures_PlaceFillsWhicheverSideOfThePaneBindsFirst(t *testing.T) {
	p := testPictures(t)
	wide := writePNG(t, p.dataDir, "wide.png", 1000, 500)
	pic := p.place(wide, 30, 20)
	require.Equal(t, 30, pic.cols, "a wide picture takes every column it is offered")
	require.Equal(t, 8, pic.rows, "30 cols × 10px, halved by the aspect, over a 20px cell")
	require.Equal(t, 300, pic.w)
	require.Equal(t, 150, pic.h, "the drawn pixels keep the source ratio exactly")

	tall := writePNG(t, p.dataDir, "tall.png", 200, 4000)
	pic = p.place(tall, 30, 20)
	require.Equal(t, 20, pic.rows, "a tall picture takes every row the pane has")
	require.Equal(t, 2, pic.cols, "and only the columns the ratio then asks for")
	require.Equal(t, 20, pic.w)
	require.Equal(t, 400, pic.h)
}

func TestPictures_PlaceNeverEnlargesPastTheSourcePixels(t *testing.T) {
	p := testPictures(t)
	pic := p.place(writePNG(t, p.dataDir, "small.png", 100, 100), 158, 50)
	require.Equal(t, 100, pic.w)
	require.Equal(t, 100, pic.h, "a sticker is drawn at its own pixels, not spread over the pane")
	require.Equal(t, 10, pic.cols)
	require.Equal(t, 5, pic.rows)
}

func TestPictures_PlaceDrawsInsideTheBoxItReserves(t *testing.T) {
	p := testPictures(t)
	for _, px := range [][2]int{{1000, 500}, {200, 4000}, {31, 47}, {100, 100}, {3, 900}, {1, 1}} {
		name := fmt.Sprintf("s%dx%d.png", px[0], px[1])
		pic := p.place(writePNG(t, p.dataDir, name, px[0], px[1]), 30, 20)
		require.LessOrEqual(t, pic.w, pic.cols*10, "%s overflows the cells it reserved", name)
		require.LessOrEqual(t, pic.h, pic.rows*20, "%s overflows the cells it reserved", name)
		// Whichever side was scaled down, the other follows from the source
		// ratio, so the two cross products stay within one pixel of each other.
		require.InDelta(t, pic.w*px[1], pic.h*px[0], float64(max(pic.w, pic.h)), "%s is distorted", name)
	}
}

func TestPictures_PlaceGivesNothingWhenThePaneHasNoRoom(t *testing.T) {
	p := testPictures(t)
	tall := writePNG(t, p.dataDir, "tall.png", 200, 4000)
	require.Zero(t, p.place(tall, 1, 20).cols, "a pane too narrow to hold a picture")
	require.Zero(t, p.place(tall, 30, 0).cols, "a pane with no rows to give")
}

func TestPictures_PlaceGivesNothingItCannotDraw(t *testing.T) {
	p := testPictures(t)
	require.Zero(t, p.place("", 30, 20).cols, "a file that was never downloaded")
	require.Zero(t, p.place("missing.png", 30, 20).cols)
	require.NoError(t, os.WriteFile(filepath.Join(p.dataDir, "bad.png"), []byte("not an image"), 0o600))
	require.Zero(t, p.place("bad.png", 30, 20).cols)
	require.True(t, p.failed[filepath.Join(p.dataDir, "bad.png")], "an undecodable file is read once, not every frame")

	var none *pictures
	require.Zero(t, none.place("whatever.png", 30, 20).cols, "a terminal without graphics draws no picture")
	require.Empty(t, none.prepare([]picture{{path: "x", cols: 2, rows: 2}}))
	require.Empty(t, none.cells(picture{cols: 2}, 0))
}

func TestPictures_PrepareTransmitsOnceAndThenPlaces(t *testing.T) {
	p := testPictures(t)
	pic := p.place(writePNG(t, p.dataDir, "a.png", 100, 100), 30, 20)
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

func TestModelPicturePrepare_SettlesWhenPicturesOutnumberTheIDs(t *testing.T) {
	m := sized(106, 59)
	p := testPictures(t)
	m.pics = p
	m.msgRows = nil
	for i := range picIDs + 40 {
		pic := p.place(writePNG(t, p.dataDir, fmt.Sprintf("p%d.png", i), 10, 20), 30, 20)
		require.Equal(t, 1, pic.rows, "one row each, so the window holds more pictures than there are ids")
		m.msgRows = append(m.msgRows, msgRow{pic: pic})
	}
	m.msgTop = 80

	require.NotEmpty(t, m.picturePrepare(), "the first pass fills the id space")
	require.Empty(t, m.picturePrepare(), "a pass that changes nothing sends nothing")
	require.LessOrEqual(t, len(p.id), picIDs)
}

func TestModelPicturePrepare_CoversEveryRowOnScreen(t *testing.T) {
	m := sized(106, 59)
	p := testPictures(t)
	m.pics = p
	m.msgRows = nil
	for i := range picIDs + 40 {
		m.msgRows = append(m.msgRows, msgRow{pic: p.place(writePNG(t, p.dataDir, fmt.Sprintf("p%d.png", i), 10, 20), 30, 20)})
	}
	m.msgTop = 80
	m.picturePrepare()

	for _, r := range m.msgRows[m.msgTop : m.msgTop+m.listHeight()] {
		require.NotEmpty(t, p.cells(r.pic, 0), "a row on screen always has its picture")
	}
}

// Raw bytes rather than an encoder call: a decoder a test imports is
// registered for the whole binary, and WebP reaches the picture list through a
// blank import that nothing else would miss if it went. Both are 1×1, the
// smallest file each format allows.
var (
	tinyGIF = []byte{
		'G', 'I', 'F', '8', '9', 'a', 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
		0x00, 0x00, 0x00, 0xff, 0xff, 0xff,
		0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
		0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
	}
	tinyWebP = []byte{
		'R', 'I', 'F', 'F', 0x1a, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P', 'V', 'P', '8', 'L',
		0x0d, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00, 0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe,
		0x07, 0x00,
	}
)

func TestPictures_DrawsEveryFormatFeishuSends(t *testing.T) {
	for name, bytes := range map[string][]byte{"a.gif": tinyGIF, "a.webp": tinyWebP} {
		p := testPictures(t)
		require.NoError(t, os.WriteFile(filepath.Join(p.dataDir, name), bytes, 0o600))
		pic := p.place(name, 30, 20)
		require.NotZero(t, pic.cols, "%s is an image message like any other", name)
		require.NotEmpty(t, p.prepare([]picture{pic}), "%s reaches the terminal", name)
	}
}
