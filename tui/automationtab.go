package tui

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/store"
)

// Local callbacks have no Lark Settings counterpart; this page edits only the
// next process's configuration, never the running automation engine.
type automationTab struct {
	idx, top     int
	form         automationForm
	confirm      string
	busy         bool
	err, result  string
	checkContext context.Context
	checkCancel  context.CancelFunc
}

type automationField int

const (
	automationID automationField = iota
	automationEnabled
	automationChats
	automationSenders
	automationTypes
	automationContains
	automationExcludes
	automationExecutable
	automationArgs
	automationTimeout
	automationFields
)

var automationLabels = [...]string{"ID", "Enabled", "Chats", "Senders", "Types", "Contains", "Excludes", "Executable", "Args", "Timeout"}
var automationTypesList = []string{"text", "post", "interactive", "image", "file", "audio", "media", "sticker", "share_chat", "share_user", "merge_forward", "system"}

type automationForm struct {
	open, quota bool
	was         *automation.Rule
	field       automationField
	inputs      [automationFields]textinput.Model
	pick        silencePick
	roster      []store.Contact
}

type automationSavedMsg struct {
	rules []automation.Rule
	id    string
	err   error
}
type automationPresetMsg struct {
	executable string
	err        error
}
type automationCheckedMsg struct {
	ctx    context.Context
	result string
	err    error
}
type automationRosterMsg struct {
	chatID string
	roster []store.Contact
	err    error
}

func automationJSON(values []string) string {
	b, _ := json.Marshal(values)
	return string(b)
}

func automationArray(raw string) ([]string, error) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		return nil, fmt.Errorf("use a JSON array of strings, e.g. [\"one value\"]")
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("use a JSON array of strings: %w", err)
	}
	return values, nil
}

func sameAutomationRule(a, b automation.Rule) bool {
	return a.ID == b.ID && a.Enabled == b.Enabled && a.Callback.Executable == b.Callback.Executable && a.Callback.Timeout == b.Callback.Timeout &&
		slices.Equal(a.Callback.Args, b.Callback.Args) && slices.Equal(a.Criteria.ChatIDs, b.Criteria.ChatIDs) &&
		slices.Equal(a.Criteria.SenderIDs, b.Criteria.SenderIDs) && slices.Equal(a.Criteria.MessageTypes, b.Criteria.MessageTypes) &&
		slices.Equal(a.Criteria.Contains, b.Criteria.Contains) && slices.Equal(a.Criteria.Excludes, b.Criteria.Excludes)
}

func (m Model) openAutomationForm(was *automation.Rule, rule automation.Rule, quota bool) (tea.Model, tea.Cmd) {
	f := automationForm{open: true, was: was, quota: quota}
	values := [...]string{rule.ID, fmt.Sprint(rule.Enabled), automationJSON(rule.Criteria.ChatIDs), automationJSON(rule.Criteria.SenderIDs), automationJSON(rule.Criteria.MessageTypes), automationJSON(rule.Criteria.Contains), automationJSON(rule.Criteria.Excludes), rule.Callback.Executable, automationJSON(rule.Callback.Args), rule.Callback.Timeout.String()}
	for i := range automationFields {
		f.inputs[i] = m.newQueryInput()
		f.inputs[i].CharLimit = 0
		f.inputs[i].SetWidth(m.automationValueWidth())
		f.inputs[i].SetValue(values[i])
	}
	m.config.automation.form = f
	m.config.automation.err, m.config.automation.result = "", ""
	cmd := m.config.automation.form.inputs[automationID].Focus()
	return m, cmd
}

func (m *Model) automationMove(d int) {
	t := &m.config.automation
	t.confirm, t.err, t.result = "", "", ""
	moveCursor(&t.idx, &t.top, d, len(m.cfg.Automation), max(1, m.configRows()-4))
}

func (m Model) onAutomationKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := &m.config.automation
	if t.busy {
		if k.String() == "esc" && t.checkCancel != nil {
			t.checkCancel()
			return m.closeConfig(), nil
		}
		return m, nil
	}
	if t.confirm != "" {
		action := t.confirm
		t.confirm = ""
		if k.String() != "y" {
			return m, nil
		}
		r := m.cfg.Automation[t.idx]
		if action == "delete" {
			return m.writeAutomation(&r, nil)
		}
		return m.checkAutomation(r)
	}
	if t.form.open {
		return m.onAutomationFormKey(k)
	}
	switch k.String() {
	case "esc":
		return m.closeConfig(), nil
	case "tab":
		return m.switchConfigTab(1)
	case "shift+tab":
		return m.switchConfigTab(-1)
	case "a":
		return m.openAutomationForm(nil, automation.Rule{Callback: automation.Callback{Timeout: time.Minute}}, false)
	case "q":
		t.busy, t.err, t.result = true, "", ""
		resolve := m.deps.Executable
		if resolve == nil {
			resolve = os.Executable
		}
		return m, func() tea.Msg { path, err := resolve(); return automationPresetMsg{path, err} }
	case "enter", "i":
		if t.idx < len(m.cfg.Automation) {
			r := m.cfg.Automation[t.idx]
			_, _, quota := automation.QuotaScope(r)
			return m.openAutomationForm(new(r), r, quota)
		}
	case "space", " ":
		if t.idx < len(m.cfg.Automation) {
			was := m.cfg.Automation[t.idx]
			r := was
			r.Enabled = !r.Enabled
			return m.writeAutomation(&was, &r)
		}
	case "d":
		if t.idx < len(m.cfg.Automation) {
			t.confirm = "delete"
		}
	case "c":
		t.err, t.result = "", ""
		if t.idx < len(m.cfg.Automation) {
			if _, _, ok := automation.QuotaScope(m.cfg.Automation[t.idx]); ok && m.deps.CheckQuota != nil {
				t.confirm = "check"
			} else {
				t.err = "Check is available only for the quota preset"
			}
		}
	case "j", "down", "ctrl+n":
		m.automationMove(1)
	case "k", "up", "ctrl+p":
		m.automationMove(-1)
	case "g", "home":
		m.automationMove(-len(m.cfg.Automation))
	case "G", "end":
		m.automationMove(len(m.cfg.Automation))
	case "pgdown", "ctrl+d":
		m.automationMove(m.configRows() / 2)
	case "pgup", "ctrl+u":
		m.automationMove(-m.configRows() / 2)
	}
	return m, nil
}

func (m Model) onAutomationFormKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.config.automation.form
	if f.pick.open {
		return m.onAutomationPickKey(k)
	}
	switch k.String() {
	case "esc":
		m.config.automation.form = automationForm{}
		m.config.automation.err = ""
		return m, nil
	case "ctrl+s":
		return m.saveAutomationForm()
	case "tab", "down", "ctrl+n":
		return m.focusAutomationField((f.field + 1) % automationFields)
	case "shift+tab", "up", "ctrl+p":
		return m.focusAutomationField((f.field + automationFields - 1) % automationFields)
	case "enter":
		switch f.field {
		case automationChats, automationSenders, automationTypes:
			return m.openAutomationPick()
		case automationEnabled:
			f.inputs[automationEnabled].SetValue(fmt.Sprint(f.inputs[automationEnabled].Value() != "true"))
			return m, nil
		case automationTimeout:
			return m.saveAutomationForm()
		default:
			return m.focusAutomationField((f.field + 1) % automationFields)
		}
	case "space", " ":
		if f.field == automationEnabled {
			f.inputs[automationEnabled].SetValue(fmt.Sprint(f.inputs[automationEnabled].Value() != "true"))
			return m, nil
		}
	}
	return m.typeIntoAutomation(k)
}

func (m Model) focusAutomationField(field automationField) (tea.Model, tea.Cmd) {
	f := &m.config.automation.form
	f.inputs[f.field].Blur()
	f.field = field
	cmd := f.inputs[field].Focus()
	return m, cmd
}

func (f automationForm) editable() bool {
	return f.open && f.field != automationEnabled && !(f.field == automationID && f.was != nil) && !(f.field == automationArgs && f.quota)
}

func (f *automationForm) syncQuotaArgs() {
	if !f.quota {
		return
	}
	chats, chatErr := automationArray(f.inputs[automationChats].Value())
	senders, senderErr := automationArray(f.inputs[automationSenders].Value())
	if chatErr != nil || senderErr != nil || len(chats) != 1 || len(senders) != 1 {
		return
	}
	rule := automation.QuotaRule("", "", chats[0], senders[0])
	f.inputs[automationArgs].SetValue(automationJSON(rule.Callback.Args))
}

func (m Model) typeIntoAutomation(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.config.automation.form
	if f.pick.open {
		cmd := f.pick.typeInto(msg, m.automationSearch)
		return m, cmd
	}
	if !f.editable() {
		return m, nil
	}
	var cmd tea.Cmd
	f.inputs[f.field], cmd = f.inputs[f.field].Update(msg)
	if f.field == automationChats || f.field == automationSenders {
		f.syncQuotaArgs()
	}
	return m, cmd
}

func (m Model) forwardAutomation(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.config.automation.busy || m.config.automation.confirm != "" {
		return m, nil
	}
	return m.typeIntoAutomation(msg)
}

func (m Model) automationSearch(query string) []silenceHit {
	f := m.config.automation.form
	ix := fuzzy.NewIndex()
	switch f.field {
	case automationChats:
		return m.chatHits(ix, query)
	case automationSenders:
		return m.senderHits(ix, query, f.roster)
	}
	var out []silenceHit
	for _, kind := range automationTypesList {
		if mark, ok := ix.Match(kind, kind, query); ok {
			out = append(out, silenceHit{id: kind, name: kind, mark: mark})
		}
	}
	return out
}

func (m *Model) refreshAutomationPick() {
	f := &m.config.automation.form
	if !f.pick.open {
		return
	}
	f.pick.hits = m.automationSearch(f.pick.input.Value())
	f.pick.move(0, m.configRows()-1)
}

func (m Model) openAutomationPick() (tea.Model, tea.Cmd) {
	f := &m.config.automation.form
	f.pick = silencePick{open: true, input: m.newQueryInput()}
	f.pick.input.SetWidth(max(4, m.configWidth()-20))
	f.pick.hits = m.automationSearch("")
	focus := f.pick.input.Focus()
	chats, _ := automationArray(f.inputs[automationChats].Value())
	if f.field != automationSenders || len(chats) != 1 || m.deps.Store == nil {
		return m, focus
	}
	d, chatID := m.deps, chats[0]
	return m, tea.Batch(focus, func() tea.Msg {
		roster, err := d.Store.ChatRoster(context.Background(), chatID, d.Self)
		return automationRosterMsg{chatID, roster, err}
	})
}

func (m Model) onAutomationPickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.config.automation.form
	p := &f.pick
	switch k.String() {
	case "esc":
		f.pick = silencePick{}
		return m, nil
	case "enter":
		id := p.chosen()
		if id == "" {
			return m, nil
		}
		if f.field == automationTypes && !slices.Contains(automationTypesList, id) {
			m.config.automation.err = "Choose a message type from the list"
			return m, nil
		}
		values, err := automationArray(f.inputs[f.field].Value())
		if err != nil {
			m.config.automation.err = err.Error()
			return m, nil
		}
		if f.quota && (f.field == automationChats || f.field == automationSenders) {
			values = []string{id}
		} else if !slices.Contains(values, id) {
			values = append(values, id)
		}
		f.inputs[f.field].SetValue(automationJSON(values))
		f.syncQuotaArgs()
		f.pick = silencePick{}
		if f.field == automationChats {
			f.roster = nil
		}
		m.config.automation.err = ""
		return m, nil
	case "up", "ctrl+p":
		p.move(-1, m.configRows()-1)
		return m, nil
	case "down", "ctrl+n":
		p.move(1, m.configRows()-1)
		return m, nil
	}
	return m.typeIntoAutomation(k)
}

func (f automationForm) rule() (automation.Rule, error) {
	r := automation.Rule{ID: strings.TrimSpace(f.inputs[automationID].Value()), Enabled: f.inputs[automationEnabled].Value() == "true"}
	if f.was != nil {
		r.ID = f.was.ID
	}
	for _, entry := range []struct {
		field automationField
		dst   *[]string
	}{
		{automationChats, &r.Criteria.ChatIDs}, {automationSenders, &r.Criteria.SenderIDs}, {automationTypes, &r.Criteria.MessageTypes},
		{automationContains, &r.Criteria.Contains}, {automationExcludes, &r.Criteria.Excludes}, {automationArgs, &r.Callback.Args},
	} {
		values, err := automationArray(f.inputs[entry.field].Value())
		if err != nil {
			return r, fmt.Errorf("%s: %w", automationLabels[entry.field], err)
		}
		*entry.dst = values
	}
	r.Callback.Executable = strings.TrimSpace(f.inputs[automationExecutable].Value())
	timeout, err := time.ParseDuration(strings.TrimSpace(f.inputs[automationTimeout].Value()))
	if err != nil {
		return r, fmt.Errorf("Timeout: %w", err)
	}
	r.Callback.Timeout = timeout
	if f.quota {
		if len(r.Criteria.ChatIDs) != 1 || len(r.Criteria.SenderIDs) != 1 {
			return r, fmt.Errorf("Quota requires exactly one chat and one sender")
		}
		r.Callback.Args = automation.QuotaRule(r.ID, r.Callback.Executable, r.Criteria.ChatIDs[0], r.Criteria.SenderIDs[0]).Callback.Args
	}
	return r, automation.ValidateRules([]automation.Rule{r})
}

func (m Model) saveAutomationForm() (tea.Model, tea.Cmd) {
	r, err := m.config.automation.form.rule()
	if err != nil {
		m.config.automation.err = err.Error()
		return m, nil
	}
	return m.writeAutomation(m.config.automation.form.was, &r)
}

// Apply the edit by ID to the latest file, refusing a concurrent edit of the
// same rule instead of overwriting it. SetFileValue preserves unrelated YAML keys.
func (m Model) writeAutomation(was, next *automation.Rule) (tea.Model, tea.Cmd) {
	m.config.automation.busy, m.config.automation.err, m.config.automation.result = true, "", ""
	path := m.deps.ConfigPath
	return m, func() tea.Msg {
		cfg, err := config.Load(path)
		if err != nil {
			return automationSavedMsg{err: err}
		}
		rules := slices.Clone(cfg.Automation)
		id := ""
		if was != nil {
			at := slices.IndexFunc(rules, func(r automation.Rule) bool { return r.ID == was.ID })
			if at < 0 || !sameAutomationRule(rules[at], *was) {
				return automationSavedMsg{err: fmt.Errorf("Rule changed in the file; reopen configuration before editing")}
			}
			if next == nil {
				rules = slices.Delete(rules, at, at+1)
			} else {
				rules[at] = *next
				id = next.ID
			}
		} else {
			rules = append(rules, *next)
			id = next.ID
		}
		cfg.Automation = rules
		cfg.Normalize()
		if err := cfg.Validate(); err != nil {
			return automationSavedMsg{err: err}
		}
		if err := config.SetFileValue(path, "automation", cfg.Automation); err != nil {
			return automationSavedMsg{err: err}
		}
		return automationSavedMsg{rules: cfg.Automation, id: id}
	}
}

func (m Model) onAutomationSaved(msg automationSavedMsg) Model {
	t := &m.config.automation
	t.busy = false
	if msg.err != nil {
		t.err = msg.err.Error()
		return m
	}
	m.cfg.Automation = msg.rules
	m.automationRestart = true
	t.form, t.err = automationForm{}, ""
	if at := slices.IndexFunc(msg.rules, func(r automation.Rule) bool { return r.ID == msg.id }); at >= 0 {
		t.idx = at
	}
	moveCursor(&t.idx, &t.top, 0, len(msg.rules), max(1, m.configRows()-4))
	return m.notify("Automation saved · Restart required", false)
}

func (m Model) onAutomationPreset(msg automationPresetMsg) (tea.Model, tea.Cmd) {
	m.config.automation.busy = false
	if msg.err != nil {
		m.config.automation.err = msg.err.Error()
		return m, nil
	}
	id := "quota"
	for n := 2; slices.ContainsFunc(m.cfg.Automation, func(r automation.Rule) bool { return r.ID == id }); n++ {
		id = fmt.Sprintf("quota-%d", n)
	}
	r := automation.QuotaRule(id, msg.executable, "", "")
	r.Criteria.ChatIDs, r.Criteria.SenderIDs = nil, nil
	return m.openAutomationForm(nil, r, true)
}

func (m Model) checkAutomation(rule automation.Rule) (tea.Model, tea.Cmd) {
	m.config.automation.busy, m.config.automation.err, m.config.automation.result = true, "", ""
	check, resolve := m.deps.CheckQuota, m.deps.Executable
	if resolve == nil {
		resolve = os.Executable
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	m.config.automation.checkContext, m.config.automation.checkCancel = ctx, cancel
	return m, func() tea.Msg {
		defer cancel()
		executable, err := resolve()
		if err != nil {
			return automationCheckedMsg{ctx: ctx, err: err}
		}
		if rule.Callback.Executable != executable {
			return automationCheckedMsg{ctx: ctx, err: fmt.Errorf("Check requires the current larkim executable")}
		}
		result, err := check(ctx, rule)
		return automationCheckedMsg{ctx: ctx, result: result, err: err}
	}
}

func (m Model) onAutomationChecked(msg automationCheckedMsg) Model {
	if msg.ctx != m.config.automation.checkContext {
		return m
	}
	m.config.automation.checkContext, m.config.automation.checkCancel = nil, nil
	m.config.automation.busy = false
	if msg.err != nil {
		m.config.automation.err = msg.err.Error()
	} else {
		m.config.automation.result = msg.result
	}
	return m
}

func (m Model) onAutomationRoster(msg automationRosterMsg) Model {
	f := &m.config.automation.form
	chats, _ := automationArray(f.inputs[automationChats].Value())
	if !f.open || len(chats) != 1 || chats[0] != msg.chatID {
		return m
	}
	if msg.err != nil {
		m.config.automation.err = msg.err.Error()
		return m
	}
	f.roster = msg.roster
	m.refreshAutomationPick()
	return m
}

const automationLabelWidth = len("Executable")

func (m Model) automationValueWidth() int { return max(4, m.configWidth()-automationLabelWidth-4) }
func (m Model) automationFormTop() int {
	return max(0, int(m.config.automation.form.field)-m.configRows()+3)
}
func (m Model) automationPickPrompt() string {
	return stBold.Render(automationLabels[m.config.automation.form.field]) + stAccent.Render(" › ")
}

func (m Model) automationLines() []string {
	t, w := m.config.automation, m.configWidth()
	if t.form.pick.open {
		return t.form.pick.lines(m.automationPickPrompt(), m.configRows()-1, w)
	}
	if t.confirm == "check" {
		return []string{fit(stBold.Render("Check quota callback?"), w), fit("Lingxi will come to the foreground.", w), fit("Read-only check · no click · timeout 60s", w)}
	}
	if t.form.open {
		f := t.form
		title := "New rule · disabled by default"
		if f.was != nil {
			title = "Edit rule · ID is immutable"
		}
		if f.quota {
			title += " · quota"
		}
		out := []string{fit(truncate(stBold.Render(title), w), w)}
		for field := automationField(m.automationFormTop()); field < automationFields && len(out) < m.configRows(); field++ {
			cell := truncate(f.inputs[field].Value(), m.automationValueWidth())
			if field == f.field {
				cell, _ = m.inputCell(f.inputs[field], m.automationValueWidth())
			}
			out = append(out, fit(cursorLead(field == f.field)+fit(automationLabels[field], automationLabelWidth)+"  "+cell, w))
		}
		return out
	}
	var out []string
	for i, r := range window(m.cfg.Automation, t.top, max(1, m.configRows()-4)) {
		state := "Disabled"
		if r.Enabled {
			state = "Enabled"
		}
		out = append(out, fit(padBetween(cursorLead(t.top+i == t.idx)+truncate(r.ID, max(4, w-26)), stDim.Render("Configured · "+state), w), w))
	}
	if len(out) == 0 {
		out = append(out, fit("No automation rules · a new · q quota preset", w))
	}
	if t.idx < len(m.cfg.Automation) {
		r := m.cfg.Automation[t.idx]
		chats, senders := strings.Join(r.Criteria.ChatIDs, ", "), strings.Join(r.Criteria.SenderIDs, ", ")
		if chats == "" {
			chats = "Any chat"
		}
		if senders == "" {
			senders = "Any sender"
		}
		out = append(out, "", fit(truncate("Chats: "+chats, w), w), fit(truncate("Senders: "+senders, w), w), fit(truncate("Callback: "+r.Callback.Executable, w), w))
	}
	if t.result != "" {
		out = append(out, "")
		out = append(out, wrap(flatten(t.result), w)...)
	}
	return out[:min(len(out), m.configRows())]
}

func (m Model) automationDetail() string {
	t, w := m.config.automation, m.configWidth()
	text := "Configured rules · take effect after restart"
	switch {
	case t.err != "":
		return fit(stErr.Render(truncate(t.err, w)), w)
	case t.busy:
		text = "Working…"
	case t.confirm == "delete":
		return fit(formatConfirmNotice("Delete this rule? y/n", w), w)
	case t.confirm == "check":
		return fit(formatConfirmNotice("Continue with check? y/n", w), w)
	case t.form.open:
		text = "Lists: JSON arrays · [] matches any"
		if t.form.field == automationContains {
			text = "JSON strings · every case-sensitive term must match"
		}
		if t.form.field == automationExcludes {
			text = "JSON strings · no term may match"
		}
		if t.form.field == automationArgs {
			text = "JSON arguments · each string is one argument"
			if t.form.quota {
				text = "Derived from Chats and Senders"
			}
		}
		if t.form.field == automationTimeout {
			text = "Positive duration, e.g. 60s"
		}
	case m.automationRestart:
		text = "Restart required · the TUI that owns sync"
	}
	return fit(stDim.Render(truncate(text, w)), w)
}

func (m Model) automationHintBar() []KeyBinding {
	t := m.config.automation
	if t.confirm != "" {
		return []KeyBinding{{Keys: "y", Desc: "confirm"}, {Keys: "n/esc", Desc: "cancel"}}
	}
	if t.busy {
		if t.checkCancel != nil {
			return []KeyBinding{{Keys: "esc", Desc: "cancel check"}}
		}
		return nil
	}
	if t.form.pick.open {
		return []KeyBinding{{Keys: "enter", Desc: "choose"}, {Keys: "esc", Desc: "back"}}
	}
	if t.form.open {
		return []KeyBinding{{Keys: "ctrl+s", Desc: "save"}, {Keys: "enter", Desc: "pick/next"}, {Keys: "tab", Desc: "field"}, {Keys: "esc", Desc: "cancel"}}
	}
	keys := []KeyBinding{{Keys: "a", Desc: "new"}, {Keys: "q", Desc: "quota"}, {Keys: "enter", Desc: "edit"}, {Keys: "space", Desc: "toggle"}, {Keys: "d", Desc: "delete"}}
	if t.idx < len(m.cfg.Automation) && m.deps.CheckQuota != nil {
		if _, _, ok := automation.QuotaScope(m.cfg.Automation[t.idx]); ok {
			keys = append(keys, KeyBinding{Keys: "c", Desc: "check"})
		}
	}
	return append(keys, KeyBinding{Keys: "tab", Desc: nextTabName(m.config.tab)}, KeyBinding{Keys: "esc", Desc: "close"})
}

func (m Model) automationCursor() *tea.Cursor {
	f := m.config.automation.form
	if !f.open || m.config.automation.busy {
		return nil
	}
	var c *tea.Cursor
	if f.pick.open {
		c = textinputCursor(f.pick.input)
		if c == nil {
			return nil
		}
		c.X += configLeft + len(automationLabels[f.field]) + 3
		c.Y = 3
	} else {
		if !f.editable() {
			return nil
		}
		_, at := m.inputCell(f.inputs[f.field], m.automationValueWidth())
		c = tea.NewCursor(configLeft+automationLabelWidth+4+at, 4+int(f.field)-m.automationFormTop())
	}
	c.Shape, c.Blink = tea.CursorBar, true
	return c
}
