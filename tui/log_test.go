package tui

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestNew_DefaultsTheLogger(t *testing.T) {
	m := New(Deps{})
	require.NotNil(t, m.deps.Log, "every log call in the TUI dereferences this")
	m.deps.Log.Warn("discarded")
}

func TestMarkChatRead_ReportsAFailureTheBadgeCannotShow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	require.NoError(t, st.Close())
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	require.Nil(t, markChatRead(st, log, "oc_quiet")())

	require.Contains(t, buf.String(), "mark chat read")
	require.Contains(t, buf.String(), "oc_quiet")
}

// logDeps are the deps a hand-over to the desktop needs, with the opener
// failing the way macOS does and the log captured.
func logDeps(t *testing.T, openErr error) (Deps, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return Deps{
		Log: slog.New(slog.NewTextHandler(&buf, nil)),
		OpenURL: func([]string, bool) error {
			return openErr
		},
	}, &buf
}

func TestClearFeishuBadge_LogsAFailureInsteadOfTakingTheNoticeBar(t *testing.T) {
	d, buf := logDeps(t, errors.New("no application knows how to open URL"))

	require.Nil(t, clearFeishuBadge(d, "oc_quiet")(), "a chat switch must not be reported as the reader's error")

	require.Contains(t, buf.String(), "clear feishu badge")
	require.Contains(t, buf.String(), "oc_quiet")
	require.Contains(t, buf.String(), "no application knows how to open URL")
}

func TestOpenInFeishu_LogsTheChatTheNoticeBarCannotName(t *testing.T) {
	d, buf := logDeps(t, errors.New("boom"))

	require.Equal(t, errMsg{errors.New("boom")}, openInFeishu(d, "oc_quiet", 227)(),
		"the keypress asked for this, so it is still reported on screen")

	require.Contains(t, buf.String(), "open in feishu")
	require.Contains(t, buf.String(), "oc_quiet")
	require.Contains(t, buf.String(), "227")
}

func TestOpenZone_LogsTheTargetsTheNoticeBarCannotHold(t *testing.T) {
	d, buf := logDeps(t, errors.New("boom"))
	z := clickZone{urls: []string{"/data/resources/img_a.png", "/data/resources/img_b.png"}, note: "opened"}

	require.Equal(t, errMsg{errors.New("boom")}, openZone(d, z)())

	require.Contains(t, buf.String(), "open zone")
	require.Contains(t, buf.String(), "img_a.png")
	require.Contains(t, buf.String(), "img_b.png")
}

func TestOpenURL_CarriesWhatOpenRefusedOn(t *testing.T) {
	err := openURL(slog.New(slog.DiscardHandler), []string{filepath.Join(t.TempDir(), "nothing-here.txt")}, true)

	require.ErrorContains(t, err, "does not exist",
		"exec drops stderr, which is the only place open says why")
}

func TestOpenURL_LogsTheArgvItBuilt(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	openURL(log, []string{"lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet"}, true)

	require.Contains(t, buf.String(), `open -g 'lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet'`,
		"-g is this function's own decision, so no caller can log it")
}

func TestNew_GivesTheDefaultOpenerTheLog(t *testing.T) {
	var buf bytes.Buffer
	m := New(Deps{Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))})

	m.deps.OpenURL([]string{filepath.Join(t.TempDir(), "nothing-here.txt")}, false)

	require.Contains(t, buf.String(), "nothing-here.txt", "the opener New installs must not log into the void")
}
