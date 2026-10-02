package spinner

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTest() Model {
	return New(Dots, lipgloss.Color("#202020"), lipgloss.Color("#3370ff"))
}

func TestShade_BreathesDimBrightDim(t *testing.T) {
	assert.Equal(t, 0, shade(0, shades))
	assert.Equal(t, shades-1, shade(breathEvery/2, shades))
	assert.Equal(t, 0, shade(breathEvery, shades), "the cycle wraps")
	assert.Less(t, shade(breathEvery/4, shades), shade(breathEvery/2, shades))
}

func TestStart_TwiceKeepsOneChain(t *testing.T) {
	m := newTest()
	t0 := time.Unix(0, 0)
	require.NotNil(t, m.Start(t0))
	assert.Nil(t, m.Start(t0), "a second Start must not stack another tick chain")
}

func TestUpdate_DropsTickFromStoppedChain(t *testing.T) {
	m := newTest()
	t0 := time.Unix(0, 0)
	m.Start(t0)
	stale := TickMsg{at: t0.Add(frameEvery), tag: m.tag}
	m.Stop()
	m.Start(t0)
	cmd, moved := m.Update(stale)
	assert.False(t, moved)
	assert.Nil(t, cmd, "a chain from before Stop must end, not run beside the new one")

	cmd, moved = m.Update(TickMsg{at: t0.Add(frameEvery), tag: m.tag})
	assert.True(t, moved)
	assert.NotNil(t, cmd)
}

func TestView_GlyphFollowsElapsedTime(t *testing.T) {
	m := newTest()
	t0 := time.Unix(0, 0)
	m.Start(t0)
	m.Update(TickMsg{at: t0.Add(3 * frameEvery), tag: m.tag})
	assert.Contains(t, m.View(), Dots[3])
	m.Update(TickMsg{at: t0.Add(time.Duration(len(Dots)) * frameEvery), tag: m.tag})
	assert.Contains(t, m.View(), Dots[0], "glyphs wrap")
}
