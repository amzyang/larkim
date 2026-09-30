package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// configModel is a model over a real configuration file, which is what :config
// edits — the panel is the file's editor, so a fake one would test nothing.
func configModel(t *testing.T) Model {
	t.Helper()
	b, err := os.ReadFile("../config.example.yaml")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, b, 0o644))
	cfg, err := config.Load(path)
	require.NoError(t, err)

	m := pickerModel(t)
	m.deps.ConfigPath = path
	m.cfg = cfg
	m = m.openConfig("")
	return m
}

// configFile reads back what the panel wrote.
func configFile(t *testing.T, m Model) config.Config {
	t.Helper()
	cfg, err := config.Load(m.deps.ConfigPath)
	require.NoError(t, err)
	return cfg
}

// configRow is the rendered line the cursor rests on, styling stripped.
func configRow(m Model) string {
	lines := m.configLines()
	i := m.config.idx - m.config.top
	if i < 0 || i >= len(lines) {
		return ""
	}
	return strings.Join(strings.Fields(ansi.Strip(lines[i])), " ")
}

func TestConfig_OpensOnEveryKeyOfTheFile(t *testing.T) {
	m := configModel(t)
	require.True(t, m.config.open)
	require.Len(t, m.config.hits, len(config.Keys()))
	require.Equal(t, "❯ data_dir "+m.cfg.DataDir, configRow(m))
}

func TestConfig_ShowsTheValueThisSessionRunsNotTheFiles(t *testing.T) {
	m := configModel(t)
	m = m.closeConfig().runSet("applink_pace_ms=1500")
	m = m.openConfig("applink_pace_ms")
	require.Equal(t, "❯ applink_pace_ms 1500", configRow(m))
	require.Equal(t, 1000, configFile(t, m).ApplinkPaceMS, "a :set leaves the file alone")
}

func TestConfig_JumpsToTheKeyItWasNamed(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("ai.model")
	s, ok := m.configFocus()
	require.True(t, ok)
	require.Equal(t, "ai.model", s.key)
}

func TestConfig_RefusesAKeyTheFileHasNot(t *testing.T) {
	m := configModel(t)
	m = m.closeConfig().openConfig("nope")
	require.False(t, m.config.open)
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "nope")
}

func TestConfig_AnEditWritesTheFileAndReachesTheSession(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("applink_pace_ms")
	m = press(t, m, "enter")
	require.True(t, m.config.editing)
	m = press(t, m, "ctrl+u", "1", "5", "0", "0", "enter")

	require.False(t, m.config.editing)
	require.Equal(t, 1500*time.Millisecond, m.applinkPace(), "the running queue re-arms on it")
	require.Equal(t, 1500, configFile(t, m).ApplinkPaceMS)
	require.Equal(t, "applink_pace_ms=1500", m.notice)
	require.False(t, m.noticeErr)
}

func TestConfig_AnEditOfAStartupKeyWritesTheFileAndSaysSo(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("backfill_days")
	m = press(t, m, "enter", "ctrl+u", "7", "enter")

	require.Equal(t, 7, configFile(t, m).BackfillDays)
	require.Equal(t, "backfill_days=7 · next start", m.notice)
}

func TestConfig_ANestedKeyReachesItsSectionAlone(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("ai.context")
	m = press(t, m, "enter", "ctrl+u", "2", "0", "enter")

	cfg := configFile(t, m)
	require.Equal(t, 20, cfg.AI.Context)
	require.Equal(t, "claude-opus-5", cfg.AI.Model, "its siblings stay")
}

func TestConfig_ARefusedValueKeepsWhatWasTyped(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("applink_pace_ms")
	m = press(t, m, "enter", "ctrl+u", "0", "enter")

	require.True(t, m.config.editing, "the reader keeps the line they were on")
	require.Equal(t, "0", m.config.editor.Value())
	require.Contains(t, m.config.err, "milliseconds")
	require.Contains(t, ansi.Strip(m.configDetail()), "milliseconds")
	require.Equal(t, 1000, configFile(t, m).ApplinkPaceMS, "nothing was written")
	require.Equal(t, applinkDefaultPaceForTest, m.applinkPace(), "nothing was applied")
}

func TestConfig_RefusesADurationSpellingOfThePollInterval(t *testing.T) {
	// The unit is in the key's name, so 3s is the reader writing it twice.
	m := configModel(t)
	m = m.openConfig("poll_interval_ms")
	m = press(t, m, "enter", "ctrl+u", "3", "s", "enter")

	require.True(t, m.config.editing)
	require.Contains(t, m.config.err, "milliseconds")
	require.Equal(t, 3000, configFile(t, m).PollIntervalMS, "nothing was written")
}

func TestConfig_RestoresTheDefault(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("backfill_days")
	m = press(t, m, "&")
	require.Equal(t, 30, configFile(t, m).BackfillDays)
	require.Equal(t, "backfill_days=30 · next start", m.notice)
}

func TestConfig_ResetRefusesTheSilenceList(t *testing.T) {
	m := configModel(t)
	m.cfg.Silence = store.SilenceRules{{Chat: "oc_quiet"}}
	m = press(t, m, "G", "&")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "Silence tab")
	require.Equal(t, store.SilenceRules{{Chat: "oc_quiet"}}, m.cfg.Silence, "the rules stay")
}

func TestConfig_SilenceKeyOpensTheSilenceTab(t *testing.T) {
	m := configModel(t)
	m = m.closeConfig().openConfig("silence")
	require.Equal(t, tabSilence, m.config.tab, ":config silence")

	m = configModel(t)
	m = press(t, m, "G")
	require.Equal(t, "❯ silence 0 rules", configRow(m), "the list is summed up, not spelled inline")
	m = press(t, m, "enter")
	require.Equal(t, tabSilence, m.config.tab, "enter on the row")
	require.False(t, m.config.editing)
}

func TestConfig_TabSwitchesBetweenGeneralAndSilence(t *testing.T) {
	m := configModel(t)
	m = press(t, m, "j", "tab")
	require.Equal(t, tabSilence, m.config.tab)
	require.Contains(t, ansi.Strip(m.renderConfig()), "no silence rules")
	m = press(t, m, "tab")
	require.Equal(t, tabGeneral, m.config.tab)
	require.Equal(t, 1, m.config.idx, "General keeps where it was")
	m = press(t, m, "shift+tab")
	require.Equal(t, tabSilence, m.config.tab, "and back the other way")
}

func TestConfig_TabIsTypedIntoAnOpenEditor(t *testing.T) {
	m := configModel(t)
	m = press(t, m, "/", "tab")
	require.Equal(t, tabGeneral, m.config.tab, "the filter keeps the key")
	m = configModel(t)
	m = m.openConfig("backfill_days")
	m = press(t, m, "enter", "tab")
	require.Equal(t, tabGeneral, m.config.tab, "and so does the editor")
	require.True(t, m.config.editing)
}

func TestConfig_FilterNarrowsOnKeyAndOnProse(t *testing.T) {
	m := configModel(t)
	m = press(t, m, "/", "a", "i", ".")
	require.Len(t, m.config.hits, 5)

	m = configModel(t)
	m = press(t, m, "/", "u", "n", "l", "i", "m", "i", "t", "e", "d")
	require.Len(t, m.config.hits, 1, "the prose is matched too")
	require.Equal(t, "resources.max_bytes", m.config.hits[0].s.key)

	m = configModel(t)
	m = press(t, m, "/", "a", "t", "t", "a", "c", "h")
	require.Equal(t, []string{"data_dir", "resources.max_bytes"},
		[]string{m.config.hits[0].s.key, m.config.hits[1].s.key},
		"a row the query is literally in stands over one fzf spelled out of it")
}

func TestConfig_EscBacksOutOneLayerAtATime(t *testing.T) {
	m := configModel(t)
	m = press(t, m, "/", "a", "i", "esc")
	require.True(t, m.config.open)
	require.Empty(t, m.config.input.Value())
	require.Len(t, m.config.hits, len(config.Keys()))
	m = press(t, m, "esc")
	require.False(t, m.config.open)
}

func TestConfig_EscOutOfAnEditKeepsThePanel(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("backfill_days")
	m = press(t, m, "enter", "9", "esc")
	require.False(t, m.config.editing)
	require.True(t, m.config.open)
	require.Equal(t, 30, configFile(t, m).BackfillDays)
}

func TestConfig_AStrayKeyLeavesThePanelStanding(t *testing.T) {
	m := configModel(t)
	m = press(t, m, "j", "j", "z")
	require.True(t, m.config.open)
	require.Equal(t, 2, m.config.idx, "and where the reader had scrolled to")
}

func TestConfig_TakesEveryKeyAheadOfTheHelpPanel(t *testing.T) {
	m := configModel(t)
	m.help = helpPanel{open: true}
	m = press(t, m, "j")
	require.Equal(t, 1, m.config.idx)
	require.True(t, m.help.open, "the help panel is left as it was")
}

func TestConfig_RenderFillsTheBoxAtEveryWidth(t *testing.T) {
	for _, tab := range []configTab{tabGeneral, tabSilence} {
		for _, w := range []int{minWidth, 100, 160} {
			m := configModel(t)
			m.width, m.height = w, 24
			m.layout()
			m.cfg.Silence = store.SilenceRules{{Chat: "oc_team", Sender: "cli_c", Contains: strings.Repeat("nightly build ", 10)}}
			m.config.tab = tab
			out := ansi.Strip(m.renderConfig())
			lines := strings.Split(out, "\n")
			require.Len(t, lines, m.configRows()+7, "tab %d width %d", tab, w)
			for i, line := range lines {
				require.Equal(t, w-4, lipgloss.Width(line), "tab %d width %d line %d: %q", tab, w, i, line)
			}
		}
	}
}

func TestConfig_TheOverlayIsTheWholeView(t *testing.T) {
	m := configModel(t)
	v := m.View()
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	require.Len(t, lines, m.height)
	require.Contains(t, v.Content, "config")
	require.Contains(t, ansi.Strip(v.Content), filepath.Base(m.deps.ConfigPath),
		"a path too long for the head row keeps the end that names the file")
}

func TestConfig_TheWheelScrollsIt(t *testing.T) {
	m := configModel(t)
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Positive(t, next.(Model).config.idx)
}

// applinkDefaultPaceForTest is the gap configModel starts on, which is the
// example file's own applink_pace_ms.
const applinkDefaultPaceForTest = time.Second

func TestConfig_ANewModelRebuildsTheAssistantAndIsWritten(t *testing.T) {
	m := configModel(t)
	var asked []string
	m.deps.NewAI = func(model, keyEnv string) AIStreamer {
		asked = append(asked, model+" "+keyEnv)
		return nil
	}
	m = m.openConfig("ai.model")
	m = press(t, m, "enter", "ctrl+u")
	for _, r := range "claude-sonnet-5" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")

	require.Equal(t, []string{"claude-sonnet-5 ANTHROPIC_API_KEY"}, asked)
	require.Equal(t, "claude-sonnet-5", configFile(t, m).AI.Model)
	require.Equal(t, "ai.model=claude-sonnet-5", m.notice, "no restart to wait for")
}

func TestConfig_ANewContextReachesTheNextQuestion(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("ai.context")
	m = press(t, m, "enter", "ctrl+u", "1", "2", "enter")
	require.Equal(t, 12, m.cfg.AI.Context, "what startAI hands the assistant")
	require.Equal(t, 12, configFile(t, m).AI.Context)
	require.Equal(t, "ai.context=12", m.notice)
}

func TestConfig_TheEditorOpensInTheCellItReplaces(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("poll_interval_ms")
	m = press(t, m, "enter")

	v := m.View()
	require.NotNil(t, v.Cursor)
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	row := []rune(lines[v.Cursor.Position.Y])
	require.Contains(t, string(row), "poll_interval_ms", "the caret sits in the row being edited")
	x := v.Cursor.Position.X
	require.Equal(t, "3000", string(row[x-4:x]), "and just past the value it is editing")
}

func TestConfig_TheCellBeingEditedLooksUnlikeAValue(t *testing.T) {
	m := configModel(t)
	m = m.openConfig("poll_interval_ms")
	// The box's own border, its head row and the blank under it come first.
	at := m.config.idx + 3
	resting := strings.Split(m.renderConfig(), "\n")[at]
	editing := strings.Split(press(t, m, "enter").renderConfig(), "\n")[at]
	require.NotEqual(t, resting, editing, "a field the reader is typing into is shaded, not bare text")
	require.Equal(t, ansi.Strip(resting), ansi.Strip(editing), "and the row keeps its columns")
}

func TestConfig_TheEditorWindowsAValueLongerThanItsColumn(t *testing.T) {
	m := configModel(t)
	m.width, m.height = minWidth, 24
	m.layout()
	m = m.openConfig("data_dir")
	m = press(t, m, "enter")
	long := strings.Repeat("/deep", 40)
	for _, r := range long {
		m = press(t, m, string(r))
	}
	cell, at := m.configEditorCell(m.configValueWidth())
	require.Equal(t, m.configValueWidth(), lipgloss.Width(ansi.Strip(cell)), "the field keeps its column")
	require.Less(t, at, m.configValueWidth(), "and the caret stays inside it")
	require.Contains(t, ansi.Strip(cell), "deep", "showing the end being typed")
}

func TestConfig_TakesAPasteIntoTheFilterAndTheEditor(t *testing.T) {
	m := configModel(t)
	m = press(t, m, "/")
	m = paste(t, m, "unlimited")
	require.Len(t, m.config.hits, 1, "a pasted query filters the way a typed one does")

	m = configModel(t)
	m = m.openConfig("applink_pace_ms")
	m = press(t, m, "enter", "ctrl+u")
	m = paste(t, m, "1500")
	require.Equal(t, "1500", m.config.editor.Value())
}
