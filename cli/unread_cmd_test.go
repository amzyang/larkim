package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// unreadApp is two chats with backlogs of different ages, one message in the
// older chat already read behind its anchor.
func unreadApp(t *testing.T, jsonOut bool) (*App, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	ctx := t.Context()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{
		{ChatID: "oc_platform", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_project", Name: "项目协作群", ChatMode: "group"},
	}, 1))
	say := func(id, chatID string, ms int64, text string) store.Message {
		return store.Message{MessageID: id, ChatID: chatID, MsgType: "text", SenderID: "ou_a",
			SenderType: "user", SenderName: "张三", ContentRaw: `{"text":"` + text + `"}`,
			Content: text, RenderedAt: 1, CreateMs: ms, UpdateMs: ms, MessagePosition: ms}
	}
	_, err = st.UpsertMessages(ctx, []store.Message{
		say("om_p0", "oc_platform", 50, "已经看过了"),
		say("om_p1", "oc_platform", 100, "接口什么时候好"),
		say("om_j1", "oc_project", 300, "发布推迟到周四"),
	}, 1)
	require.NoError(t, err)
	read, unread := true, false
	require.NoError(t, st.SetReadStatus(ctx, "om_p0", &read, 100, 0))
	require.NoError(t, st.SetReadStatus(ctx, "om_p1", &unread, 100, 0))
	require.NoError(t, st.SetReadStatus(ctx, "om_j1", &unread, 100, 0))
	require.NoError(t, st.Close())

	var out bytes.Buffer
	return &App{Out: &out, Err: &out, jsonOut: jsonOut, cfg: config.Config{DataDir: dir}}, &out
}

func runUnread(t *testing.T, a *App, args ...string) string {
	t.Helper()
	cmd := a.unreadCmd()
	cmd.SetOut(a.Out)
	cmd.SetErr(a.Err)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
	return a.Out.(*bytes.Buffer).String()
}

// The page is the TUI panel's, drawn without a cursor: the same rules, the
// same blocks, the same order.
func TestUnreadCmd_PrintsThePagePartedByChat(t *testing.T) {
	a, _ := unreadApp(t, false)

	out := runUnread(t, a)

	// The chat name is drawn brighter than the arms around it, so the rule only
	// reads as one run of dashes once the colours are off it.
	require.Contains(t, ansi.Strip(out), "─ 平台组 ─")
	require.Contains(t, ansi.Strip(out), "─ 项目协作群 ─")
	require.Less(t, strings.Index(out, "平台组"), strings.Index(out, "项目协作群"),
		"the chat that has waited longest opens the page")
	require.Contains(t, out, "接口什么时候好")
	require.Contains(t, out, "发布推迟到周四")
	require.NotContains(t, out, "已经看过了", "nothing from before the anchor")
	require.NotContains(t, out, "  \n", "no line is padded out to the width")
}

func TestUnreadCmd_JSONAnswersWithTheMessages(t *testing.T) {
	a, _ := unreadApp(t, true)

	out := runUnread(t, a)

	require.Contains(t, out, `"message_id": "om_p1"`)
	require.Contains(t, out, `"message_id": "om_j1"`)
	require.NotContains(t, out, "om_p0")
}

func TestUnreadCmd_SaysSoWhenNothingIsWaiting(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	require.NoError(t, st.Close())
	var out bytes.Buffer
	a := &App{Out: &out, Err: &out, cfg: config.Config{DataDir: dir}}

	require.Equal(t, "nothing waiting\n", runUnread(t, a))
}

func TestUnreadScreen_FallsBackWhenThereIsNoTerminalToAsk(t *testing.T) {
	sc := (&App{Out: &bytes.Buffer{}}).unreadScreen()

	require.Equal(t, unreadDefaultWidth, sc.Width)
	require.Equal(t, unreadDefaultHeight, sc.Height)
	require.False(t, sc.TTY, "a buffer takes no pictures")
}
