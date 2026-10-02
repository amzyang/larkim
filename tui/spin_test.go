package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaceSpin_RunsExactlyWhileAnAnswerStreams(t *testing.T) {
	turn := &aiTurn{state: aiAsking}
	m := Model{spin: newSpin(), aiP: &aiPanel{sess: []*aiSession{{turns: []*aiTurn{turn}}}}}

	require.NotNil(t, m.paceSpin(nil), "a streaming answer starts the spinner")
	assert.True(t, m.spin.Running())
	assert.Nil(t, m.paceSpin(nil), "a second message must not stack a tick chain")

	turn.state = aiDone
	assert.Nil(t, m.paceSpin(nil))
	assert.False(t, m.spin.Running(), "the spinner stops once nothing waits on it")
}
