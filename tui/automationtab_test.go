package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const testAutomationExecutable = "/Users/linlan/bin/larkim"

func automationModel(t *testing.T) Model {
	t.Helper()
	m := notifyModel(t)
	m = press(t, m, "tab")
	require.Equal(t, tabAutomation, m.config.tab)
	m.deps.Executable = func() (string, error) { return testAutomationExecutable, nil }
	return m
}

func automationRule() automation.Rule {
	return automation.Rule{ID: "build", Criteria: automation.Criteria{ChatIDs: []string{"oc_quiet"}, Contains: []string{"build ready"}}, Callback: automation.Callback{Executable: "/usr/bin/true", Args: []string{"one argument", "a,b", "quoted \"value\""}, Timeout: time.Minute}}
}

func seedAutomation(t *testing.T, m Model, rules ...automation.Rule) Model {
	t.Helper()
	require.NoError(t, config.SetFileValue(m.deps.ConfigPath, "automation", rules))
	m.cfg.Automation = rules
	return m
}

func automationCommand(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.onAutomationKey(keyMsg(key))
	return next.(Model), cmd
}

func finishAutomation(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	require.NotNil(t, cmd)
	next, _ := m.Update(cmd())
	return next.(Model)
}

func TestAutomationTab_NewRuleIsDisabledAndSavesOnlyAfterCompletion(t *testing.T) {
	t.Parallel()
	m := press(t, automationModel(t), "a")
	require.True(t, m.config.automation.form.open)
	require.Equal(t, "false", m.config.automation.form.inputs[automationEnabled].Value())
	m.config.automation.form.inputs[automationID].SetValue("build")
	m.config.automation.form.inputs[automationChats].SetValue(`["oc_quiet"]`)
	m.config.automation.form.inputs[automationContains].SetValue(`["build ready", "a,b"]`)
	m.config.automation.form.inputs[automationExecutable].SetValue("/usr/bin/true")
	m.config.automation.form.inputs[automationArgs].SetValue(`["one argument", "a,b", "quoted \"value\""]`)
	m, cmd := automationCommand(t, m, "ctrl+s")
	require.Empty(t, m.cfg.Automation)
	require.Empty(t, configFile(t, m).Automation)
	m = finishAutomation(t, m, cmd)
	require.False(t, m.config.automation.form.open)
	require.False(t, m.cfg.Automation[0].Enabled)
	require.Equal(t, []string{"one argument", "a,b", "quoted \"value\""}, m.cfg.Automation[0].Callback.Args)
	require.Equal(t, m.cfg.Automation, configFile(t, m).Automation)
	require.Contains(t, ansi.Strip(m.renderConfig()), "Restart required")
	m = m.closeConfig().openConfig("automation")
	require.Equal(t, tabAutomation, m.config.tab)
	require.Contains(t, ansi.Strip(m.renderConfig()), "Restart required")
}

func TestAutomationTab_EditKeepsIDAndPreservesLatestFileRules(t *testing.T) {
	t.Parallel()
	rule := automationRule()
	m := seedAutomation(t, automationModel(t), rule)
	m = press(t, m, "enter")
	m = paste(t, m, "renamed")
	require.Equal(t, "build", m.config.automation.form.inputs[automationID].Value())
	m.config.automation.form.inputs[automationArgs].SetValue(`["changed value"]`)
	require.Equal(t, "one argument", rule.Callback.Args[0], "the editor never mutates shared configuration")
	other := automationRule()
	other.ID = "other"
	require.NoError(t, config.SetFileValue(m.deps.ConfigPath, "automation", []automation.Rule{rule, other}))
	require.NoError(t, config.SetFile(m.deps.ConfigPath, "ai.context", "17"))
	m, cmd := automationCommand(t, m, "ctrl+s")
	m = finishAutomation(t, m, cmd)
	require.Len(t, m.cfg.Automation, 2)
	require.Equal(t, "build", m.cfg.Automation[0].ID)
	require.Equal(t, []string{"changed value"}, m.cfg.Automation[0].Callback.Args)
	require.Equal(t, 17, configFile(t, m).AI.Context)
	require.Equal(t, []string{"one argument", "a,b", "quoted \"value\""}, rule.Callback.Args)
}

func TestAutomationTab_ToggleAndConfirmedDeleteAreAsync(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automationRule())
	m, cmd := automationCommand(t, m, " ")
	require.False(t, m.cfg.Automation[0].Enabled)
	m = finishAutomation(t, m, cmd)
	require.True(t, m.cfg.Automation[0].Enabled)
	m = press(t, m, "d", "n")
	require.Len(t, configFile(t, m).Automation, 1)
	m = press(t, m, "d")
	m, cmd = automationCommand(t, m, "y")
	require.Len(t, m.cfg.Automation, 1)
	m = finishAutomation(t, m, cmd)
	require.Empty(t, m.cfg.Automation)
	require.Empty(t, configFile(t, m).Automation)
}

func TestAutomationTab_InvalidAndFailedWritesKeepEditorAndConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, value string }{{"not JSON", "a b"}, {"not array", `"arg"`}, {"not strings", `[1]`}} {
		t.Run(tc.name, func(t *testing.T) {
			m := seedAutomation(t, automationModel(t), automationRule())
			m = press(t, m, "enter")
			m.config.automation.form.inputs[automationArgs].SetValue(tc.value)
			m, cmd := automationCommand(t, m, "ctrl+s")
			require.Nil(t, cmd)
			require.True(t, m.config.automation.form.open)
			require.NotEmpty(t, m.config.automation.err)
			require.True(t, sameAutomationRule(automationRule(), configFile(t, m).Automation[0]))
		})
	}
	m := seedAutomation(t, automationModel(t), automationRule())
	m = press(t, m, "enter")
	m.deps.ConfigPath = filepath.Join(m.deps.ConfigPath, "invalid.yaml")
	m, cmd := automationCommand(t, m, "ctrl+s")
	m = finishAutomation(t, m, cmd)
	require.True(t, m.config.automation.form.open)
	require.NotEmpty(t, m.config.automation.err)
	require.Equal(t, automationRule(), m.cfg.Automation[0])
}

func TestAutomationTab_QuotaPresetResolvesExecutableAndRequiresExplicitScope(t *testing.T) {
	t.Parallel()
	m := automationModel(t)
	called := false
	m.deps.Executable = func() (string, error) { called = true; return testAutomationExecutable, nil }
	m, cmd := automationCommand(t, m, "q")
	require.False(t, called)
	m = finishAutomation(t, m, cmd)
	require.True(t, called)
	f := &m.config.automation.form
	require.Equal(t, "false", f.inputs[automationEnabled].Value())
	require.Equal(t, testAutomationExecutable, f.inputs[automationExecutable].Value())
	m, cmd = automationCommand(t, m, "ctrl+s")
	require.Nil(t, cmd)
	require.Contains(t, m.config.automation.err, "chat")
	m.config.automation.form.inputs[automationChats].SetValue(`["oc_quiet"]`)
	m.config.automation.form.inputs[automationSenders].SetValue(`["cli_c"]`)
	m, cmd = automationCommand(t, m, "ctrl+s")
	m = finishAutomation(t, m, cmd)
	r := m.cfg.Automation[0]
	require.Equal(t, []string{"interactive"}, r.Criteria.MessageTypes)
	require.Equal(t, []string{"AI Coding 总额度已用", "再次申请加油包（+50%）"}, r.Criteria.Contains)
	require.Equal(t, []string{"automation", "claim-quota", "--chat-id", "oc_quiet", "--sender-id", "cli_c"}, r.Callback.Args)
	require.Equal(t, time.Minute, r.Callback.Timeout)
}

func TestAutomationTab_CheckIsConfirmedBoundedAndNeverRunsArbitraryCallbacks(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automationRule())
	calls := 0
	m.deps.CheckQuota = func(ctx context.Context, r automation.Rule) (string, error) {
		calls++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.InDelta(t, 60, time.Until(deadline).Seconds(), 1)
		require.Equal(t, "oc_quiet", r.Criteria.ChatIDs[0])
		return "Claim button available", nil
	}
	m, cmd := automationCommand(t, m, "c")
	require.Nil(t, cmd)
	require.Zero(t, calls)
	r := automation.QuotaRule("quota", testAutomationExecutable, "oc_quiet", "cli_c")
	m = seedAutomation(t, m, r)
	m = press(t, m, "c")
	view := ansi.Strip(m.renderConfig())
	require.Contains(t, view, "Lingxi")
	require.Contains(t, view, "foreground")
	require.Contains(t, view, "no click")
	m = press(t, m, "n")
	require.Zero(t, calls)
	m = press(t, m, "c")
	m, cmd = automationCommand(t, m, "y")
	require.Zero(t, calls)
	m = finishAutomation(t, m, cmd)
	require.Equal(t, 1, calls)
	require.Contains(t, m.config.automation.result, "Claim button available")
	require.False(t, m.automationRestart)

	r.Callback.Executable = "/tmp/arbitrary"
	m = seedAutomation(t, m, r)
	m = press(t, m, "c")
	m, cmd = automationCommand(t, m, "y")
	m = finishAutomation(t, m, cmd)
	require.Equal(t, 1, calls)
	require.Contains(t, m.config.automation.err, "executable")
}

func TestAutomationTab_CheckFailureIsVisible(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automation.QuotaRule("quota", testAutomationExecutable, "oc_quiet", "cli_c"))
	m.deps.CheckQuota = func(context.Context, automation.Rule) (string, error) { return "", errors.New("no matching message") }
	m = press(t, m, "c")
	m, cmd := automationCommand(t, m, "y")
	m = finishAutomation(t, m, cmd)
	require.Contains(t, m.config.automation.err, "no matching message")
}

func TestAutomationTab_PickersOfferChatsBotsAndTypes(t *testing.T) {
	t.Parallel()
	m := automationModel(t)
	m.contacts = append(m.contacts, store.Contact{OpenID: "cli_c", Name: "构建机器人"})
	m = press(t, m, "a", "tab", "tab", "enter")
	require.True(t, m.config.automation.form.pick.open)
	m = paste(t, m, "平台")
	m = press(t, m, "enter")
	require.Equal(t, `["oc_quiet"]`, m.config.automation.form.inputs[automationChats].Value())
	m = press(t, m, "tab", "enter")
	m = paste(t, m, "构建")
	m = press(t, m, "enter")
	require.Equal(t, `["cli_c"]`, m.config.automation.form.inputs[automationSenders].Value())
	m = press(t, m, "tab", "enter")
	m = paste(t, m, "interactive")
	m = press(t, m, "enter")
	require.Equal(t, `["interactive"]`, m.config.automation.form.inputs[automationTypes].Value())
}

func TestAutomationTab_QuotaScopeUpdatesVisibleArgs(t *testing.T) {
	t.Parallel()
	m := automationModel(t)
	m, cmd := automationCommand(t, m, "q")
	m = finishAutomation(t, m, cmd)
	m = press(t, m, "tab", "tab", "enter")
	m = paste(t, m, "平台")
	m = press(t, m, "enter", "tab", "enter")
	m = paste(t, m, "cli_c")
	m = press(t, m, "enter")
	require.Equal(t, `["automation","claim-quota","--chat-id","oc_quiet","--sender-id","cli_c"]`, m.config.automation.form.inputs[automationArgs].Value())
}

func TestAutomationTab_QuotaArgsFollowScopeAndAreNotFreeText(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automation.QuotaRule("quota", testAutomationExecutable, "oc_quiet", "cli_c"))
	m = press(t, m, "enter")
	next, _ := m.focusAutomationField(automationArgs)
	m = next.(Model)
	before := m.config.automation.form.inputs[automationArgs].Value()
	m = paste(t, m, "unrelated")
	require.Equal(t, before, m.config.automation.form.inputs[automationArgs].Value())
	require.Contains(t, ansi.Strip(m.automationDetail()), "Derived from Chats and Senders")
}

func TestAutomationTab_CheckHintOnlyForQuotaPreset(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automationRule())
	require.NotContains(t, m.automationHintBar(), KeyBinding{Keys: "c", Desc: "check"})
	m = seedAutomation(t, m, automation.QuotaRule("quota", testAutomationExecutable, "oc_quiet", "cli_c"))
	m.deps.CheckQuota = func(context.Context, automation.Rule) (string, error) { return "", nil }
	require.Contains(t, m.automationHintBar(), KeyBinding{Keys: "c", Desc: "check"})
}

func TestAutomationTab_RefusesDuplicateAndConcurrentRuleEdits(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automationRule())
	m = press(t, m, "enter")
	changed := automationRule()
	changed.Callback.Args = []string{"external edit"}
	require.NoError(t, config.SetFileValue(m.deps.ConfigPath, "automation", []automation.Rule{changed}))
	m, cmd := automationCommand(t, m, "ctrl+s")
	m = finishAutomation(t, m, cmd)
	require.Contains(t, m.config.automation.err, "changed")
	require.Equal(t, changed.Callback.Args, configFile(t, m).Automation[0].Callback.Args)
	require.True(t, m.config.automation.form.open)

	m = press(t, m, "esc", "a")
	m.config.automation.form.inputs[automationID].SetValue("build")
	m.config.automation.form.inputs[automationExecutable].SetValue("/usr/bin/true")
	m.config.automation.form.inputs[automationChats].SetValue(`["oc_quiet"]`)
	m, cmd = automationCommand(t, m, "ctrl+s")
	m = finishAutomation(t, m, cmd)
	require.Contains(t, m.config.automation.err, "unique")
	require.Len(t, configFile(t, m).Automation, 1)
}

func TestAutomationTab_ClipboardPasteFollowsFocusedEditor(t *testing.T) {
	t.Parallel()
	m := press(t, automationModel(t), "a")
	m = pasteFromClipboardKey(t, m, clip{kind: clipText, text: "build"}, keyMsg("super+v"))
	require.Equal(t, "build", m.config.automation.form.inputs[automationID].Value())
	m = press(t, m, "tab", "tab", "enter")
	m = pasteFromClipboardKey(t, m, clip{kind: clipText, text: "平台"}, keyMsg("super+v"))
	require.Equal(t, "oc_quiet", m.config.automation.form.pick.hits[0].id)
}

func TestAutomationTab_ListShowsScopeCallbackAndRestart(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automationRule())
	m.automationRestart = true
	m.width, m.height = 60, 24
	view := ansi.Strip(m.renderConfig())
	for _, want := range []string{"oc_quiet", "Any sender", "/usr/bin/true", "Restart required"} {
		require.Contains(t, view, want)
	}
	require.LessOrEqual(t, lipgloss.Width(view), 60)
}

func TestAutomationTab_CheckEscapeCancelsAndIgnoresLateResult(t *testing.T) {
	t.Parallel()
	m := seedAutomation(t, automationModel(t), automation.QuotaRule("quota", testAutomationExecutable, "oc_quiet", "cli_c"))
	started := make(chan struct{})
	m.deps.CheckQuota = func(ctx context.Context, _ automation.Rule) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	m = press(t, m, "c")
	m, cmd := automationCommand(t, m, "y")
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	m = press(t, m, "esc")
	require.False(t, m.config.open)
	m = m.openConfig("automation")
	select {
	case msg := <-result:
		next, _ := m.Update(msg)
		m = next.(Model)
	case <-time.After(time.Second):
		t.Fatal("Escape did not cancel check")
	}
	require.Empty(t, m.config.automation.err)
	require.Empty(t, m.config.automation.result)
}

func TestAutomationTab_RenderFitsSmallScreens(t *testing.T) {
	t.Parallel()
	for _, width := range []int{80, 60} {
		m := seedAutomation(t, automationModel(t), automationRule())
		m.width, m.height = width, 24
		for _, keys := range [][]string{nil, {"enter"}, {"esc", "a", "tab", "tab", "enter"}} {
			m = press(t, m, keys...)
			view := m.renderConfig()
			require.LessOrEqual(t, lipgloss.Width(view), width)
			require.LessOrEqual(t, lipgloss.Height(view), 24)
			for line := range strings.SplitSeq(view, "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), width)
			}
		}
	}
}
