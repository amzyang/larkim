package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/amzyang/larkim/store"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// candPicker lists the reply drafts lark-watch is holding for the open chat,
// open only in modeCandidates. It fills the composer's box the way the forward
// and targets choosers do, and holds no text of its own: a candidate is picked,
// not edited — editing is what the composer it lands in is for.
type candPicker struct {
	rows []store.Candidate
	idx  int
	top  int
}

// openCandidates asks the store for the chat's pending drafts. The picker
// itself opens on candidatesLoadedMsg, so what it lists is what the store says
// rather than what the keypress guessed.
func (m Model) openCandidates() (tea.Model, tea.Cmd) {
	if m.openingChat() == "" {
		return m.notify("open a chat first", true), nil
	}
	return m, loadCandidates(m.deps, m.openingChat())
}

func loadCandidates(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		rows, err := d.Store.ChatCandidates(context.Background(), chatID, d.Self)
		if err != nil {
			return errMsg{err}
		}
		return candidatesLoadedMsg{rows: rows}
	}
}

// onCandidatesKey drives the picker. The keys are the ones every list here
// takes, and enter is the composer hand-off the assistant's drafts use: the
// wording is a starting point, not a verdict.
func (m Model) onCandidatesKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch s := k.String(); s {
	case "esc", "q", "ctrl+[":
		return m.closeCandidates(), nil
	case "enter", "o", "l", "right":
		return m.chooseCandidate(m.cand.idx)
	case "j", "down", "ctrl+n":
		m.cand.move(1, m.candRows())
	case "k", "up", "ctrl+p":
		m.cand.move(-1, m.candRows())
	case "g", "home":
		m.cand.move(-len(m.cand.rows), m.candRows())
	case "G", "end":
		m.cand.move(len(m.cand.rows), m.candRows())
	}
	return m, nil
}

// chooseCandidate hands the draft at i to the composer. The mid is remembered
// so the send that leaves the composer can drop the mirrored row — the card on
// Feishu stays lark-watch's and resolves on its own.
func (m Model) chooseCandidate(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.cand.rows) {
		return m, nil
	}
	c := m.cand.rows[i]
	m = m.closeCandidates()
	m.areap().SetValue(strings.TrimSpace(c.Text))
	m.replan()
	m.candFilled = c.Mid
	return m.notify("candidate placed in the composer: i to edit, Enter to send", false), nil
}

// closeCandidates puts the composer back.
func (m Model) closeCandidates() Model {
	m.mode = modeNormal
	m.cand = candPicker{}
	m.layout()
	return m
}

// clearCandidate drops the mirrored drafts of the mid a composer send was
// seeded from. Fire and forget: the picker is closed by then, and the badge
// goes with the next revision bump.
func clearCandidate(d Deps, mid string) tea.Cmd {
	return func() tea.Msg {
		if err := d.Store.ClearCandidate(context.Background(), mid); err != nil {
			d.log().Error("clear candidate", "err", err)
		}
		return candidateClearedMsg{}
	}
}

func (c *candPicker) move(d, rows int) { moveCursor(&c.idx, &c.top, d, len(c.rows), rows) }

// candRows is how many candidates the box shows: the composer's height less
// the line the list is titled by.
func (m Model) candRows() int { return max(1, m.composerHeight()-1) }

func (m Model) candVisible() []store.Candidate {
	return window(m.cand.rows, m.cand.top, m.candRows())
}

// renderCandidates draws the picker in the composer's place, filling exactly
// the box the composer would have drawn.
func (m Model) renderCandidates() string {
	w := m.bandWidth(m.side) - 2
	rows := m.candRows()
	count := stDim.Render(strconv.Itoa(m.cand.idx+1) + "/" + strconv.Itoa(len(m.cand.rows)))
	lines := []string{padBetween(stBold.Render("drafts")+stAccent.Render(" › ")+
		stDim.Render("j/k move · enter fill composer · esc cancel"), count, w)}
	for i, c := range m.candVisible() {
		lines = append(lines, m.candLine(c, m.cand.top+i, w))
	}
	for len(lines) < rows+1 {
		lines = append(lines, fit("", w))
	}
	return paneStyle(true).Render(strings.Join(lines[:rows+1], "\n"))
}

// candLine stands one candidate up in a row: the numeral the Feishu card gives
// the same draft, the first line of its wording, and the marks that change what
// picking it means — markdown renders as rich text, and a draft the reader has
// already answered past is a wording to borrow, not a reply still owed.
func (m Model) candLine(c store.Candidate, i int, w int) string {
	mark := "  "
	if i == m.cand.idx {
		mark = stAccent.Render("› ")
	}
	head := mark + candNumeral(m.cand.top+i) + " "
	text := firstDisplayLine(c.Text)
	room := max(4, w-lipgloss.Width(head)-2)
	style := lipgloss.NewStyle()
	if i == m.cand.idx {
		style = stBold
	}
	line := style.Render(truncate(text, room))
	if c.Format == "markdown" {
		line += stDim.Render(" md")
	}
	if c.Replied {
		line += stDim.Render(" · replied after")
	}
	return head + line
}

// candNumeral counts candidates the way the Feishu card does. lark-watch stops
// at three; a longer list keeps counting, in plain digits.
func candNumeral(i int) string {
	if i >= 0 && i <= 19 {
		return string(rune('①' + i))
	}
	return strconv.Itoa(i + 1)
}

// firstDisplayLine is the whole of a single-line draft and the opening of a
// longer one — enough to tell the candidates apart, which is all a row is for.
func firstDisplayLine(s string) string {
	if before, _, found := strings.Cut(s, "\n"); found {
		return before + " …"
	}
	return s
}
