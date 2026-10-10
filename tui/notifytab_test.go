package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// notifyModel is the config panel on its Notifications tab, over two named
// chats and two people.
func notifyModel(t *testing.T) Model {
	t.Helper()
	m := configModel(t)
	m.chats = []store.Chat{{ChatID: "oc_quiet", Name: "平台组"}, {ChatID: "oc_team", Name: "项目协作群"}}
	m.contacts = []store.Contact{{OpenID: "ou_a", Name: "张三"}, {OpenID: "ou_b", Name: "李四"}}
	m = press(t, m, "tab", "tab")
	require.Equal(t, tabNotify, m.config.tab)
	return m
}

func notifyRow(m Model) string {
	return cursorLine(m.notifyLines(), m.config.notify.idx, m.config.notify.top)
}

func TestNotifyTab_AddsAWatchedPersonAndChatThroughThePicker(t *testing.T) {
	t.Parallel()
	m := notifyModel(t)
	require.Contains(t, ansi.Strip(m.renderConfig()), "nothing watched")

	m = press(t, m, "a")
	require.True(t, m.config.notify.pick.open)
	m = typeText(t, m, "李四")
	m = press(t, m, "enter")
	m = press(t, m, "a")
	m = typeText(t, m, "平台")
	m = press(t, m, "enter")

	want := config.Strings{"ou_b", "oc_quiet"}
	require.Equal(t, want, m.cfg.Notifications.Watch)
	require.Equal(t, want, configFile(t, m).Notifications.Watch)
	require.Equal(t, "notifications · 2 watch, 0 keywords · the daemon rereads it", m.notice)
	require.Contains(t, ansi.Strip(strings.Join(m.notifyLines(), "\n")), "李四")
	require.Contains(t, ansi.Strip(strings.Join(m.notifyLines(), "\n")), "平台组")
}

func TestNotifyTab_RefusesAnIDThatIsNeitherAUserNorAChat(t *testing.T) {
	t.Parallel()
	m := notifyModel(t)
	m = press(t, m, "a")
	m = typeText(t, m, "cli_c")
	m = press(t, m, "enter")
	require.Nil(t, m.cfg.Notifications.Watch)
	require.Contains(t, m.config.notify.err, "neither a user")
	require.Nil(t, configFile(t, m).Notifications.Watch, "nothing was written")
}

func TestNotifyTab_AddsAKeywordAndRefusesOneThatDoesNotCompile(t *testing.T) {
	t.Parallel()
	m := notifyModel(t)
	m = press(t, m, "A")
	require.True(t, m.config.notify.keyword.Focused())
	m = typeText(t, m, "上线|故障")
	m = press(t, m, "enter")
	require.Equal(t, config.Strings{"上线|故障"}, configFile(t, m).Notifications.Keywords)

	m = press(t, m, "A")
	m = typeText(t, m, "(")
	m = press(t, m, "enter")
	require.Contains(t, m.config.notify.err, "notifications.keywords")
	require.True(t, m.config.notify.keyword.Focused(), "the pattern stays to be fixed")
	require.Equal(t, config.Strings{"上线|故障"}, m.cfg.Notifications.Keywords)
}

func TestNotifyTab_DeletesAfterAYes(t *testing.T) {
	t.Parallel()
	m := notifyModel(t)
	m.cfg.Notifications = config.Notifications{Watch: config.Strings{"ou_a"}, Keywords: config.Strings{"故障"}}
	m = press(t, m, "j")
	require.Contains(t, notifyRow(m), "故障")
	m = press(t, m, "d", "n")
	require.Equal(t, config.Strings{"故障"}, m.cfg.Notifications.Keywords, "no keeps it")
	m = press(t, m, "d", "y")
	require.Nil(t, m.cfg.Notifications.Keywords)
	require.Equal(t, config.Strings{"ou_a"}, configFile(t, m).Notifications.Watch, "the other list stays")
}

func TestNotifyTab_TellsTheBannerSideRunningHere(t *testing.T) {
	t.Parallel()
	m := notifyModel(t)
	var got config.Notifications
	m.deps.SetNotifications = func(n config.Notifications) { got = n }
	m = press(t, m, "a")
	m = typeText(t, m, "张三")
	m = press(t, m, "enter")
	require.Equal(t, config.Strings{"ou_a"}, got.Watch)
	require.Contains(t, m.notice, "takes effect now")
}

func TestNotifyTab_TheGeneralRowsOpenIt(t *testing.T) {
	t.Parallel()
	m := configModel(t).closeConfig().openConfig("notifications.keywords")
	require.Equal(t, "❯ notifications.keywords 0 keywords", configRow(m))
	m = press(t, m, "enter")
	require.Equal(t, tabNotify, m.config.tab)
	m = configModel(t).closeConfig().openConfig("notifications.watch")
	m = press(t, m, "&")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "Notifications tab")
}

func TestNotifyTab_ShowsAnUnresolvedIDInRed(t *testing.T) {
	t.Parallel()
	m := notifyModel(t)
	m.cfg.Notifications.Watch = config.Strings{"ou_gone"}
	require.Contains(t, notifyRow(m), "ou_gone")
	require.NotContains(t, notifyRow(m), "张三")
}
