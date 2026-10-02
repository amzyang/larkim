package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/amzyang/larkim/tui/component/spinner"
)

// The breath runs from the chip grey up to Lark's own blue, the colour the
// client gives what is live.
func newSpin() spinner.Model {
	return spinner.New(spinner.Dots, colChipDim, colMentionMe)
}

// paceSpin keeps the shared spinner running exactly while something on screen
// is waiting on it. Deciding here, after every handler, rather than at each
// place work starts and ends means no transition can leave it ticking for
// nothing or still on a row whose wait is over.
func (m *Model) paceSpin(msg tea.Msg) tea.Cmd {
	if !m.spinBusy() {
		m.spin.Stop()
		return nil
	}
	if _, ok := msg.(spinner.TickMsg); !ok {
		return m.spin.Start(time.Now())
	}
	cmd, moved := m.spin.Update(msg)
	if moved {
		m.redrawSpin()
	}
	return cmd
}

// spinBusy reports a row on screen that draws the spinner.
func (m Model) spinBusy() bool {
	return m.fetchingOlder() || m.aiP.anyStreaming()
}

// fetchingOlder is the floor row saying older messages are on their way.
func (m Model) fetchingOlder() bool {
	return m.msgPullInFlight && !m.searching && m.feed == nil && len(m.msgRows) > 0 &&
		m.atLocalFloor() && m.historyFloorMs() != 0
}

// redrawSpin repaints only the rows baked with a frame: the floor row in place,
// and the assistant's turns when a placeholder is showing. The panel header is
// drawn at View time and needs nothing.
func (m *Model) redrawSpin() {
	if m.fetchingOlder() {
		m.msgRows[0] = m.floorRow(m.messagesWidth() - 2)
	}
	if m.aiP.placeholderShowing() {
		m.aiP.rebuild(*m)
	}
}

// spinRule is daySeparator with the spinner ahead of the label.
func spinRule(spin, label string, width int) string {
	room := max(0, width-lipgloss.Width(label)-lipgloss.Width(spin)-3)
	left := room / 2
	return stDim.Render(strings.Repeat("─", left)+" ") + spin + stDim.Render(" "+label+" "+strings.Repeat("─", room-left))
}
