package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstOf runs a cmd, and every cmd of the batches it returns, and hands back
// the first message of type T among them: a row on screen that waits on
// something batches the spinner's tick beside whatever the handler returned.
func firstOf[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var found T
	var ok bool
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil || ok {
			return
		}
		switch v := c().(type) {
		case T:
			found, ok = v, true
		case tea.BatchMsg:
			for _, cc := range v {
				run(cc)
			}
		}
	}
	run(cmd)
	require.True(t, ok, "no %T among the cmds", found)
	return found
}

func TestPaceSpin_RunsExactlyWhileAnAnswerStreams(t *testing.T) {
	t.Parallel()
	turn := &aiTurn{state: aiAsking}
	m := Model{spin: newSpin(), aiP: &aiPanel{sess: []*aiSession{{turns: []*aiTurn{turn}}}}}

	require.NotNil(t, m.paceSpin(nil), "a streaming answer starts the spinner")
	assert.True(t, m.spin.Running())
	assert.Nil(t, m.paceSpin(nil), "a second message must not stack a tick chain")

	turn.state = aiDone
	assert.Nil(t, m.paceSpin(nil))
	assert.False(t, m.spin.Running(), "the spinner stops once nothing waits on it")
}

func TestPaceSpin_LeavesASendOnItsWayAlone(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m.enqueue(outboxItem{localID: "local-1", chatID: "oc_1", msgType: "text", body: "在路上", createMs: 10})
	m.refreshPanes()

	assert.Nil(t, m.paceSpin(nil), "a send is drawn as sent, so nothing on screen waits on the spinner")
	assert.False(t, m.spin.Running())
}
