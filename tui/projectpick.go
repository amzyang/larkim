package tui

import (
	"context"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/todoist"
)

// projectPick is the todoist.project chooser: a combobox standing in the row's
// value cell, with the account's projects listed under it. A project id is
// nothing a reader can type from memory, so the row offers the names and
// writes the id. The Lark client has no Todoist settings to follow; the shape
// is the one every other chooser here has, a query over a menu.
//
// A zero value is closed.
type projectPick struct {
	open bool
	// input is the query, typed into the row's own cell.
	input textinput.Model
	menu  menu[projectHit]
	// loading says the listing has not landed yet. A list from an earlier
	// opening is drawn meanwhile.
	loading bool
}

// projectHit is one project the chooser offers, with what its row says of it
// baked in when the list is filled.
type projectHit struct {
	p    todoist.Project
	mark []int
	// current is the project the row is set to now.
	current bool
}

var projectSpec = menuSpec[projectHit]{
	row: func(h projectHit) offer {
		icon := offerIcon{text: " "}
		if h.current {
			icon.text = "✓"
		}
		return offer{icon: icon, name: markName(h.p.Name, h.mark, lipgloss.NewStyle())}
	},
}

// todoistProjectsMsg is a listing, with the token it was asked under: one
// still in flight when the token changes is another account's.
type todoistProjectsMsg struct {
	token    string
	projects []todoist.Project
	err      error
}

// loadTodoistProjects lists the account's projects for the chooser.
func loadTodoistProjects(d Deps, token string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(begin("todoist-projects"), sendTimeout)
		defer cancel()
		ps, err := d.Todoist.Projects(ctx)
		if err != nil {
			d.log().ErrorContext(ctx, "todoist projects", "err", err)
			return todoistProjectsMsg{token: token, err: err}
		}
		return todoistProjectsMsg{token: token, projects: todoist.InboxFirst(ps)}
	}
}

// openProjectPick opens the chooser on the row. The listing is asked for every
// time, so a project made in Todoist since the last opening is there; the last
// list is drawn until it lands.
func (m Model) openProjectPick() (tea.Model, tea.Cmd) {
	if m.deps.Todoist == nil {
		m.config.err = "set todoist.token first"
		return m, nil
	}
	m.config.err = ""
	m.config.project = projectPick{open: true, input: m.newQueryInput(), loading: true}
	m.config.project.input.SetWidth(m.configValueWidth())
	m.fillProjectPick("")
	focus := m.config.project.input.Focus()
	return m, tea.Batch(focus, loadTodoistProjects(m.deps, m.cfg.Todoist.Token))
}

// onTodoistProjects lands a listing: it is kept for the row's name either way,
// and refills the chooser if one is still open. A failed listing closes the
// chooser and says why where a refused value is said.
func (m Model) onTodoistProjects(msg todoistProjectsMsg) Model {
	if msg.token != m.cfg.Todoist.Token {
		return m
	}
	if msg.err != nil {
		if m.config.project.open {
			m.config.project = projectPick{}
			m.config.err = msg.err.Error()
		}
		return m
	}
	m.todoistProjects = msg.projects
	if m.config.project.open {
		h, _ := m.config.project.menu.focused()
		m.config.project.loading = false
		m.fillProjectPick(h.p.ID)
	}
	return m
}

// fillProjectPick narrows the projects to the query. The cursor lands on keep
// where the list still holds it — the project the reader had walked to before
// a listing landed under them. Failing that it starts on the project the row
// is set to, so enter alone keeps it, and once a query is typed on the best
// answer instead.
func (m *Model) fillProjectPick(keep string) {
	p := &m.config.project
	query := strings.TrimSpace(p.input.Value())
	ix := fuzzy.NewIndex()
	var hits []projectHit
	at, kept := 0, false
	for _, pr := range m.todoistProjects {
		mark, ok := ix.Match(pr.ID, pr.Name, query)
		if !ok {
			continue
		}
		current := pr.Value() == m.cfg.Todoist.Project
		switch {
		case keep != "" && pr.ID == keep:
			at, kept = len(hits), true
		case current && query == "" && !kept:
			at = len(hits)
		}
		hits = append(hits, projectHit{p: pr, mark: mark, current: current})
	}
	p.menu = fillMenu(hits, projectSpec)
	m.projectPickScroll(at)
}

func (m Model) onProjectPickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.config.project
	switch k.String() {
	case "esc":
		m.config.project = projectPick{}
		return m, nil
	case "enter":
		h, ok := p.menu.focused()
		if !ok {
			return m, nil
		}
		m.config.project = projectPick{}
		return m.commitConfig(h.p.Value()), nil
	case "up", "ctrl+p":
		m.projectPickScroll(-1)
		return m, nil
	case "down", "ctrl+n":
		m.projectPickScroll(1)
		return m, nil
	}
	return m.typeIntoProjectPick(k)
}

// typeIntoProjectPick hands a keypress or a paste to the query and narrows the
// list when the query came back changed.
func (m Model) typeIntoProjectPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.config.project.input.Value()
	var cmd tea.Cmd
	m.config.project.input, cmd = m.config.project.input.Update(msg)
	if m.config.project.input.Value() != before {
		m.fillProjectPick("")
	}
	return m, cmd
}

// projectCell is what the todoist.project row draws: the project's name where
// a listing has named it, the id until one has, and the id marked when a
// listing came back without it — archived or deleted since it was picked.
func (m Model) projectCell(id string) string {
	if id == "" {
		return "Inbox"
	}
	i := slices.IndexFunc(m.todoistProjects, func(p todoist.Project) bool { return p.ID == id })
	switch {
	case i >= 0:
		return m.todoistProjects[i].Name
	case len(m.todoistProjects) > 0:
		return id + " · not found"
	}
	return id
}

// projectPickLines is the list drawn under the row, indented to the value
// column so it hangs off the cell it fills.
func (m Model) projectPickLines(indent, w int) []string {
	p := m.config.project
	lead := strings.Repeat(" ", indent)
	line := func(s string) string { return fit(lead+s, w) }
	switch {
	case !p.menu.open() && p.loading:
		return []string{line(stDim.Render("loading projects…"))}
	case !p.menu.open():
		return []string{line(stDim.Render("no project matches"))}
	}
	v := p.menu.view(m.projectPickRows())
	out := make([]string, len(v.rows))
	for i, o := range v.rows {
		row := m.joinSegsWidth(m.offerRow(o, v.cols, i, i == v.sel))
		if i == v.sel {
			row = paint(stChatSelBG, row)
		}
		out[i] = line(row)
	}
	return out
}

// projectPickHeight is how many lines the list under the row takes.
func (m Model) projectPickHeight() int {
	if !m.config.project.open {
		return 0
	}
	return max(1, min(len(m.config.project.menu.items), m.projectPickRows()))
}

// projectPickRows is how many projects the list shows at most: a popup menu's
// height, less on a panel too short to hold one under its row.
func (m Model) projectPickRows() int { return max(1, min(pumMaxRows, m.configRows()-1)) }

// projectPickScroll walks the list, for a key or the wheel.
func (m *Model) projectPickScroll(d int) { m.config.project.menu.move(d, m.projectPickRows()) }
