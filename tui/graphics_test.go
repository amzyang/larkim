package tui

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

func TestGraphicsReply_IsYesOnlyWhenTheQueryIsAnsweredFirst(t *testing.T) {
	ok, done := GraphicsReply([]byte("\x1b_Gi=31;OK\x1b\\\x1b[?62;22c"))
	require.True(t, ok)
	require.True(t, done)

	ok, done = GraphicsReply([]byte("\x1b[?62;22c"))
	require.False(t, ok, "a terminal that ignores the query only answers the attributes")
	require.True(t, done)

	ok, done = GraphicsReply([]byte("\x1b_Gi=31;ENOENT:bad\x1b\\\x1b[?62c"))
	require.False(t, ok)
	require.True(t, done)

	_, done = GraphicsReply([]byte("\x1b_Gi=31;OK\x1b\\"))
	require.False(t, done, "the attributes are still on their way")
}

func TestUpdate_TheGraphicsAnswerSwapsInThePictureRenderers(t *testing.T) {
	m, _ := tallModel(t)
	m.deps.DataDir = t.TempDir()
	m.avatars, m.pics = textAvatars{}, nil

	next, _ := m.update(uv.CellSizeEvent{Width: 9, Height: 19})
	m = next.(Model)
	next, _ = m.update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: 7}, Payload: []byte("OK")})
	m = next.(Model)
	require.IsType(t, textAvatars{}, m.avatars, "a reply to somebody else's image says nothing")

	next, _ = m.update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: graphicsQueryID}, Payload: []byte("OK")})
	m = next.(Model)
	k, ok := m.avatars.(*kittyAvatars)
	require.True(t, ok)
	require.Equal(t, [2]int{9, 19}, [2]int{k.cellW, k.cellH}, "drawn at the cell size already reported")
	require.NotNil(t, m.pics)
	require.Equal(t, 9, m.pics.cellW)
}

func TestUpdate_NoGraphicsAnswerKeepsTheColourBlocks(t *testing.T) {
	m, _ := tallModel(t)
	m.deps.DataDir = t.TempDir()
	m.avatars, m.pics = textAvatars{}, nil

	next, _ := m.update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: graphicsQueryID}, Payload: []byte("EINVAL")})
	m = next.(Model)
	require.IsType(t, textAvatars{}, m.avatars)
	require.Nil(t, m.pics)
}
