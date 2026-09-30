package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// silenceModel is the config panel on its Silence tab, over two named chats
// and two contacts, one of them a bot.
func silenceModel(t *testing.T) Model {
	t.Helper()
	m := configModel(t)
	m.chats = []store.Chat{{ChatID: "oc_quiet", Name: "平台组"}, {ChatID: "oc_team", Name: "项目协作群"}}
	m.contacts = []store.Contact{{OpenID: "cli_c", Name: "构建机器人", IsBot: true}, {OpenID: "ou_a", Name: "张三"}}
	m = press(t, m, "tab")
	require.Equal(t, tabSilence, m.config.tab)
	return m
}

// typeText presses each rune of s as a key of its own.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = press(t, m, string(r))
	}
	return m
}

// silenceRow is the rendered rule the cursor rests on, styling stripped.
func silenceRow(m Model) string {
	lines := m.silenceLines()
	i := m.config.silence.idx - m.config.silence.top
	if i < 0 || i >= len(lines) {
		return ""
	}
	return strings.Join(strings.Fields(ansi.Strip(lines[i])), " ")
}

func TestSilenceTab_AddsARuleThroughThePickersAndWritesTheFile(t *testing.T) {
	m := silenceModel(t)
	m = press(t, m, "a", "enter")
	require.True(t, m.config.silence.form.pick.open)
	m = typeText(t, m, "平台")
	m = press(t, m, "enter", "tab", "enter")
	m = typeText(t, m, "构建")
	m = press(t, m, "enter")
	require.Equal(t, store.SilenceRule{Chat: "oc_quiet", Sender: "cli_c"}, m.config.silence.form.rule)
	m = press(t, m, "tab")
	m = typeText(t, m, "nightly build")
	m = press(t, m, "enter")

	want := store.SilenceRules{{Chat: "oc_quiet", Sender: "cli_c", Contains: "nightly build"}}
	require.False(t, m.config.silence.form.open)
	require.Equal(t, want, m.cfg.Silence)
	require.Equal(t, want, configFile(t, m).Silence)
	require.Equal(t, "silence · 1 rule · next start", m.notice)
	require.False(t, m.noticeErr)
	text, err := os.ReadFile(m.deps.ConfigPath)
	require.NoError(t, err)
	require.Contains(t, string(text), "silence:\n  - chat: oc_quiet\n    sender: cli_c\n    contains: nightly build\n",
		"written as a block list")
}

func TestSilenceTab_AFieldLeftUnsetIsNotWritten(t *testing.T) {
	m := silenceModel(t)
	m = press(t, m, "a", "enter")
	m = typeText(t, m, "平台")
	m = press(t, m, "enter", "tab", "tab", "enter")

	require.Equal(t, store.SilenceRules{{Chat: "oc_quiet"}}, configFile(t, m).Silence)
	text, err := os.ReadFile(m.deps.ConfigPath)
	require.NoError(t, err)
	require.NotContains(t, string(text), "sender: \"\"")
}

func TestSilenceTab_RefusesAnEmptyRule(t *testing.T) {
	m := silenceModel(t)
	m = press(t, m, "a", "tab", "tab", "enter")

	require.True(t, m.config.silence.form.open, "the form stays for the reader to finish")
	require.Contains(t, ansi.Strip(m.silenceDetail()), "set at least one of chat, sender, contains")
	require.Empty(t, configFile(t, m).Silence, "nothing was written")
	require.Empty(t, m.cfg.Silence)
}

func TestSilenceTab_EditReplacesOnlyItsRule(t *testing.T) {
	m := silenceModel(t)
	rules := store.SilenceRules{{Chat: "oc_quiet"}, {Sender: "cli_c", Contains: "nightly"}}
	require.NoError(t, m.writeSilence(rules))
	m = press(t, m, "j", "enter")
	require.Equal(t, fieldContains, m.config.silence.form.field, "a rule with text opens on it")
	m = press(t, m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	m = typeText(t, m, "daily")
	m = press(t, m, "enter")

	want := store.SilenceRules{{Chat: "oc_quiet"}, {Sender: "cli_c", Contains: "daily"}}
	require.Equal(t, want, configFile(t, m).Silence)
	require.Equal(t, 1, m.config.silence.idx, "the cursor stays on the rule")
}

func TestSilenceTab_EscAbandonsTheForm(t *testing.T) {
	m := silenceModel(t)
	m = press(t, m, "a", "tab", "tab")
	m = typeText(t, m, "nightly")
	m = press(t, m, "esc")
	require.False(t, m.config.silence.form.open)
	require.True(t, m.config.open)
	require.Empty(t, configFile(t, m).Silence)
}

func TestSilenceTab_BackspaceClearsAnIDField(t *testing.T) {
	m := silenceModel(t)
	require.NoError(t, m.writeSilence(store.SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}))
	m = press(t, m, "enter", "tab", "backspace", "tab", "enter")
	require.Equal(t, store.SilenceRules{{Chat: "oc_quiet"}}, configFile(t, m).Silence)
}

func TestSilenceTab_DeleteAsksFirst(t *testing.T) {
	m := silenceModel(t)
	require.NoError(t, m.writeSilence(store.SilenceRules{{Chat: "oc_quiet"}, {Sender: "cli_c"}}))
	m = press(t, m, "d")
	require.Contains(t, ansi.Strip(m.silenceDetail()), "delete this rule? y/n")
	m = press(t, m, "n")
	require.Len(t, configFile(t, m).Silence, 2, "anything but y keeps it")

	m = press(t, m, "d", "y")
	require.Equal(t, store.SilenceRules{{Sender: "cli_c"}}, configFile(t, m).Silence)
	m = press(t, m, "d", "y")
	require.Empty(t, configFile(t, m).Silence)
	require.Contains(t, ansi.Strip(m.silenceDetail()), "no silence rules")
}

func TestSilenceSearch_ListsNamedChatMembersBeforeContacts(t *testing.T) {
	m := silenceModel(t)
	m.config.silence.form = silenceForm{
		open: true,
		rule: store.SilenceRule{Chat: "oc_quiet"},
	}
	m.config.silence.roster = []store.Contact{{OpenID: "ou_in", Name: "王五"}}
	m.contacts = []store.Contact{
		{OpenID: "cli_c", Name: "构建机器人", IsBot: true},
		{OpenID: "ou_a", Name: "张三"},
		{OpenID: "ou_in", Name: "王五"},
	}
	hits := m.silenceSearch(fieldSender, "")
	require.GreaterOrEqual(t, len(hits), 2)
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.id
	}
	require.Equal(t, "ou_in", ids[0])
	require.Less(t, slices.Index(ids, "ou_in"), slices.Index(ids, "ou_a"))
}

func TestSilenceTab_RosterLoadRefreshesTheSenderPicker(t *testing.T) {
	m := silenceModel(t)
	ctx := t.Context()
	require.NoError(t, m.deps.Store.EnsureChat(ctx, "oc_quiet", 1))
	require.NoError(t, m.deps.Store.SetChatMembers(ctx, "oc_quiet",
		[]store.Contact{{OpenID: "ou_in", Name: "王五"}}, false, 1))
	m.contacts = []store.Contact{{OpenID: "ou_a", Name: "张三"}, {OpenID: "ou_in", Name: "王五"}}
	m.config.silence.form = silenceForm{
		open:  true,
		field: fieldSender,
		rule:  store.SilenceRule{Chat: "oc_quiet"},
		pick:  silencePick{open: true},
	}

	m = m.onSilenceRoster(loadSilenceRoster(m.deps, "oc_quiet")().(silenceRosterLoadedMsg))

	require.Len(t, m.config.silence.roster, 1)
	require.Equal(t, "ou_in", m.config.silence.roster[0].OpenID)
	require.Equal(t, "ou_in", m.silenceSearch(fieldSender, "")[0].id)
	require.Equal(t, "ou_in", m.config.silence.form.pick.hits[0].id)
}

func TestSilenceTab_PickerTakesARawIDWhenNothingMatches(t *testing.T) {
	m := silenceModel(t)
	m = press(t, m, "a", "tab", "enter")
	m = typeText(t, m, "cli_zz")
	require.Empty(t, m.config.silence.form.pick.hits)
	m = press(t, m, "enter")
	require.Equal(t, "cli_zz", m.config.silence.form.rule.Sender)
}

func TestSilenceTab_EscOutOfThePickerKeepsTheField(t *testing.T) {
	m := silenceModel(t)
	require.NoError(t, m.writeSilence(store.SilenceRules{{Chat: "oc_quiet"}}))
	m = press(t, m, "enter", "enter")
	m = typeText(t, m, "项目")
	m = press(t, m, "esc")
	require.False(t, m.config.silence.form.pick.open)
	require.Equal(t, "oc_quiet", m.config.silence.form.rule.Chat)
}

func TestSilenceTab_NamesChatsAndSendersAndFallsBackToTheID(t *testing.T) {
	m := silenceModel(t)
	require.NoError(t, m.writeSilence(store.SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}, {Chat: "oc_gone", Contains: "nightly"}}))
	require.Equal(t, "❯ 平台组 构建机器人 — …", silenceRow(m))
	m = press(t, m, "j")
	require.Equal(t, "❯ oc_gone — nightly …", silenceRow(m))
	cw, _, _ := m.silenceColumns()
	require.Contains(t, m.silenceLines()[1], stErr.Render(fit("oc_gone", cw)), "an id nothing names stands out")
}

func TestSilenceTab_ShowsMatchCountsAndIgnoresRulesSinceRemoved(t *testing.T) {
	m := silenceModel(t)
	stale := store.SilenceRules{{Chat: "oc_team"}, {Sender: "cli_c"}}
	require.NoError(t, m.writeSilence(stale))
	msg := loadSilenceMatches(m.deps, stale)()
	require.NoError(t, m.writeSilence(store.SilenceRules{{Chat: "oc_team"}}))
	next, _ := m.Update(msg)
	m = next.(Model)

	require.Equal(t, "❯ 项目协作群 — — 1", silenceRow(m))
	require.Contains(t, ansi.Strip(m.silenceDetail()), "chat 项目协作群 · matched 1 · last match")
	require.Len(t, m.silenceLines(), 1, "the removed rule is not drawn")

	require.NoError(t, m.writeSilence(store.SilenceRules{{Sender: "cli_c"}}))
	next, _ = m.Update(loadSilenceMatches(m.deps, m.cfg.Silence)())
	m = next.(Model)
	require.Equal(t, "❯ — 构建机器人 — 0", silenceRow(m), "a rule that matches nothing reads as a zero")
}

func TestSilenceTab_AFailedWriteLeavesTheSessionAlone(t *testing.T) {
	m := silenceModel(t)
	// A directory where the file should be is a write the rename refuses.
	m.deps.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.Mkdir(m.deps.ConfigPath, 0o755))
	m = press(t, m, "a", "tab", "tab")
	m = typeText(t, m, "nightly")
	m = press(t, m, "enter")

	require.True(t, m.config.silence.form.open)
	require.NotEmpty(t, m.config.silence.form.err)
	require.Empty(t, m.cfg.Silence)
}

func TestSilenceTab_TheCaretSitsInTheFieldBeingTyped(t *testing.T) {
	m := silenceModel(t)
	m = press(t, m, "a", "tab", "tab")
	m = typeText(t, m, "nightly")
	v := m.View()
	require.NotNil(t, v.Cursor)
	row := []rune(strings.Split(ansi.Strip(v.Content), "\n")[v.Cursor.Position.Y])
	require.Contains(t, string(row), "contains")
	x := v.Cursor.Position.X
	require.Equal(t, "nightly", string(row[x-7:x]))

	m = press(t, m, "shift+tab", "enter")
	m = typeText(t, m, "平台")
	v = m.View()
	require.NotNil(t, v.Cursor)
	line := strings.Split(ansi.Strip(v.Content), "\n")[v.Cursor.Position.Y]
	require.Contains(t, line, "sender › 平台", "the picker's query line")
}
