package tui

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/store"
)

// silenceCount is what one rule silences among the messages already stored.
type silenceCount struct{ matched, lastMs int64 }

// silenceTab is the Silence page of :config: the configured rules, each named
// by the chat and the sender it points at, and the form a rule is written in.
//
// It edits m.cfg.Silence and the file together, never the store's own rules:
// the process holding daemon.lock stamps the flags from the rules it started
// with, and every other writer has to stamp from the same ones until it
// restarts.
type silenceTab struct {
	idx, top int
	// counts is keyed by each rule's own fingerprint, so a reply that lands
	// after an edit fills in only the rules still standing.
	counts map[string]silenceCount
	// confirmDelete holds the list on a question: d rewrites the file, so it
	// asks before it does.
	confirmDelete bool
	form          silenceForm
}

// silenceField is one line of the rule form.
type silenceField int

const (
	fieldChat silenceField = iota
	fieldSender
	fieldContains
	silenceFields
)

var silenceFieldNames = []string{"chat", "sender", "contains"}

// silenceForm is the rule being written. A zero value is closed.
type silenceForm struct {
	open bool
	// at is the rule being edited, -1 for a new one.
	at       int
	rule     store.SilenceRule
	field    silenceField
	contains textinput.Model
	pick     silencePick
	// err is a rule the configuration refused, kept beside what was typed.
	err string
}

// silencePick chooses the chat or the sender of the form's focused field.
type silencePick struct {
	open     bool
	input    textinput.Model
	hits     []silenceHit
	idx, top int
}

type silenceHit struct {
	id, name string
	mark     []int
}

type silenceMatchesMsg struct{ counts map[string]silenceCount }

// silenceKey is the key a rule's count is filed under.
func silenceKey(r store.SilenceRule) string { return store.SilenceRules{r}.Fingerprint() }

// loadSilenceMatches counts what each rule silences, which is how a typo in an
// id shows: a rule naming nobody matches nothing.
func loadSilenceMatches(d Deps, rules store.SilenceRules) tea.Cmd {
	return func() tea.Msg {
		counts := make(map[string]silenceCount, len(rules))
		for _, r := range rules {
			n, last, err := d.Store.SilenceMatches(context.Background(), r)
			if err != nil {
				d.log().Error("silence matches", "err", err)
				return silenceMatchesMsg{counts: counts}
			}
			counts[silenceKey(r)] = silenceCount{matched: n, lastMs: last}
		}
		return silenceMatchesMsg{counts: counts}
	}
}

func (m Model) onSilenceMatches(msg silenceMatchesMsg) Model {
	if m.config.silence.counts == nil {
		m.config.silence.counts = make(map[string]silenceCount, len(msg.counts))
	}
	maps.Copy(m.config.silence.counts, msg.counts)
	return m
}

func ruleCount(n int) string {
	if n == 1 {
		return "1 rule"
	}
	return strconv.Itoa(n) + " rules"
}

func (m *Model) silenceMove(d int) {
	t := &m.config.silence
	t.confirmDelete = false
	moveCursor(&t.idx, &t.top, d, len(m.cfg.Silence), m.configRows())
}

// onSilenceKey drives the Silence page. The form and its picker own their keys
// whole, the way the General editor does; the list reads bare letters.
func (m Model) onSilenceKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := &m.config.silence
	if t.form.open {
		return m.onSilenceFormKey(k)
	}
	s := k.String()
	if t.confirmDelete {
		t.confirmDelete = false
		if s == "y" {
			return m.deleteSilence(), m.silenceRecount()
		}
		return m, nil
	}
	switch s {
	case "esc", "q":
		return m.closeConfig(), nil
	case "tab":
		return m.switchConfigTab(1)
	case "shift+tab":
		return m.switchConfigTab(-1)
	case "a":
		return m.openSilenceForm(-1)
	case "enter", "i":
		if t.idx < len(m.cfg.Silence) {
			return m.openSilenceForm(t.idx)
		}
	case "d":
		t.confirmDelete = t.idx < len(m.cfg.Silence)
	case "j", "down", "ctrl+n":
		m.silenceMove(1)
	case "k", "up", "ctrl+p":
		m.silenceMove(-1)
	case "ctrl+d", "pgdown":
		m.silenceMove(m.configRows() / 2)
	case "ctrl+u", "pgup":
		m.silenceMove(-m.configRows() / 2)
	case "g", "home":
		m.silenceMove(-len(m.cfg.Silence))
	case "G", "end":
		m.silenceMove(len(m.cfg.Silence))
	}
	return m, nil
}

// openSilenceForm opens the form on rule at, or on an empty rule for -1. It
// starts on contains when the rule has one, so an edit of the text is a keypress
// away; otherwise on chat, which is where a new rule usually begins.
func (m Model) openSilenceForm(at int) (tea.Model, tea.Cmd) {
	f := silenceForm{open: true, at: at, contains: m.newQueryInput()}
	if at >= 0 {
		f.rule = m.cfg.Silence[at]
	}
	f.contains.SetValue(f.rule.Contains)
	f.contains.SetWidth(m.silenceValueWidth())
	m.config.silence.form = f
	if f.rule.Contains != "" {
		return m.focusSilenceField(fieldContains)
	}
	return m, nil
}

func (m Model) focusSilenceField(to silenceField) (tea.Model, tea.Cmd) {
	f := &m.config.silence.form
	f.field, f.err = to, ""
	if to != fieldContains {
		f.contains.Blur()
		return m, nil
	}
	cmd := f.contains.Focus()
	return m, cmd
}

func (m Model) onSilenceFormKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.config.silence.form
	if f.pick.open {
		return m.onSilencePickKey(k)
	}
	s := k.String()
	switch s {
	case "esc":
		m.config.silence.form = silenceForm{}
		return m, nil
	case "tab", "down", "ctrl+n":
		return m.focusSilenceField((f.field + 1) % silenceFields)
	case "shift+tab", "up", "ctrl+p":
		return m.focusSilenceField((f.field + silenceFields - 1) % silenceFields)
	}
	if f.field == fieldContains {
		if s == "enter" {
			return m.saveSilenceForm()
		}
		var cmd tea.Cmd
		f.contains, cmd = f.contains.Update(k)
		return m, cmd
	}
	switch s {
	case "enter":
		return m.openSilencePick()
	case "backspace", "delete":
		*f.idField() = ""
		f.err = ""
	}
	return m, nil
}

// idField is the rule's id the focused field writes: chat or sender.
func (f *silenceForm) idField() *string {
	if f.field == fieldSender {
		return &f.rule.Sender
	}
	return &f.rule.Chat
}

func (m Model) openSilencePick() (tea.Model, tea.Cmd) {
	f := &m.config.silence.form
	f.pick = silencePick{open: true, input: m.newQueryInput()}
	f.pick.hits = m.silenceSearch(f.field, "")
	cmd := f.pick.input.Focus()
	return m, cmd
}

func (m Model) onSilencePickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.config.silence.form
	p := &f.pick
	rows := m.silencePickRows()
	switch k.String() {
	case "esc":
		f.pick = silencePick{}
		return m, nil
	case "enter":
		// A query that names nothing is taken as the id itself: a bot that
		// has only ever posted is not always among the contacts.
		id := strings.TrimSpace(p.input.Value())
		if p.idx < len(p.hits) {
			id = p.hits[p.idx].id
		}
		if id != "" {
			*f.idField() = id
			f.err = ""
		}
		f.pick = silencePick{}
		return m, nil
	case "up", "ctrl+p":
		moveCursor(&p.idx, &p.top, -1, len(p.hits), rows)
		return m, nil
	case "down", "ctrl+n":
		moveCursor(&p.idx, &p.top, 1, len(p.hits), rows)
		return m, nil
	}
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(k)
	if q := p.input.Value(); q != before {
		p.hits, p.idx, p.top = m.silenceSearch(f.field, q), 0, 0
	}
	return m, cmd
}

// silenceSearch narrows the chats or the people a rule can name, in the order
// the forward chooser offers them: the chat list's own, then the contacts with
// the bots among them, since a bot is the usual sender a rule is written for.
func (m Model) silenceSearch(field silenceField, query string) []silenceHit {
	ix := fuzzy.NewIndex()
	var out []silenceHit
	if field == fieldChat {
		for _, c := range m.chats {
			if len(out) == fwdLimit {
				break
			}
			name := flatten(c.Name)
			if mark, ok := ix.Match(c.ChatID, name, query); ok {
				out = append(out, silenceHit{id: c.ChatID, name: name, mark: mark})
			}
		}
		return out
	}
	for _, p := range m.contacts {
		if len(out) == fwdLimit {
			break
		}
		if p.Name == "" {
			continue
		}
		if mark, ok := ix.Match(p.OpenID, p.Name, query); ok {
			out = append(out, silenceHit{id: p.OpenID, name: p.Name, mark: mark})
		}
	}
	return out
}

// saveSilenceForm writes the rule in place of the one it was opened on, or
// after the rest for a new one.
func (m Model) saveSilenceForm() (tea.Model, tea.Cmd) {
	f := &m.config.silence.form
	f.rule.Contains = strings.TrimSpace(f.contains.Value())
	next := slices.Clone(m.cfg.Silence)
	at := f.at
	if at < 0 {
		next, at = append(next, f.rule), len(next)
	} else {
		next[at] = f.rule
	}
	if err := m.writeSilence(next); err != nil {
		f.err = err.Error()
		return m, nil
	}
	m.config.silence.form = silenceForm{}
	moveCursor(&m.config.silence.idx, &m.config.silence.top, at-m.config.silence.idx, len(next), m.configRows())
	return m, m.silenceRecount()
}

func (m Model) deleteSilence() Model {
	t := &m.config.silence
	next := slices.Delete(slices.Clone(m.cfg.Silence), t.idx, t.idx+1)
	if err := m.writeSilence(next); err != nil {
		return m.notify("silence: "+err.Error(), true)
	}
	moveCursor(&t.idx, &t.top, 0, len(next), m.configRows())
	return m
}

// writeSilence judges the rules, writes them, and only then takes them into
// m.cfg, in the order commitConfig keeps: a set the file refused must not be
// the one this panel shows.
func (m *Model) writeSilence(next store.SilenceRules) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if err := config.SetFileValue(m.deps.ConfigPath, "silence", next); err != nil {
		return err
	}
	m.cfg.Silence = next
	*m = m.notify("silence · "+ruleCount(len(next))+" · next start", false)
	return nil
}

func (m Model) silenceRecount() tea.Cmd { return loadSilenceMatches(m.deps, m.cfg.Silence) }

// chatName and senderName resolve a rule's ids for the list. An id nothing
// here names comes back unresolved, which is how a typo stands out.
func (m Model) chatName(id string) (string, bool) {
	i := slices.IndexFunc(m.chats, func(c store.Chat) bool { return c.ChatID == id })
	if i < 0 || m.chats[i].Name == "" {
		return id, false
	}
	return flatten(m.chats[i].Name), true
}

func (m Model) senderName(id string) (string, bool) {
	i := slices.IndexFunc(m.contacts, func(c store.Contact) bool { return c.OpenID == id })
	if i < 0 || m.contacts[i].Name == "" {
		return id, false
	}
	return m.contacts[i].Name, true
}

// silenceIDCell draws one id field of a rule: a dash when it is unset, the
// name when something here carries one, the bare id in red when nothing does.
func silenceIDCell(id string, resolve func(string) (string, bool), w int) string {
	if id == "" {
		return stDim.Render(fit("—", w))
	}
	name, ok := resolve(id)
	if !ok {
		return stErr.Render(fit(truncate(id, w), w))
	}
	return fit(truncate(name, w), w)
}

// silenceMatchedWidth is the right-aligned count column.
const silenceMatchedWidth = 7

// silenceColumns splits the row after the cursor lead and the count into the
// chat, sender and contains columns, the text getting what is left.
func (m Model) silenceColumns() (chat, sender, contains int) {
	rest := max(9, m.configWidth()-2-silenceMatchedWidth-3*configGap)
	chat, sender = rest*3/10, rest/4
	return chat, sender, rest - chat - sender
}

func (m Model) silenceLines() []string {
	if m.config.silence.form.open {
		return m.silenceFormLines()
	}
	t := m.config.silence
	w := m.configWidth()
	gap := strings.Repeat(" ", configGap)
	cw, sw, tw := m.silenceColumns()
	var out []string
	for i, r := range window(m.cfg.Silence, t.top, m.configRows()) {
		row := t.top + i
		lead := "  "
		if row == t.idx {
			lead = stAccent.Render("❯ ")
		}
		text := stDim.Render(fit("—", tw))
		if r.Contains != "" {
			text = fit(truncate(r.Contains, tw), tw)
		}
		count := stDim.Render(strings.Repeat(" ", silenceMatchedWidth-1) + "…")
		if c, ok := t.counts[silenceKey(r)]; ok {
			style := lipgloss.NewStyle()
			if c.matched == 0 {
				style = stErr
			}
			n := strconv.FormatInt(c.matched, 10)
			count = style.Render(strings.Repeat(" ", max(0, silenceMatchedWidth-len(n))) + n)
		}
		out = append(out, fit(lead+silenceIDCell(r.Chat, m.chatName, cw)+gap+
			silenceIDCell(r.Sender, m.senderName, sw)+gap+text+gap+count, w))
	}
	return out
}

// silenceLabelWidth is the form's label column, as wide as its longest label.
const silenceLabelWidth = len("contains")

// silenceValueWidth is the form's value column, which the contains field opens
// at.
func (m Model) silenceValueWidth() int {
	return max(4, m.configWidth()-2-silenceLabelWidth-configGap)
}

// silenceFormHead is how many lines the form draws above its picker: its
// title, a blank, the three fields and a blank.
const silenceFormHead = 6

// silencePickRows is how many choices fit under the picker's query line.
func (m Model) silencePickRows() int { return max(1, m.configRows()-silenceFormHead-1) }

func (m Model) silenceFormLines() []string {
	f := m.config.silence.form
	w, valw := m.configWidth(), m.silenceValueWidth()
	gap := strings.Repeat(" ", configGap)
	title := "new rule"
	if f.at >= 0 {
		title = "rule " + strconv.Itoa(f.at+1) + " of " + strconv.Itoa(len(m.cfg.Silence))
	}
	out := []string{fit(stBold.Render(title), w), fit("", w)}
	for field := range silenceFields {
		lead := "  "
		if field == f.field {
			lead = stAccent.Render("❯ ")
		}
		var cell string
		switch field {
		case fieldChat:
			cell = silenceIDCell(f.rule.Chat, m.chatName, valw)
		case fieldSender:
			cell = silenceIDCell(f.rule.Sender, m.senderName, valw)
		case fieldContains:
			if field == f.field {
				cell, _ = m.inputCell(f.contains, valw)
			} else {
				cell = fit(truncate(f.contains.Value(), valw), valw)
			}
		}
		out = append(out, fit(lead+fit(silenceFieldNames[field], silenceLabelWidth)+gap+cell, w))
	}
	out = append(out, fit("", w))
	if !f.pick.open {
		return out
	}
	p := f.pick
	right := stDim.Render(strconv.Itoa(len(p.hits)))
	if len(p.hits) == 0 && strings.TrimSpace(p.input.Value()) != "" {
		right = stDim.Render("enter takes it as the id")
	}
	out = append(out, padBetween(m.silencePickPrompt()+p.input.View(), right, w))
	for i, h := range window(p.hits, p.top, m.silencePickRows()) {
		lead := "  "
		if p.top+i == p.idx {
			lead = stAccent.Render("❯ ")
		}
		out = append(out, padBetween(lead+markName(h.name, h.mark, lipgloss.NewStyle()), stDim.Render(h.id), w))
	}
	return out
}

func (m Model) silencePickPrompt() string {
	return stBold.Render(silenceFieldNames[m.config.silence.form.field]) + stAccent.Render(" › ")
}

// silenceDetail is the line under the page: the question d asks, the rule the
// form refused, or the focused rule's ids and what it has matched.
func (m Model) silenceDetail() string {
	w := m.configWidth()
	t := m.config.silence
	switch {
	case t.form.err != "":
		return fit(stErr.Render(truncate(t.form.err, w)), w)
	case t.form.open:
		help := "a chat_id or sender_id each, matched exactly"
		if t.form.field == fieldContains {
			help = "case-insensitive substring of the message text"
		}
		return fit(stDim.Render(truncate(help+" · every field set must match", w)), w)
	case t.confirmDelete:
		return fit(stErr.Render("delete this rule? y/n"), w)
	case len(m.cfg.Silence) == 0:
		return fit(stDim.Render("no silence rules · a to add"), w)
	}
	r := m.cfg.Silence[min(t.idx, len(m.cfg.Silence)-1)]
	var parts []string
	if r.Chat != "" {
		name, _ := m.chatName(r.Chat)
		parts = append(parts, "chat "+name)
	}
	if r.Sender != "" {
		name, _ := m.senderName(r.Sender)
		parts = append(parts, "sender "+name)
	}
	if c, ok := t.counts[silenceKey(r)]; ok {
		parts = append(parts, "matched "+strconv.FormatInt(c.matched, 10))
		if c.lastMs > 0 {
			parts = append(parts, "last match "+time.UnixMilli(c.lastMs).Format("2006-01-02 15:04"))
		}
	}
	parts = append(parts, "next start")
	return fit(stDim.Render(truncate(strings.Join(parts, " · "), w)), w)
}

func (m Model) silenceHint() string {
	f := m.config.silence.form
	switch {
	case f.pick.open:
		return "enter choose · esc back"
	case f.open && f.field == fieldContains:
		return "enter save · tab field · esc cancel"
	case f.open:
		return "enter pick · backspace clear · tab field · esc cancel"
	}
	return "a add · enter edit · d delete · tab General · esc close"
}

// silenceCursor puts the caret in the form's live input, counted from the
// screen as configCursor does: the box's border, its head row and the blank
// under it come before the first body line.
func (m Model) silenceCursor() *tea.Cursor {
	f := m.config.silence.form
	var c *tea.Cursor
	switch {
	case !f.open:
		return nil
	case f.pick.open:
		c = textinputCursor(f.pick.input)
		if c == nil {
			return nil
		}
		c.X += configLeft + lipgloss.Width(m.silencePickPrompt())
		c.Y = 3 + silenceFormHead
	case f.field == fieldContains:
		_, at := m.inputCell(f.contains, m.silenceValueWidth())
		c = tea.NewCursor(configLeft+len("  ")+silenceLabelWidth+configGap+at, 3+2+int(fieldContains))
	default:
		return nil
	}
	c.Shape, c.Blink = tea.CursorBar, true
	return c
}
