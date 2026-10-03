package tui

import (
	"image/color"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectHandshake_TakesEveryAnswerFromOneRoundTrip(t *testing.T) {
	t.Parallel()
	// The answers in the order a terminal gives them: graphics, cell size,
	// background, and the device attributes closing the batch.
	stream := "\x1b_Gi=31;OK\x1b\\" + // graphics: yes
		"\x1b[6;16;8t" + // cell: 8 wide, 16 tall
		"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\" + // background: dark grey
		"\x1b[?62;22c"
	h := collectHandshake(strings.NewReader(stream), Handshake{})
	require.True(t, h.Graphics)
	require.Equal(t, 8, h.CellW)
	require.Equal(t, 16, h.CellH)
	require.NotNil(t, h.BG)
	require.True(t, h.Dark, "a dark grey background leans dark")
}

func TestCollectHandshake_TakesALightBackgroundAnsweredByBell(t *testing.T) {
	t.Parallel()
	h := collectHandshake(strings.NewReader("\x1b]11;rgb:ffff/ffff/ffff\x07\x1b[?62c"), Handshake{})
	require.False(t, h.Graphics, "a terminal that never answered the graphics query does not draw them")
	require.False(t, h.Dark, "white leans light")
}

func TestCollectHandshake_DropsKeysTypedInsideTheWindow(t *testing.T) {
	t.Parallel()
	h := collectHandshake(strings.NewReader("jkl\x1b[?62c"), Handshake{Dark: true})
	require.False(t, h.Graphics)
	require.True(t, h.Dark, "the fallback holds")
}

func TestCollectHandshake_KeepsWhatWasSaidWhenTheTerminalGoesQuiet(t *testing.T) {
	t.Parallel()
	h := collectHandshake(strings.NewReader("\x1b_Gi=31;OK\x1b\\"), Handshake{Dark: true})
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
