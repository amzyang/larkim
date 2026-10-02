package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amzyang/larkim/store"
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
	turn := &aiTurn{state: aiAsking}
	m := Model{spin: newSpin(), aiP: &aiPanel{sess: []*aiSession{{turns: []*aiTurn{turn}}}}}

	require.NotNil(t, m.paceSpin(nil), "a streaming answer starts the spinner")
	assert.True(t, m.spin.Running())
	assert.Nil(t, m.paceSpin(nil), "a second message must not stack a tick chain")

	turn.state = aiDone
	assert.Nil(t, m.paceSpin(nil))
	assert.False(t, m.spin.Running(), "the spinner stops once nothing waits on it")
}

func TestRespin_SwapsOnlyThePendingSendsFrame(t *testing.T) {
	msgs := []store.Message{{MessageID: "local-1", SenderID: "ou_me", SenderName: "林岚",
		ChatID: "oc_1", Content: "在路上", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.outbox = map[string]outboxState{"local-1": outSending}
	st.spin = "<f0>"
	rows := renderRows(msgs, st)
	require.True(t, spinning(rows), "a send on its way heads its block with the spinner")
	before := rowText(rows)

	respin(rows, "<f1>")
	after := rowText(rows)
	assert.Contains(t, after, "<f1> sending")
	assert.NotContains(t, after, "<f0>")
	assert.Equal(t, strings.Replace(before, "<f0>", "<f1>", 1), after, "nothing but the frame moves")

	st.outbox["local-1"] = outFailed
	assert.False(t, spinning(renderRows(msgs, st)), "a failed send has nothing to wait on")
}
