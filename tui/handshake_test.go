package tui

import (
	"image/color"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// decode feeds one batch of bytes the way a read delivers it and returns the
// handshake it left, folding in what earlier batches said.
func decode(t *testing.T, h Handshake, batches ...string) (Handshake, bool) {
	var dec uv.EventDecoder
	pending := []byte{}
	var done bool
	for _, b := range batches {
		h, pending, done = decodeHandshake(&dec, pending, h)
		require.False(t, done, "an earlier batch must not close the batch")
		pending = append(pending, b...)
	}
	h, pending, done = decodeHandshake(&dec, pending, h)
	return h, done
}

func TestDecodeHandshake_TakesEveryAnswerFromOneRoundTrip(t *testing.T) {
	t.Parallel()
	// The answers in the order a terminal gives them: graphics, cell size,
	// background, and the device attributes closing the batch.
	h, done := decode(t, Handshake{},
		"\x1b_Gi=31;OK\x1b\\"+ // graphics: yes
			"\x1b[6;16;8t"+ // cell: 8 wide, 16 tall
			"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"+ // background: dark grey
			"\x1b[?62;22c")
	require.True(t, done)
	require.True(t, h.Graphics)
	require.Equal(t, 8, h.CellW)
	require.Equal(t, 16, h.CellH)
	require.NotNil(t, h.BG)
	require.True(t, h.Dark, "a dark grey background leans dark")
}

func TestDecodeHandshake_TakesALightBackgroundAnsweredByBell(t *testing.T) {
	t.Parallel()
	h, done := decode(t, Handshake{}, "\x1b]11;rgb:ffff/ffff/ffff\x07\x1b[?62c")
	require.True(t, done)
	require.False(t, h.Graphics, "a terminal that never answered the graphics query does not draw them")
	require.False(t, h.Dark, "white leans light")
}

func TestDecodeHandshake_DropsKeysTypedInsideTheWindow(t *testing.T) {
	t.Parallel()
	h, done := decode(t, Handshake{Dark: true}, "jkl\x1b[?62c")
	require.True(t, done)
	require.False(t, h.Graphics)
	require.True(t, h.Dark, "the fallback holds")
}

func TestDecodeHandshake_KeepsWhatWasSaidWhenTheTerminalGoesQuiet(t *testing.T) {
	t.Parallel()
	h, done := decode(t, Handshake{Dark: true}, "\x1b_Gi=31;OK\x1b\\")
	require.False(t, done)
	require.True(t, h.Graphics)
	require.Nil(t, h.BG)
	require.True(t, h.Dark)
}

func TestNew_DrawsTheFirstFrameTheHandshakeDescribed(t *testing.T) {
	t.Parallel()
	m := New(Deps{DataDir: t.TempDir(), Term: Handshake{Graphics: true, CellW: 8, CellH: 16, BG: color.Black, Dark: true}})
	require.IsType(t, &kittyAvatars{}, m.avatars)
	require.NotNil(t, m.pics)
	require.Equal(t, 8, m.cellW)
	require.Equal(t, 16, m.cellH)
}

func TestNew_KeepsTheStandInsForATerminalThatDrewNoPictures(t *testing.T) {
	t.Parallel()
	m := New(Deps{})
	require.IsType(t, textAvatars{}, m.avatars)
	require.Nil(t, m.pics)
	require.True(t, m.dark, "the palette stays at the dark fallback")
}
