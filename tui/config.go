package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/fuzzy"
)

// configHit is one key the filter kept, with the runes of its name the query
// landed on.
type configHit struct {
	s    setting
	mark []int
}

// configPanel is the :config overlay. A zero value is closed.
//
// It edits the configuration file: a value committed here is written before it
// is applied, so what the panel shows a reader is what the next run reads. The
// value it shows is the session's own, which is where a :set of the same key
// is visible — committing that row is how the two are reconciled.
type configPanel struct {
	open bool
	// input is the filter, armed by /. It is the text input every other
	// chooser is built on, so the query takes the same readline keys.
	input     textinput.Model
	filtering bool
	hits      []configHit
	idx, top  int
	// editor holds the value being typed into the focused row; editing is
	// what tells the two inputs apart, since only one of them is ever live.
	editing bool
	editor  textinput.Model
	// err is a value the row refused, kept on screen beside the typed text
	// rather than thrown away with it.
	err string
}

// openConfig opens the panel, on the row key names when it names one.
func (m Model) openConfig(key string) Model {
	in := m.newQueryInput()
	hits := configSearch("")
	at := 0
	if key = strings.TrimSpace(key); key != "" {
		at = slices.IndexFunc(hits, func(h configHit) bool { return h.s.key == key })
		if at < 0 {
			return m.notify("unknown key "+key, true)
		}
	}
	m.config = configPanel{open: true, input: in, hits: hits}
	moveCursor(&m.config.idx, &m.config.top, at, len(hits), m.configRows())
	return m
}

func (m Model) closeConfig() Model {
	m.config = configPanel{}
	return m
}

// configSearch narrows the registry. The prose is matched alongside the key,
// so "assistant" reaches the ai section and "attachment" the resource cap, but
// only the name is ever underlined because it is the only column a hit is
// drawn in.
func configSearch(query string) []configHit {
	ix := fuzzy.NewIndex()
	q := strings.ToLower(strings.TrimSpace(query))
	var near, far []configHit
	for i, s := range settings {
		id := strconv.Itoa(i)
		km, keyOK := ix.Match(id+"k", s.key, query)
		_, helpOK := ix.Match(id+"h", s.help, query)
		if !keyOK && !helpOK {
			continue
		}
		h := configHit{s: s, mark: km}
		// A row the query is literally in is the one the reader meant. fzf
		// spells "attach" out of "active_top_k ... chats" as well, and that
		// answer belongs under the real one rather than over it.
		if q != "" && !strings.Contains(strings.ToLower(s.key+" "+s.help), q) {
			far = append(far, h)
			continue
		}
		near = append(near, h)
	}
	return append(near, far...)
}

// configWidth is the panel's own width, the screen less the overlay's margin,
// border and padding — the help panel's, so the two overlays sit alike.
func (m Model) configWidth() int { return max(20, m.width-8) }

// configRows is how many keys fit: the screen less the border, the head line
// and the blank under it, and the blank, detail and hint lines at the foot.
func (m Model) configRows() int { return max(1, m.height-7) }

func (m *Model) configMove(d int) {
	// A refused value belongs to the row it was typed into, so leaving that
	// row takes the message with it.
	m.config.err = ""
	moveCursor(&m.config.idx, &m.config.top, d, len(m.config.hits), m.configRows())
}

// configFocus is the row the cursor rests on.
func (m Model) configFocus() (setting, bool) {
	if m.config.idx >= len(m.config.hits) {
		return setting{}, false
	}
	return m.config.hits[m.config.idx].s, true
}

// onConfigKey drives the panel. Its three states own their keys whole: the
// editor takes everything it can edit with, the filter likewise, and only the
// browsing state reads bare letters as commands.
func (m Model) onConfigKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if m.config.editing {
		switch s {
		case "esc":
			m.config.editing, m.config.err = false, ""
			m.config.editor.Blur()
			return m, nil
		case "enter":
			return m.commitConfig(m.config.editor.Value()), nil
		}
		var cmd tea.Cmd
		m.config.editor, cmd = m.config.editor.Update(k)
		return m, cmd
	}
	if m.config.filtering {
		switch s {
		case "esc":
			// Esc backs out one step at a time, the way it does out of the
			// help panel: the query first, the panel only once it is empty.
			if m.config.input.Value() == "" {
				return m.closeConfig(), nil
			}
			m.config.input.SetValue("")
			m.config.filtering = false
			m.config.input.Blur()
			m.config.hits, m.config.idx, m.config.top = configSearch(""), 0, 0
			return m, nil
		case "enter":
			m.config.filtering = false
			m.config.input.Blur()
			return m, nil
		case "up", "ctrl+p":
			m.configMove(-1)
			return m, nil
		case "down", "ctrl+n":
			m.configMove(1)
			return m, nil
		}
		before := m.config.input.Value()
		var cmd tea.Cmd
		m.config.input, cmd = m.config.input.Update(k)
		if q := m.config.input.Value(); q != before {
			m.config.hits, m.config.idx, m.config.top = configSearch(q), 0, 0
		}
		return m, cmd
	}
	switch s {
	case "esc", "q":
		return m.closeConfig(), nil
	case "/":
		m.config.filtering = true
		cmd := m.config.input.Focus()
		return m, cmd
	case "enter", "i":
		return m.editConfig()
	case "&":
		return m.resetConfig(), nil
	case "j", "down", "ctrl+n":
		m.configMove(1)
	case "k", "up", "ctrl+p":
		m.configMove(-1)
	case "ctrl+d", "pgdown":
		m.configMove(m.configRows() / 2)
	case "ctrl+u", "pgup":
		m.configMove(-m.configRows() / 2)
	case "g", "home":
		m.configMove(-len(m.config.hits))
	case "G", "end":
		m.configMove(len(m.config.hits))
	}
	// Unlike the help panel, an unhandled key leaves the panel standing: this
	// one is worked in rather than read, and closing under a stray keystroke
	// would throw away where the reader had scrolled to.
	return m, nil
}

// editConfig arms the row's editor with the value already there, so a small
// change is an edit rather than a retyping.
func (m Model) editConfig() (tea.Model, tea.Cmd) {
	s, ok := m.configFocus()
	if !ok {
		return m, nil
	}
	if s.readOnly {
		return m.notify(s.key+": a list; edit the config file", true), nil
	}
	in := m.newQueryInput()
	in.SetValue(m.settingValue(s))
	in.SetWidth(m.configValueWidth())
	m.config.editing, m.config.editor, m.config.err = true, in, ""
	cmd := m.config.editor.Focus()
	return m, cmd
}

// configEditorCell draws the value being typed, shaded across the whole
// column the way a selected row is, and returns how far into that column the
// caret sits. The input's own View is not drawn: it ends in a reset and in a
// cursor cell of its own, and both leave unshaded seams in the field.
//
// The text is windowed on the caret, so a value longer than the column is
// typed at rather than scrolled past.
func (m Model) configEditorCell(valw int) (string, int) {
	v := []rune(m.config.editor.Value())
	pos := clamp(m.config.editor.Position(), 0, len(v))
	// One column past the last rune is where the caret rests after typing.
	start := max(0, pos-valw+1)
	end := min(len(v), start+valw)
	return m.th.sel.Render(fit(string(v[start:end]), valw)), lipgloss.Width(string(v[start:pos]))
}

func (m Model) resetConfig() Model {
	s, ok := m.configFocus()
	if !ok {
		return m
	}
	v, _ := config.Default().Get(s.key)
	return m.commitConfig(v)
}

// commitConfig judges the value, writes it, and only then lets it into the
// session: a value the file refused is one this run must not be running
// either, or the panel would show a setting the next start does not have.
func (m Model) commitConfig(value string) Model {
	s, ok := m.configFocus()
	if !ok {
		return m
	}
	value = strings.TrimSpace(value)
	next, err := nextValue(m.cfg, s, value)
	if err != nil {
		m.config.err = err.Error()
		return m
	}
	if err := config.SetFile(m.deps.ConfigPath, s.key, value); err != nil {
		m.config.err = err.Error()
		return m
	}
	m.cfg = next
	if s.apply != nil {
		s.apply(&m)
	}
	m.config.editing, m.config.err = false, ""
	m.config.editor.Blur()
	note := s.key + "=" + m.settingValue(s)
	if !s.live {
		note += " · next start"
	}
	return m.notify(note, false)
}

// configPrompt labels the query box. The cursor is placed past it, so its
// width is not measured in two places.
func configPrompt() string { return stBold.Render("config") + stAccent.Render(" › ") }

// configKeyWidth is the column the keys are laid out in, measured over the
// whole registry rather than the rows the filter kept, so the values do not
// slide sideways as a query is typed.
func configKeyWidth() int {
	w := 0
	for _, s := range settings {
		w = max(w, lipgloss.Width(s.key))
	}
	return w
}

// configLeft is the column the panel's content starts in: the overlay is
// centred in a screen four columns wider than its box, and then costs a border
// and a pad.
const configLeft = 4

// configGap is the space between the key column and the value one.
const configGap = 2

// configValueWidth is the value column, which is also the width the editor
// opens at: it stands in the cell the value was drawn in, not beside it.
func (m Model) configValueWidth() int {
	return max(4, m.configWidth()-configKeyWidth()-2-configGap)
}

// configLines is the list of keys laid out for the current width. A value
// still at its default is dimmed, so what this machine has actually changed is
// what stands out; the one being edited is shaded whole, the way a selected
// row is, so the cell reads as a field rather than as text.
func (m Model) configLines() []string {
	gap := strings.Repeat(" ", configGap)
	w, keyw, valw := m.configWidth(), configKeyWidth(), m.configValueWidth()
	def := config.Default()
	var out []string
	for i, h := range window(m.config.hits, m.config.top, m.configRows()) {
		row := m.config.top + i
		lead := "  "
		key := markName(h.s.key, h.mark, stBold)
		if row == m.config.idx {
			lead = stAccent.Render("❯ ")
		}
		var cell string
		switch {
		case row == m.config.idx && m.config.editing:
			cell, _ = m.configEditorCell(valw)
		default:
			value := m.settingValue(h.s)
			style := lipgloss.NewStyle()
			if d, _ := def.Get(h.s.key); d == value {
				style = stDim
			}
			cell = style.Render(truncate(value, valw))
		}
		out = append(out, fit(lead+fit(key, keyw)+gap+cell, w))
	}
	return out
}

// configDetail is the line under the list: what the focused key is for, and
// how far a change to it reaches.
func (m Model) configDetail() string {
	w := m.configWidth()
	if m.config.err != "" {
		return fit(stErr.Render(truncate(m.config.err, w)), w)
	}
	s, ok := m.configFocus()
	if !ok {
		return fit(stDim.Render("nothing matches "+m.config.input.Value()), w)
	}
	reach := "next start"
	switch {
	case s.readOnly:
		reach = "file only"
	case s.live:
		reach = "takes effect now"
	}
	return fit(stDim.Render(truncate(s.help+" · "+reach, w)), w)
}

func (m Model) renderConfig() string {
	w, rows := m.configWidth(), m.configRows()
	title := stHelpSection.Render("config")
	where := strconv.Itoa(min(m.config.idx+1, len(m.config.hits))) + "/" + strconv.Itoa(len(m.config.hits))
	path := shortPath(underHome(m.deps.ConfigPath), max(8, w-lipgloss.Width(title)-len(where)-4))
	head := padBetween(title, stDim.Render(where+" · "+path), w)
	if m.config.filtering || strings.TrimSpace(m.config.input.Value()) != "" {
		head = padBetween(configPrompt()+m.config.input.View(),
			stDim.Render(strconv.Itoa(len(m.config.hits))+"/"+strconv.Itoa(len(settings))), w)
	}
	body := m.configLines()
	for len(body) < rows {
		body = append(body, fit("", w))
	}
	hint := "enter edit · & default · / filter · esc close"
	if m.config.editing {
		hint = "enter save · esc cancel"
	}
	lines := append([]string{head, fit("", w)}, body...)
	lines = append(lines, fit("", w), m.configDetail(), fit(stDim.Render(hint), w))
	return paneStyle(true).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

// underHome spells a path the way a reader writes it, so the file the panel
// names is the one they would open.
func underHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// shortPath keeps a path's tail, which is the part that names the file, for a
// head row too narrow to hold the whole of it.
func shortPath(path string, w int) string {
	r := []rune(path)
	if len(r) <= w {
		return path
	}
	if w <= 1 {
		return "…"
	}
	return "…" + string(r[len(r)-w+1:])
}

// configCursor puts the caret in whichever of the panel's two inputs is live.
// The overlay is drawn as one block rather than into the pane grid, so its
// coordinates are counted from the screen rather than taken from the layout.
func (m Model) configCursor() *tea.Cursor {
	switch {
	case m.config.editing:
		_, at := m.configEditorCell(m.configValueWidth())
		// The head row and the blank under it, under the box's own top border.
		c := tea.NewCursor(configLeft+len("  ")+configKeyWidth()+configGap+at,
			3+m.config.idx-m.config.top)
		c.Shape, c.Blink = tea.CursorBar, true
		return c
	case m.config.filtering:
		c := textinputCursor(m.config.input)
		if c == nil {
			return nil
		}
		c.X += configLeft + lipgloss.Width(configPrompt())
		c.Y++
		c.Shape, c.Blink = tea.CursorBar, true
		return c
	}
	return nil
}
