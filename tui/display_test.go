package tui

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// kittyAnswer is kitty's XTGETTCAP reply to one of displayQuery's names.
func kittyAnswer(name, value string) string {
	return "\x1bP1+r" + hex.EncodeToString([]byte(name)) + "=" + hex.EncodeToString([]byte(value)) + "\x1b\\"
}

func TestCollectHandshake_TakesTheDisplayScaleKittyStates(t *testing.T) {
	t.Parallel()
	stream := kittyAnswer("kitty-query-dpi_x", "144") + kittyAnswer("kitty-query-os_name", "macos") + "\x1b[?62c"
	h := collectHandshake(strings.NewReader(stream), Handshake{})
	require.Equal(t, 2.0, h.Display.scale())
}

func TestCollectHandshake_RefusedDisplayQueryLeavesScaleOne(t *testing.T) {
	t.Parallel()
	stream := "\x1bP0+r" + hex.EncodeToString([]byte("kitty-query-dpi_x")) + "\x1b\\\x1b[?62c"
	h := collectHandshake(strings.NewReader(stream), Handshake{})
	require.Equal(t, 1.0, h.Display.scale())
}

func TestDisplay_ScaleIsDPIOverTheTerminalOSBase(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		dpi   string
		os    string
		scale float64
	}{
		{"retina mac", "144", "macos", 2},
		{"1x external screen", "72", "macos", 1},
		{"2x linux", "192", "linux", 2},
		{"mac terminal, larkim over ssh: the base is kitty's OS", "144", "macos", 2},
		{"multiplexer that answers nothing", "", "", 1},
		{"dpi without the OS it is measured against", "144", "", 1},
		{"never below today's size", "72", "linux", 1},
	} {
		var d display
		if c.dpi != "" {
			require.True(t, d.read("kitty-query-dpi_x="+c.dpi), c.name)
		}
		if c.os != "" {
			require.True(t, d.read("kitty-query-os_name="+c.os), c.name)
		}
		require.Equal(t, c.scale, d.scale(), c.name)
	}
	var d display
	require.False(t, d.read("RGB"), "an answer display did not ask for")
	require.False(t, d.read("kitty-query-dpi_x=nope"))
}

// The case from issue #2: a 219×151 image on a retina screen, cells 17×36
// device px, with room to spare. The client shows it at 219×151 pt, which is
// 438×302 screen px; before the scale it was drawn at half that.
func TestPictures_PlaceDrawsAtTheClientSizeOnARetinaScreen(t *testing.T) {
	t.Parallel()
	p := newPictures(t.TempDir(), true)
	p.setCellSize(17, 36)
	path := writePNG(t, p.dataDir, "sticker.png", 219, 151)

	unscaled := p.place(path, 80, 20)
	require.Equal(t, [4]int{13, 4, 208, 144}, [4]int{unscaled.cols, unscaled.rows, unscaled.w, unscaled.h},
		"no answer from the terminal draws today's size")

	var d display
	d.read("kitty-query-dpi_x=144")
	d.read("kitty-query-os_name=macos")
	require.True(t, p.setDisplay(d))
	pic := p.place(path, 80, 20)
	require.Equal(t, [4]int{26, 8, 417, 288}, [4]int{pic.cols, pic.rows, pic.w, pic.h},
		"twice the cells, fitted inside them the same way")

	require.False(t, p.setDisplay(d), "the same scale again changes nothing")
}

func TestPictures_SetDisplayDropsPlacementsSentForTheOldScale(t *testing.T) {
	t.Parallel()
	p := testPictures(t)
	p.id["x"], p.used["x"] = picIDBase, 1
	var d display
	d.read("kitty-query-dpi_x=144")
	d.read("kitty-query-os_name=macos")
	require.True(t, p.setDisplay(d))
	require.Empty(t, p.id)
	require.Empty(t, p.used)
}
