package tui

import (
	"slices"
	"strings"
	"time"
	"uuid"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
)

// There is no Lark desktop counterpart for the reply drafts triage writes, drawn under
// a message's reactions; the float picker under C has none either.

const (
	candIgnoreIcon = "\uf00d"
	candSendIcon   = "\uf1d8"
)

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

func (m Model) visibleCountForMid(mid string) int {
	n := 0
	for _, c := range m.visibleCandidates(m.chatCands) {
		if c.Mid == mid {
			n++
		}
	}
	return n
}

// candidateRows draws pending drafts under a message's reaction strip: ignore
// icon, send icon, then the opening line of a reply or the emoji of a
// reaction.
func candidateRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	if standsAlone(x) {
		return nil
	}
	cs := st.candidates[x.MessageID]
	if len(cs) == 0 {
		return nil
	}
	var rows []msgRow
	for _, c := range cs {
		ignore := stDim.Render(fit(candIgnoreIcon, pickerIconCols))
		send := stDim.Render(fit(candSendIcon, pickerIconCols))
		iw, sw := lipgloss.Width(ignore), lipgloss.Width(send)
		row := msgRow{lead: g.take(), idx: idx}
		if c.Reaction != "" {
			row.segs = append([]rowSeg{{text: ignore + send + " "}}, candidateEmoji(c.Reaction, st)...)
		} else {
			preview := firstDisplayLine(c.Text)
			if c.Format == "markdown" {
				preview += stDim.Render(" markdown")
			}
			row.text = ignore + send + " " + preview
		}
		row.addZones([]clickZone{
			{x0: 0, x1: iw, cand: candZone{c: c}},
			{x0: iw, x1: iw + sw, cand: candZone{c: c, send: true}},
		})
		rows = append(rows, row)
	}
	return rows
}

// candidateEmoji draws a reaction candidate the way the strip above it draws
// a chip's emoji: the client's picture, its name in brackets where the
// terminal draws none, and the name after it either way, since a suggestion
// has to be read before it is sent.
func candidateEmoji(key string, st msgStyle) []rowSeg {
	e, _ := emoji.ByKey(key)
	name := stDim.Render(" " + e.Name())
	pic, label := emojiFace(key, st)
	if pic.cols > 0 {
		return []rowSeg{{pic: pic}, {text: name}}
	}
	return []rowSeg{{text: label + name}}
}

type candZone struct {
	c    store.Candidate
	send bool
}

func (m Model) ignoreInlineCandidate(c store.Candidate) (Model, tea.Cmd) {
	if m.candIgnored == nil {
		m.candIgnored = map[string]struct{}{}
	}
	m.candIgnored[candidateKey(c)] = struct{}{}
	var cmd tea.Cmd
	if m.visibleCountForMid(c.Mid) == 0 {
		m.pruneChatCands(c.Mid)
		cmd = clearCandidate(m.deps, c.Mid)
	}
	if m.mode == modeCandidates {
		rows := m.visibleCandidates(m.chatCands)
		if len(rows) == 0 {
			m = m.closeCandidates()
		} else {
			m.cand = fillMenu(rows, candSpec)
		}
	}
	m.rebuildMessages()
	return m.notify("draft dismissed", false), cmd
}

func (m Model) sendInlineCandidate(c store.Candidate, x store.Message) (Model, tea.Cmd) {
	if c.Reaction != "" {
		mm, cmd := m.reactCandidate(c, x)
		return mm.(Model), cmd
	}
	text := strings.TrimSpace(c.Text)
	if text == "" {
		return m.notify("empty draft", true), nil
	}
	p, err := m.files.planCandidate(c)
	if err != nil {
		return m.notify(err.Error(), true), nil
	}
	it := outboxItem{localID: uuid.New().String(), chatID: x.ChatID, replyTo: x.MessageID,
		msgType: p.kind.msgType(), send: p.send, body: p.body, images: p.uploads(), file: p.file,
		createMs: time.Now().UnixMilli()}
	if x.ThreadID != "" && x.MessagePosition < 0 {
		it.inThread, it.threadID = true, x.ThreadID
	}
	cmd := m.sendItem(it)
	m.enqueue(it)
	m.candFilled = c.Mid
	m.pruneChatCands(c.Mid)
	m.refreshPanes()
	m.rebuildMessages()
	return m.notify("sending reply draft", false), cmd
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
