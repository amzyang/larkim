package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
)

// There is no Lark desktop counterpart for the reply drafts triage writes, drawn under
// a message's reactions; the float picker under C has none either.

// candDismissIcon closes the box of drafts a message drew.
const candDismissIcon = "✕"

// candidateKey names one draft for session-local ignore.
func candidateKey(c store.Candidate) string { return c.Mid + "\x00" + c.Text + "\x00" + c.Reaction }

func (m Model) visibleCandidates(cands []store.Candidate) []store.Candidate {
	if len(cands) == 0 {
		return nil
	}
	out := make([]store.Candidate, 0, len(cands))
	for _, c := range cands {
		if c.Answered {
			continue
		}
		if m.candIgnored != nil {
			if _, ok := m.candIgnored[candidateKey(c)]; ok {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func (m Model) candidatesForStyle() map[string][]store.Candidate {
	out := make(map[string][]store.Candidate)
	for _, c := range m.visibleCandidates(m.chatCands) {
		out[c.Mid] = append(out[c.Mid], c)
	}
	return out
}

func (m *Model) pruneChatCands(mid string) {
	m.chatCands = slices.DeleteFunc(m.chatCands, func(c store.Candidate) bool { return c.Mid == mid })
}

// candidateRows draws the drafts owed on a message in a box under its reaction
// strip: the suggested reactions share one line, each reply takes its own. The
// border is what tells a draft from the message's own text, so no line needs
// an icon to say so; dismiss is the one button, on the first line's right,
// because it answers the whole box.
func candidateRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	if standsAlone(x) {
		return nil
	}
	cs := st.candidates[x.MessageID]
	if len(cs) == 0 {
		return nil
	}
	var reacts, replies []store.Candidate
	for _, c := range cs {
		if c.Reaction != "" {
			reacts = append(reacts, c)
		} else {
			replies = append(replies, c)
		}
	}
	dw := lipgloss.Width(candDismissIcon)
	// What the dismiss takes on the first line: a gap and the icon.
	rsv := 1 + dw
	// Two border columns and a padding column inside each.
	room := st.inner() - 4
	// The reactions line packs what fits beside the dismiss; triage offers a
	// handful, so the rest are cut rather than wrapped.
	var strip []rowSeg
	var stripZones []clickZone
	sw := 0
	for _, c := range reacts {
		segs := candidateEmoji(c.Reaction, st)
		w := segsWidth(segs)
		gap := min(sw, 1)
		if sw+gap+w+rsv > room {
			break
		}
		if gap > 0 {
			strip = append(strip, rowSeg{text: " "})
		}
		stripZones = append(stripZones, clickZone{x0: sw + gap, x1: sw + gap + w, cand: candZone{c: c}})
		strip = append(strip, segs...)
		sw += gap + w
	}
	if len(strip) == 0 && len(replies) == 0 {
		return nil
	}
	// cw is the width inside the border, the first line's dismiss counted.
	cw := sw + rsv
	previews := make([]string, len(replies))
	for i, c := range replies {
		previews[i] = firstDisplayLine(strings.TrimSpace(c.Text))
		w := lipgloss.Width(previews[i])
		if i == 0 && len(strip) == 0 {
			w += rsv
		}
		cw = max(cw, w)
	}
	cw = max(min(cw, room), rsv)

	bar := stDim.Render("│")
	gutter := bar + " "
	gw := lipgloss.Width(gutter)
	edge := func(l, r string) msgRow {
		return msgRow{lead: g.take(), idx: idx, text: stDim.Render(l + strings.Repeat("─", cw+2) + r)}
	}
	// line frames content no wider than cw, padding it out and putting the
	// dismiss at its right edge when asked; zones come in content columns.
	line := func(segs []rowSeg, zones []clickZone, dismiss bool, c store.Candidate) msgRow {
		w, tail := cw, " "+bar
		if dismiss {
			w -= dw
			tail = stDim.Render(candDismissIcon) + tail
			zones = append(zones, clickZone{x0: w, x1: cw, cand: candZone{c: c, dismiss: true}})
		}
		segs = slices.Concat([]rowSeg{{text: gutter}}, segs, []rowSeg{{text: strings.Repeat(" ", w-segsWidth(segs)) + tail}})
		row := msgRow{lead: g.take(), idx: idx}
		row.text, row.segs = oneLineOr(segs)
		for i := range zones {
			zones[i].x0 += gw
			zones[i].x1 += gw
		}
		row.addZones(zones)
		return row
	}

	rows := []msgRow{edge("╭", "╮")}
	if len(strip) > 0 {
		rows = append(rows, line(strip, stripZones, true, reacts[0]))
	}
	for i, c := range replies {
		// The dismiss goes on the first line, a reply's when no reaction has one.
		dismiss := len(rows) == 1
		w := cw
		if dismiss {
			w -= rsv
		}
		// The whole line is the target, not just the words on it.
		zone := clickZone{x0: 0, x1: w, cand: candZone{c: c}}
		rows = append(rows, line([]rowSeg{{text: truncate(previews[i], w)}}, []clickZone{zone}, dismiss, c))
	}
	return append(rows, edge("╰", "╯"))
}

// candidateEmoji draws a reaction candidate as an emoji standing in text, not
// as a chip: it is not on the message yet, so it carries no reaction tint. The
// client's picture alone, or its name in brackets where the terminal draws none.
func candidateEmoji(key string, st msgStyle) []rowSeg {
	e, ok := emoji.ByKey(key)
	if !ok {
		return []rowSeg{{text: "[" + key + "]"}}
	}
	if pic := st.emojiInline(e.Key); pic.cols > 0 {
		return []rowSeg{{pic: pic}}
	}
	return []rowSeg{{text: "[" + e.Name() + "]"}}
}

// candZone is a press inside a message's box of drafts: one draft, or the
// dismiss that answers them all.
type candZone struct {
	c       store.Candidate
	dismiss bool
}

// pressCandidate answers a press inside the box. A reaction goes on the
// message there and then, as the picker's does; a reply is only a starting
// point, so it lands in the composer answering the message and waits for
// Enter.
func (m Model) pressCandidate(p pane, z candZone, x store.Message) (tea.Model, tea.Cmd) {
	switch {
	case z.dismiss:
		return m.dismissCandidates(z.c.Mid)
	case z.c.Reaction != "":
		return m.reactCandidate(z.c, x)
	}
	return m.replyWithCandidate(p, z.c, x)
}

// dismissCandidates drops every draft owed on mid. The ignore set covers a
// reload that lands before the store has let go of the row.
func (m Model) dismissCandidates(mid string) (tea.Model, tea.Cmd) {
	if m.candIgnored == nil {
		m.candIgnored = map[string]struct{}{}
	}
	for _, c := range m.chatCands {
		if c.Mid == mid {
			m.candIgnored[candidateKey(c)] = struct{}{}
		}
	}
	m.pruneChatCands(mid)
	if m.mode == modeCandidates {
		// The picker lists the whole chat as the store read it, while
		// chatCands holds only what the pane draws, so it prunes its own rows.
		if rows := slices.DeleteFunc(slices.Clone(m.cand.items), func(c store.Candidate) bool { return c.Mid == mid }); len(rows) == 0 {
			m = m.closeCandidates()
		} else {
			m.cand = fillMenu(rows, candSpec)
		}
	}
	m.rebuildMessages()
	return m.notify("drafts dismissed", false), clearCandidate(m.deps, mid)
}

// replyWithCandidate opens a reply to x holding the draft's wording, the way
// the picker hands one over. A reply inside a topic stays in it.
func (m Model) replyWithCandidate(p pane, c store.Candidate, x store.Message) (tea.Model, tea.Cmd) {
	m.focus = p
	mm, cmd := m.startInsert(&x, x.ThreadID != "" && x.MessagePosition < 0)
	m = mm.(Model)
	if m.mode != modeInsert {
		return m, cmd
	}
	m.seedComposer(c)
	return m.notify("draft placed in the composer: Enter to send", false), cmd
}

// reactCandidate puts a reaction candidate on its source message. It goes
// through the same press every reaction does, carrying the mid so that the
// drafts go once Feishu has taken it and stay if Feishu refuses. A candidate
// only ever adds: one that is already the reader's is said so, not toggled
// off.
func (m Model) reactCandidate(c store.Candidate, x store.Message) (tea.Model, tea.Cmd) {
	if mineOn(m.drawnChips(x), c.Reaction) {
		return m.notify("already reacted with that", false), nil
	}
	return m.reactAs(x, c.Reaction, c.Mid)
}
