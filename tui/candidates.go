package tui

import (
	"context"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"

	tea "charm.land/bubbletea/v2"
)

// the drafts picker lists the reply drafts triage wrote for the open
// chat, open only in modeCandidates. It stands over the panes as a list the way
// a completion does, so the draft a pick would replace stays in sight under it,
// and the focused draft is read whole in the box beside it. It holds no text of
// its own: a candidate is picked, not edited — editing is what the composer it
// lands in is for.

// candSpec draws a reply by its opening line and a reaction by its emoji,
// numbered for the digit that picks it. One the reader has answered past is
// dimmed: it is a wording to borrow, not a reply still owed.
var candSpec = menuSpec[store.Candidate]{
	row: func(c store.Candidate) offer {
		var o offer
		if c.Reaction != "" {
			e, _ := emoji.ByKey(c.Reaction)
			o = offer{icon: emojiIcon(e), name: e.Name()}
		} else {
			o = offer{name: firstDisplayLine(c.Text)}
		}
		if c.Answered {
			o.name = stDim.Render(o.name)
		}
		return o
	},
	info: func(c store.Candidate) []string {
		var marks []string
		if c.Reaction != "" {
			marks = append(marks, "reaction")
		}
		if c.Format == "markdown" {
			marks = append(marks, "markdown")
		}
		if c.Answered {
			marks = append(marks, "answered after")
		}
		if c.Reaction != "" {
			e, _ := emoji.ByKey(c.Reaction)
			return infoLines(e.Name(), emojiKey(e), strings.Join(marks, " · "))
		}
		return infoLines(strings.TrimSpace(c.Text), strings.Join(marks, " · "))
	},
	digits: digitRow,
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
// wording is a starting point, not a verdict. A digit picks the row it is
// drawn beside, the way the targets chooser's do.
func (m Model) onCandidatesKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.candRows()
	switch s := k.String(); s {
	case "esc", "q", "ctrl+[":
		return m.closeCandidates(), nil
	case "enter", "o", "l", "right":
		return m.chooseCandidate(m.cand.idx)
	case "j", "down", "ctrl+n":
		m.cand.move(1, rows)
	case "k", "up", "ctrl+p":
		m.cand.move(-1, rows)
	case "g", "home":
		m.cand.move(-len(m.cand.items), rows)
	case "G", "end":
		m.cand.move(len(m.cand.items), rows)
	default:
		if i, ok := m.cand.pick(s, rows); ok {
			return m.chooseCandidate(i)
		}
	}
	return m, nil
}

// chooseCandidate hands the reply at i to the composer. The mid is remembered
// so the send that leaves the composer can drop the row: the message has been
// answered, and its other drafts with it. A reaction has no wording to edit,
// so it goes on the source message there and then.
func (m Model) chooseCandidate(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.cand.items) {
		return m, nil
	}
	c := m.cand.items[i]
	m = m.closeCandidates()
	if c.Reaction != "" {
		for _, msgs := range [][]store.Message{m.msgs, m.thread} {
			if j := indexOfID(msgs, c.Mid); j >= 0 {
				return m.reactCandidate(c, msgs[j])
			}
		}
		return m.notify("the message it answers is not loaded", true), nil
	}
	m.areap().SetValue(strings.TrimSpace(c.Text))
	m.replan()
	m.candFilled = c.Mid
	return m.notify("candidate placed in the composer: i to edit, Enter to send", false), nil
}

// closeCandidates puts the composer back.
func (m Model) closeCandidates() Model {
	m.mode = modeNormal
	m.cand = menu[store.Candidate]{}
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

// candRows is how many drafts the list shows. It stands over the pane, so it
// costs the composer's box nothing.
func (m Model) candRows() int {
	if m.mode != modeCandidates {
		return 0
	}
	return m.floatRoom(len(m.cand.items), m.cand.maxRows())
}

// firstDisplayLine is the whole of a single-line draft and the opening of a
// longer one — enough to tell the candidates apart, which is all a row is for.
func firstDisplayLine(s string) string {
	if before, _, found := strings.Cut(s, "\n"); found {
		return before + " …"
	}
	return s
}
