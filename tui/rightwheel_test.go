package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// wheelRight turns the wheel over the right-hand column, which the thread, the
// assistant and the info pane take in turns.
func wheelRight(m Model, n int) Model {
	for range n {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelDown, X: m.width - 5, Y: 5})
		m = mm.(Model)
	}
	return m
}

func TestWheel_RightPaneScrollsTheAssistantWhileItIsOpen(t *testing.T) {
	m := aiOpenOn(withThread(sized(120, 36)))
	s := &aiSession{id: "s1", title: "问", turns: []*aiTurn{{
		ask: "总结", answer: strings.Repeat("回答很长 answer\n", 80), state: aiDone, at: time.Now()}}}
	m.aiP.sess = []*aiSession{s}
	m.aiP.rebuild(m)
	// Off the bottom, the way a reader who scrolled up is: the follow would
	// otherwise pin the viewport and eat the wheel.
	m.aiP.top, m.aiP.follow = 0, false
	m.layout()
	require.Greater(t, len(m.aiP.rows), m.listHeight())

	m = wheelRight(m, 2)

	require.Equal(t, 6, m.aiP.top)
	require.Equal(t, 0, m.threadTop, "the thread is behind the assistant, not under the pointer")
}

func TestWheel_RightPaneScrollsTheRosterWhileItIsOpen(t *testing.T) {
	m := withThread(sized(120, 36))
	m.rightKind, m.infoOpen = rightNone, true
	for i := range 60 {
		m.info = append(m.info, store.Contact{OpenID: "ou_a", Name: "同事 " + string(rune('A'+i%26))})
	}
	m.layout()
	require.Greater(t, len(m.infoLines(m.rightWidth()-2)), m.listHeight())

	m = wheelRight(m, 2)

	require.Equal(t, 6, m.infoTop)
	require.Equal(t, 0, m.threadTop)
}

func TestWheel_RightPaneScrollsTheThreadOtherwise(t *testing.T) {
	m := withThread(sized(120, 36))
	m.thread = m.msgs
	m.rebuildThread()
	m.layout()
	require.Greater(t, len(m.threadRows), m.listHeight())

	m = wheelRight(m, 2)

	require.Equal(t, 6, m.threadTop)
	require.Nil(t, m.aiP)
	require.Equal(t, 0, m.infoTop)
}
