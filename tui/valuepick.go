package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/config"
)

// valuePick is the chooser a pickValue row opens: its config.Values listed
// under the value cell, the one set now ticked. The Lark client's Settings
// window offers a short fixed set the same way, as a dropdown under the field.
// The list is short enough to walk, so it has no query to type into.
//
// A zero value is closed.
type valuePick struct {
	open bool
	menu menu[valueHit]
}

// valueHit is one value the chooser offers.
type valueHit struct {
	value string
	// current is the value the row is set to now.
	current bool
}

var valueSpec = menuSpec[valueHit]{
	row: func(h valueHit) offer {
		icon := offerIcon{text: " "}
		if h.current {
			icon.text = "✓"
		}
		return offer{icon: icon, name: h.value}
	},
}

// openValuePick opens the chooser on the row, its cursor on the value set now
// so enter alone keeps it.
func (m Model) openValuePick(s setting) Model {
	current := m.settingValue(s)
	values := config.Values(s.key)
	hits := make([]valueHit, len(values))
	for i, v := range values {
		hits[i] = valueHit{value: v, current: v == current}
	}
	m.config.err = ""
	m.config.value = valuePick{open: true, menu: fillMenu(hits, valueSpec)}
	m.valuePickScroll(max(0, slices.Index(values, current)))
	return m
}

func (m Model) onValuePickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.config.value = valuePick{}
	case "enter":
		h, ok := m.config.value.menu.focused()
		if !ok {
			return m, nil
		}
		m.config.value = valuePick{}
		return m.commitConfig(h.value), nil
	case "j", "down", "ctrl+n":
		m.valuePickScroll(1)
	case "k", "up", "ctrl+p":
		m.valuePickScroll(-1)
	}
	// Any other key leaves the chooser standing, as one does the panel.
	return m, nil
}

// valuePickLines is the list drawn under the row.
func (m Model) valuePickLines(indent, w int) []string {
	return m.pickMenuLines(m.config.value.menu.view(m.configPickRows()), indent, w)
}

// valuePickHeight is how many lines the list under the row takes.
func (m Model) valuePickHeight() int {
	if !m.config.value.open {
		return 0
	}
	return min(len(m.config.value.menu.items), m.configPickRows())
}

// valuePickScroll walks the list, for a key or the wheel.
func (m *Model) valuePickScroll(d int) { m.config.value.menu.move(d, m.configPickRows()) }
