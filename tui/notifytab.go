package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/fuzzy"
)

// notifyTab is the Notifications page of :config: the people and chats whose
// every message raises a banner, then the patterns that do. The Lark client's
// own Notifications settings choose which messages notify at all; this one
// only adds to what triage already treats as urgent, so the page is larkim's
// own beside the client's name for it.
//
// It edits m.cfg.Notifications and the file together, and hands the result to
// the banner side when it runs in this process; a daemon rereads the file.
type notifyTab struct {
	idx, top      int
	confirmDelete bool
	// pick adds a watch entry; keyword, focused, adds a pattern. At most one
	// is live.
	pick    silencePick
	keyword textinput.Model
	// err is an entry the configuration refused, kept beside what was typed.
	err string
}

// notifyEntry is one row of the page.
type notifyEntry struct {
	keyword bool
	value   string
}

func (m Model) notifyEntries() []notifyEntry {
	n := m.cfg.Notifications
	out := make([]notifyEntry, 0, len(n.Watch)+len(n.Keywords))
	for _, w := range n.Watch {
		out = append(out, notifyEntry{value: w})
	}
	for _, k := range n.Keywords {
		out = append(out, notifyEntry{keyword: true, value: k})
	}
	return out
}

func (m *Model) notifyMove(d int) {
	t := &m.config.notify
	t.confirmDelete = false
	moveCursor(&t.idx, &t.top, d, len(m.notifyEntries()), m.configRows())
}

// onNotifyKey drives the page. The picker and the pattern input own their keys
// whole; the list reads bare letters.
func (m Model) onNotifyKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := &m.config.notify
	if t.pick.open {
		return m.onNotifyPickKey(k)
	}
	s := k.String()
	if t.keyword.Focused() {
		switch s {
		case "esc":
			t.keyword.Blur()
			t.err = ""
			return m, nil
		case "enter":
			return m.addNotify(notifyEntry{keyword: true, value: strings.TrimSpace(t.keyword.Value())}), nil
		}
		var cmd tea.Cmd
		t.keyword, cmd = t.keyword.Update(k)
		return m, cmd
	}
	if t.confirmDelete {
		t.confirmDelete = false
		if s == "y" {
			return m.deleteNotify(), nil
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
		t.err = ""
		t.pick = silencePick{open: true, input: m.newQueryInput()}
		t.pick.hits = m.watchSearch("")
		cmd := t.pick.input.Focus()
		return m, cmd
	case "A":
		t.err = ""
		t.keyword = m.newQueryInput()
		t.keyword.SetWidth(m.notifyValueWidth())
		cmd := t.keyword.Focus()
		return m, cmd
	case "d":
		t.confirmDelete = t.idx < len(m.notifyEntries())
	case "j", "down", "ctrl+n":
		m.notifyMove(1)
	case "k", "up", "ctrl+p":
		m.notifyMove(-1)
	case "g", "home":
		m.notifyMove(-len(m.notifyEntries()))
	case "G", "end":
		m.notifyMove(len(m.notifyEntries()))
	}
	return m, nil
}

func (m Model) onNotifyPickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := &m.config.notify
	p := &t.pick
	rows := m.notifyPickRows()
	switch k.String() {
	case "esc":
		t.pick, t.err = silencePick{}, ""
		return m, nil
	case "enter":
		// A typed id is for the configuration to judge.
		id := p.chosen()
		if id == "" {
			return m, nil
		}
		return m.addNotify(notifyEntry{value: id}), nil
	case "up", "ctrl+p":
		p.move(-1, rows)
		return m, nil
	case "down", "ctrl+n":
		p.move(1, rows)
		return m, nil
	}
	return m.typeIntoNotifyPick(k)
}

// typeIntoNotifyPick hands a message to the picker's query and re-runs the
// search when it came back changed.
func (m Model) typeIntoNotifyPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.config.notify.pick.typeInto(msg, m.watchSearch)
	return m, cmd
}

// forwardNotify is forwardConfig for the page.
func (m Model) forwardNotify(msg tea.Msg) (tea.Model, tea.Cmd) {
	t := &m.config.notify
	var cmd tea.Cmd
	switch {
	case t.pick.open:
		return m.typeIntoNotifyPick(msg)
	case t.keyword.Focused():
		t.keyword, cmd = t.keyword.Update(msg)
	}
	return m, cmd
}

// watchSearch narrows the chats, in the list's order, then the people in
// contacts, that a watch entry can name.
func (m Model) watchSearch(query string) []silenceHit {
	ix := fuzzy.NewIndex()
	out := m.chatHits(ix, query)
	for _, p := range m.contacts {
		if len(out) == fwdLimit {
			break
		}
		if !strings.HasPrefix(p.OpenID, "ou_") {
			continue
		}
		name := cmp.Or(p.Name, p.OpenID)
		if mark, ok := ix.Match(p.OpenID, name, query); ok {
			out = append(out, silenceHit{id: p.OpenID, name: name, mark: mark})
		}
	}
	return out
}

// addNotify appends e to its list. An entry already there is not added twice.
func (m Model) addNotify(e notifyEntry) Model {
	if e.value == "" {
		return m
	}
	next := m.cfg.Notifications
	list := &next.Watch
	if e.keyword {
		list = &next.Keywords
	}
	if !slices.Contains(*list, e.value) {
		*list = append(slices.Clone(*list), e.value)
	}
	if err := m.writeNotify(next); err != nil {
		m.config.notify.err = err.Error()
		return m
	}
	t := &m.config.notify
	t.pick, t.err = silencePick{}, ""
	t.keyword.Blur()
	at := slices.Index(m.notifyEntries(), e)
	moveCursor(&t.idx, &t.top, at-t.idx, len(m.notifyEntries()), m.configRows())
	return m
}

func (m Model) deleteNotify() Model {
	t := &m.config.notify
	entries := m.notifyEntries()
	if t.idx >= len(entries) {
		return m
	}
	e := entries[t.idx]
	next := m.cfg.Notifications
	drop := func(s config.Strings) config.Strings {
		out := slices.DeleteFunc(slices.Clone(s), func(v string) bool { return v == e.value })
		if len(out) == 0 {
			return nil
		}
		return out
	}
	if e.keyword {
		next.Keywords = drop(next.Keywords)
	} else {
		next.Watch = drop(next.Watch)
	}
	if err := m.writeNotify(next); err != nil {
		return m.notify("notifications: "+err.Error(), true)
	}
	moveCursor(&t.idx, &t.top, 0, len(m.notifyEntries()), m.configRows())
	return m
}

// writeNotify judges the section, writes it, and only then takes it into
// m.cfg, as writeSilence does.
func (m *Model) writeNotify(next config.Notifications) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if err := config.SetFileValue(m.deps.ConfigPath, "notifications", next); err != nil {
		return err
	}
	m.cfg.Notifications = next
	reach := "the daemon rereads it"
	if m.deps.SetNotifications != nil {
		m.deps.SetNotifications(next)
		reach = "takes effect now"
	}
	*m = m.notify("notifications · "+strconv.Itoa(len(next.Watch))+" watch, "+
		plural(len(next.Keywords), "keyword", "keywords")+" · "+reach, false)
	return nil
}

// notifyName resolves a watch entry for the list.
func (m Model) notifyName(id string) (string, bool) {
	if strings.HasPrefix(id, "oc_") {
		return m.chatName(id)
	}
	return m.senderName(id)
}

// notifyKindWidth is the column naming each row's kind.
const notifyKindWidth = len("keyword")

func (m Model) notifyValueWidth() int {
	return max(4, m.configWidth()-2-notifyKindWidth-configGap)
}

// notifyPickRows is how many hits the picker shows under its query line.
func (m Model) notifyPickRows() int { return max(1, m.configRows()-1) }

func (m Model) notifyLines() []string {
	t := m.config.notify
	w := m.configWidth()
	gap := strings.Repeat(" ", configGap)
	if t.pick.open {
		return t.pick.lines(m.notifyPickPrompt(), m.notifyPickRows(), w)
	}
	if t.keyword.Focused() {
		cell, _ := m.inputCell(t.keyword, m.notifyValueWidth())
		return []string{fit(cursorLead(true)+stBold.Render(fit("keyword", notifyKindWidth))+gap+cell, w)}
	}
	vw := m.notifyValueWidth()
	var out []string
	for i, e := range window(m.notifyEntries(), t.top, m.configRows()) {
		kind, cell := "watch", silenceIDCell(e.value, m.notifyName, vw)
		if e.keyword {
			kind, cell = "keyword", fit(truncate(e.value, vw), vw)
		}
		out = append(out, fit(cursorLead(t.top+i == t.idx)+stDim.Render(fit(kind, notifyKindWidth))+gap+cell, w))
	}
	return out
}

func (m Model) notifyPickPrompt() string { return stBold.Render("watch") + stAccent.Render(" › ") }

// notifyDetail is the line under the page.
func (m Model) notifyDetail() string {
	w := m.configWidth()
	t := m.config.notify
	entries := m.notifyEntries()
	switch {
	case t.err != "":
		return fit(stErr.Render(truncate(t.err, w)), w)
	case t.pick.open:
		return fit(stDim.Render(truncate("a person or a chat; every message from it raises a banner", w)), w)
	case t.keyword.Focused():
		return fit(stDim.Render(truncate("an RE2 pattern; a message whose text matches it raises a banner", w)), w)
	case t.confirmDelete:
		return fit(formatConfirmNotice("delete this entry? y/n", w), w)
	case len(entries) == 0:
		return fit(stDim.Render("nothing watched · a to watch a person or chat · A to add a keyword"), w)
	}
	e := entries[min(t.idx, len(entries)-1)]
	if e.keyword {
		return fit(stDim.Render(truncate("keyword "+e.value, w)), w)
	}
	return fit(stDim.Render(truncate("watch "+e.value, w)), w)
}

func (m Model) notifyHintBar() []KeyBinding {
	t := m.config.notify
	switch {
	case t.pick.open:
		return []KeyBinding{{Keys: "enter", Desc: "add"}, {Keys: "esc", Desc: "back"}}
	case t.keyword.Focused():
		return []KeyBinding{{Keys: "enter", Desc: "add"}, {Keys: "esc", Desc: "cancel"}}
	}
	return []KeyBinding{
		{Keys: "a", Desc: "watch"},
		{Keys: "A", Desc: "keyword"},
		{Keys: "d", Desc: "delete"},
		{Keys: "tab", Desc: nextTabName(m.config.tab)},
		{Keys: "esc", Desc: "close"},
	}
}

// notifyCursor puts the caret in the live input, the first body line under
// the box's border, its head row and the blank under it.
func (m Model) notifyCursor() *tea.Cursor {
	t := m.config.notify
	var c *tea.Cursor
	switch {
	case t.pick.open:
		c = textinputCursor(t.pick.input)
		if c == nil {
			return nil
		}
		c.X += configLeft + lipgloss.Width(m.notifyPickPrompt())
	case t.keyword.Focused():
		_, at := m.inputCell(t.keyword, m.notifyValueWidth())
		c = tea.NewCursor(configLeft+len("  ")+notifyKindWidth+configGap+at, 0)
	default:
		return nil
	}
	c.Y = 3
	c.Shape, c.Blink = tea.CursorBar, true
	return c
}
