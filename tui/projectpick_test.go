package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/todoist"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// fakeAccount is the projects the fake answers with, in the order Todoist
// would: Inbox is not first, which is what the chooser puts right.
var fakeAccount = []todoist.Project{
	{ID: "p_work", Name: "平台组"},
	{ID: "p_inbox", Name: "Inbox", Inbox: true},
	{ID: "p_home", Name: "Home"},
}

// projectModel is the config panel on todoist.project with a token set and
// f standing in for the account.
func projectModel(t *testing.T, f *fakeTasks) Model {
	t.Helper()
	m := configModel(t)
	m.cfg.Todoist.Token = "tok_a"
	m.deps.Todoist = f
	return m.openConfig("todoist.project")
}

// openProjects presses enter on the row and lands the listing the press asked
// for, the way the runtime would once the request returned.
func openProjects(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, m, "enter")
	require.True(t, m.config.project.open)
	next, _ := m.Update(loadTodoistProjects(m.deps, m.cfg.Todoist.Token)())
	return next.(Model)
}

// projectOffers is the names the chooser lists, in order.
func projectOffers(m Model) []string {
	var out []string
	for _, h := range m.config.project.menu.items {
		out = append(out, h.p.Name)
	}
	return out
}

func focusedProject(t *testing.T, m Model) string {
	t.Helper()
	h, ok := m.config.project.menu.focused()
	require.True(t, ok)
	return h.p.Name
}

func TestProjectPick_ListsTheAccountsProjectsInboxFirst(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
	require.Equal(t, []string{"Inbox", "平台组", "Home"}, projectOffers(m))
	require.Equal(t, "Inbox", focusedProject(t, m), "an empty value is the Inbox")
}

func TestProjectPick_OpensOnTheProjectAlreadyChosen(t *testing.T) {
	t.Parallel()
	m := projectModel(t, &fakeTasks{projects: fakeAccount})
	m.cfg.Todoist.Project = "p_home"
	m = openProjects(t, m)
	require.Equal(t, "Home", focusedProject(t, m))
}

func TestProjectPick_EnterWritesTheProjectsIDAndRebuildsTheFiler(t *testing.T) {
	t.Parallel()
	m := projectModel(t, &fakeTasks{projects: fakeAccount})
	var built []string
	m.deps.NewTodoist = func(token, project string) TodoistClient {
		built = append(built, token+" "+project)
		return &fakeTasks{projects: fakeAccount}
	}
	m = openProjects(t, m)
	m = press(t, m, "down", "enter")

	require.False(t, m.config.project.open)
	require.Equal(t, "p_work", m.cfg.Todoist.Project)
	require.Equal(t, "p_work", configFile(t, m).Todoist.Project)
	require.Equal(t, []string{"tok_a p_work"}, built)
	require.Equal(t, "❯ todoist.project 平台组", configRow(m), "the row names the project, not its id")
}

func TestProjectPick_InboxWritesTheEmptyValue(t *testing.T) {
	t.Parallel()
	m := projectModel(t, &fakeTasks{projects: fakeAccount})
	m.cfg.Todoist.Project = "p_home"
	m = openProjects(t, m)
	m = press(t, m, "up", "up", "enter")

	require.Equal(t, "", m.cfg.Todoist.Project, "no project is how a task lands in the Inbox")
	require.Equal(t, "", configFile(t, m).Todoist.Project)
	require.Equal(t, "❯ todoist.project Inbox", configRow(m))
}

func TestProjectPick_TypingNarrowsByName(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
	m = press(t, m, "h", "o")
	require.Equal(t, []string{"Home"}, projectOffers(m))
	require.Equal(t, "Home", focusedProject(t, m), "a query lands the cursor on its best answer")

	m = press(t, m, "enter")
	require.Equal(t, "p_home", m.cfg.Todoist.Project)
}

func TestProjectPick_KeysThatTypeAreQueryNotCommands(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
	m = press(t, m, "q", "j")
	require.True(t, m.config.project.open, "q types, it does not close")
	require.Equal(t, "qj", m.config.project.input.Value())
}

func TestProjectPick_EscLeavesTheValueAlone(t *testing.T) {
	t.Parallel()
	m := projectModel(t, &fakeTasks{projects: fakeAccount})
	m.cfg.Todoist.Project = "p_home"
	m = openProjects(t, m)
	m = press(t, m, "down", "esc")

	require.False(t, m.config.project.open)
	require.True(t, m.config.open, "esc closes the chooser, not the panel")
	require.Equal(t, "p_home", m.cfg.Todoist.Project)
}

func TestProjectPick_NeedsAToken(t *testing.T) {
	t.Parallel()
	m := configModel(t).openConfig("todoist.project")
	m = press(t, m, "enter")

	require.False(t, m.config.project.open)
	require.Contains(t, m.config.err, "todoist.token")
}

func TestProjectPick_SaysWhyTheListingFailed(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{listErr: errors.New("todoist: 401 Unauthorized")}))

	require.False(t, m.config.project.open)
	require.Contains(t, m.config.err, "401")
}

func TestProjectPick_ShowsLoadingUntilTheListingLands(t *testing.T) {
	t.Parallel()
	m := press(t, projectModel(t, &fakeTasks{projects: fakeAccount}), "enter")
	require.Contains(t, ansi.Strip(strings.Join(m.configLines(), "\n")), "loading projects…")
}

func TestProjectPick_ResetGoesBackToTheInbox(t *testing.T) {
	t.Parallel()
	m := projectModel(t, &fakeTasks{projects: fakeAccount})
	m.cfg.Todoist.Project = "p_home"
	m = press(t, m, "&")
	require.Equal(t, "", m.cfg.Todoist.Project)
	require.Equal(t, "❯ todoist.project Inbox", configRow(m))
}

func TestProjectCell_NamesWhatItKnows(t *testing.T) {
	t.Parallel()
	m := projectModel(t, &fakeTasks{projects: fakeAccount})
	m.cfg.Todoist.Project = "p_home"
	require.Equal(t, "❯ todoist.project p_home", configRow(m), "before a listing only the id is known")

	m = openProjects(t, m)
	m = press(t, m, "esc")
	require.Equal(t, "❯ todoist.project Home", configRow(m))

	m.cfg.Todoist.Project = "p_gone"
	require.Equal(t, "❯ todoist.project p_gone · not found", configRow(m))
}

func TestRunSet_ANewTokenForgetsTheOldAccountsProjects(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
	m = press(t, m, "esc").closeConfig()
	require.NotEmpty(t, m.todoistProjects)

	m = m.runSet("todoist.token=tok_b")
	require.Empty(t, m.todoistProjects)
}

func TestProjectPick_TheWheelWalksTheListNotThePanel(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(Model)

	s, ok := m.configFocus()
	require.True(t, ok)
	require.Equal(t, "todoist.project", s.key, "enter must still write the row the chooser opened on")
	require.Equal(t, "平台组", focusedProject(t, m), "a notch is one row, the way the keys that own the list move")

	token := m.cfg.Todoist.Token
	m = press(t, m, "enter")
	require.Equal(t, "p_work", m.cfg.Todoist.Project)
	require.Equal(t, token, m.cfg.Todoist.Token)
}

func TestProjectPick_DropsAListingFromAnEarlierToken(t *testing.T) {
	t.Parallel()
	m := press(t, projectModel(t, &fakeTasks{projects: fakeAccount}), "enter")
	late := loadTodoistProjects(m.deps, m.cfg.Todoist.Token)()
	m = m.runSet("todoist.token=tok_b")

	next, _ := m.Update(late)
	require.Empty(t, next.(Model).todoistProjects, "the list belongs to the account the token was swapped away from")
}

func TestProjectPick_KeepsItsRowOnScreenInAShortTerminal(t *testing.T) {
	t.Parallel()
	var many []todoist.Project
	for i := range 12 {
		many = append(many, todoist.Project{ID: "p_" + strconv.Itoa(i), Name: "Project " + strconv.Itoa(i), Inbox: i == 0})
	}
	m := projectModel(t, &fakeTasks{projects: many})
	m.height = 12
	m = openProjects(t, m)

	require.Contains(t, configRow(m), "todoist.project", "the row the chooser hangs off stays drawn")
	require.LessOrEqual(t, len(m.configLines()), m.configRows())
}

func TestProjectPick_AListingLandingKeepsTheCursorWhereTheReaderLeftIt(t *testing.T) {
	t.Parallel()
	m := openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
	m = press(t, m, "esc")
	// The second opening draws the list kept from the first while it asks again.
	m = press(t, m, "enter", "down", "down")
	require.Equal(t, "Home", focusedProject(t, m))

	next, _ := m.Update(loadTodoistProjects(m.deps, m.cfg.Todoist.Token)())
	require.Equal(t, "Home", focusedProject(t, next.(Model)))
}
