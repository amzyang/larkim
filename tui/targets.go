package tui

import (
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// targets is the chooser listing everything the selected message leads to,
// open only in modeTarget.
//
// The Feishu client never needs one: there a person presses the thing itself,
// and larkim's mouse does the same. The keyboard cannot, because the cursor
// picks a message rather than a spot inside one, and a card routinely draws
// a dozen links — so the targets are named and chosen instead of guessed at.
type targets struct {
	zones []clickZone
	idx   int
	top   int
}

// openTargets arms the chooser. It is only ever reached with more than one
// target, a single one being opened where it was found.
//
// The message itself closes the list. Not everything a message draws can be
// handed over — a link inside a card's table is laid out by lipgloss, which
// gives back no columns to hang a target on — so the last line is one the
// reader can always fall back to, rather than a list they have to judge the
// completeness of.
func (m Model) openTargets(zs []clickZone) (tea.Model, tea.Cmd) {
	if sel, ok := m.selected(); ok {
		link := feishuChatLink(sel.ChatID, sel.MessagePosition)
		if !slices.ContainsFunc(zs, func(z clickZone) bool { return z.urls[0] == link }) {
			zs = append(zs, clickZone{urls: []string{link},
				label: "open in Feishu", note: "opening in Feishu"})
		}
	}
	m.mode = modeTarget
	m.targets = targets{zones: zs}
	m.layout()
	return m, nil
}

// closeTargets puts the composer back.
func (m Model) closeTargets() Model {
	m.mode = modeNormal
	m.targets = targets{}
	m.layout()
	return m
}

// targetRows is how many targets the chooser shows: the composer's box less
// the line it is titled by, so pressing o moves nothing above it.
func (m Model) targetRows() int { return max(1, m.composerHeight()-1) }

// onTargetKey drives the chooser. Movement is vim's, because nothing here is
// typed into; the digits reach the first nine straight off, which is what a
// list this short is for.
func (m Model) onTargetKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch s := k.String(); s {
	case "esc", "q", "ctrl+[":
		return m.closeTargets(), nil
	case "enter", "o", "l", "right":
		return m.chooseTarget(m.targets.idx)
	case "j", "down", "ctrl+n":
		m.targets.move(1, m.targetRows())
	case "k", "up", "ctrl+p":
		m.targets.move(-1, m.targetRows())
	case "g", "home":
		m.targets.move(-len(m.targets.zones), m.targetRows())
	case "G", "end":
		m.targets.move(len(m.targets.zones), m.targetRows())
	case "ctrl+d":
		m.targets.move(m.targetRows()/2, m.targetRows())
	case "ctrl+u":
		m.targets.move(-m.targetRows()/2, m.targetRows())
	default:
		// A digit means the line it is drawn on, so it can never reach a
		// target that scrolled out of sight.
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 9 {
			return m.chooseTarget(m.targets.top + n - 1)
		}
	}
	return m, nil
}

// chooseTarget hands over the one at i, if the list reaches that far.
func (m Model) chooseTarget(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.targets.zones) {
		return m, nil
	}
	z := m.targets.zones[i]
	return m.closeTargets(), openZone(m.deps, z)
}

// move walks the list and scrolls to keep the cursor on screen.
func (t *targets) move(d, rows int) {
	if len(t.zones) == 0 {
		return
	}
	t.idx = clamp(t.idx+d, 0, len(t.zones)-1)
	t.top = clamp(t.top, max(0, t.idx-rows+1), t.idx)
}

// visible is the slice of targets the box has room for.
func (m Model) targetsVisible() []clickZone {
	lo := min(m.targets.top, len(m.targets.zones))
	return m.targets.zones[lo:min(len(m.targets.zones), lo+m.targetRows())]
}

// renderTargets draws the chooser in the composer's place, filling exactly the
// box the composer would have drawn.
func (m Model) renderTargets() string {
	w := m.width - 2
	rows := m.targetRows()
	count := stDim.Render(strconv.Itoa(m.targets.idx+1) + "/" + strconv.Itoa(len(m.targets.zones)))
	lines := []string{padBetween(stBold.Render("open")+stAccent.Render(" › ")+
		stDim.Render("j/k move · enter open · 1-9 jump · esc cancel"), count, w)}
	for i, z := range m.targetsVisible() {
		lines = append(lines, m.targetLine(z, m.targets.top+i, w))
	}
	for len(lines) < rows+1 {
		lines = append(lines, fit("", w))
	}
	return paneStyle(true, w).Render(strings.Join(lines[:rows+1], "\n"))
}

// targetLine names one target: the number that reaches it, what it is, and
// where it leads. The hint is not decoration — a link is drawn as its label
// alone, so without it the reader would be choosing blind.
func (m Model) targetLine(z clickZone, i int, w int) string {
	mark, num := "  ", stDim.Render(" ")
	if n := i - m.targets.top; n < 9 {
		num = stDim.Render(strconv.Itoa(n + 1))
	}
	label := z.label
	if label == "" {
		label = z.urls[0]
	}
	style := lipgloss.NewStyle()
	if i == m.targets.idx {
		mark, style = stAccent.Render("› "), stBold
	}
	hint := stDim.Render(targetHint(z.urls))
	head := mark + num + " "
	room := max(4, w-lipgloss.Width(head)-lipgloss.Width(hint)-1)
	return padBetween(head+style.Render(truncate(flatten(label), room)), hint, w)
}

// targetHint says where a target leads, in the terms the reader chooses by:
// the host for a link, the folder for a file on this machine, and the client
// for an applink, which is not a place a browser would go.
func targetHint(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	if len(urls) > 1 {
		return strconv.Itoa(len(urls)) + " files"
	}
	u := urls[0]
	switch {
	case strings.HasPrefix(u, "lark://"):
		return "Feishu"
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
			return parsed.Host
		}
		return "link"
	case filepath.IsAbs(u):
		return filepath.Base(filepath.Dir(u))
	}
	return ""
}
