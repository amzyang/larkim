package tui

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

func TestNew_DefaultsTheLogger(t *testing.T) {
	t.Parallel()
	m := New(Deps{})
	require.NotNil(t, m.deps.Log, "every log call in the TUI dereferences this")
	m.deps.Log.Warn("discarded")
}

func TestMarkChatRead_ReportsAFailureTheBadgeCannotShow(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
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
	log := slog.New(slog.NewTextHandler(&buf, nil))
	open := func([]string, bool) error { return openErr }
	return Deps{
		Log:        log,
		OpenURL:    open,
		ClearBadge: markread.New(config.Default().MarkRead, log, nil, open),
	}, &buf
}

func TestFireBadgeClear_LogsAFailureInsteadOfTakingTheNoticeBar(t *testing.T) {
	t.Parallel()
	d, buf := logDeps(t, errors.New("no application knows how to open URL"))

	msg := fireBadgeClear(d, store.ChatUnread{ChatID: "oc_quiet"}, 3)().(applinkFiredMsg)

	require.Error(t, msg.err, "the queue counts it; a chat switch is not reported as the reader's error")
	require.Contains(t, buf.String(), "clear feishu badge")
	require.Contains(t, buf.String(), "oc_quiet")
	require.Contains(t, buf.String(), "no application knows how to open URL")
}

func TestOpenInFeishu_LogsTheChatTheNoticeBarCannotName(t *testing.T) {
	t.Parallel()
	d, buf := logDeps(t, errors.New("boom"))

	require.Equal(t, errMsg{errors.New("boom")}, openInFeishu(d, "oc_quiet", "", 227)(),
		"the keypress asked for this, so it is still reported on screen")

	require.Contains(t, buf.String(), "open in feishu")
	require.Contains(t, buf.String(), "oc_quiet")
	require.Contains(t, buf.String(), "227")
}

func TestOpenZone_LogsTheTargetsTheNoticeBarCannotHold(t *testing.T) {
	t.Parallel()
	d, buf := logDeps(t, errors.New("boom"))
	z := clickZone{urls: []string{"/data/resources/img_a.png", "/data/resources/img_b.png"}, note: "opened"}

	require.Equal(t, errMsg{errors.New("boom")}, openZone(d, z)())

	require.Contains(t, buf.String(), "open zone")
	require.Contains(t, buf.String(), "img_a.png")
	require.Contains(t, buf.String(), "img_b.png")
}

func TestOpenURL_CarriesWhatOpenRefusedOn(t *testing.T) {
	t.Parallel()
	err := applink.Open(slog.New(slog.DiscardHandler), []string{filepath.Join(t.TempDir(), "nothing-here.txt")}, true)

	require.ErrorContains(t, err, "does not exist",
		"exec drops stderr, which is the only place open says why")
}

func TestOpenURL_LogsTheArgvItBuilt(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// A file that is not there: open refuses it after the line is logged, where
	// a lark:// link would move the Feishu client of whoever runs the test.
	target := filepath.Join(t.TempDir(), "open?openChatId=oc_quiet")
	applink.Open(log, []string{target}, true)

	require.Contains(t, buf.String(), `open -g '`+target+`'`,
		"-g is this function's own decision, so no caller can log it")
}

func TestNew_GivesTheDefaultOpenerTheLog(t *testing.T) {
	saved := openApplink
	openApplink = applink.Open
	t.Cleanup(func() { openApplink = saved })
	var buf bytes.Buffer
	m := New(Deps{Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))})

	m.deps.OpenURL([]string{filepath.Join(t.TempDir(), "nothing-here.txt")}, false)

	require.Contains(t, buf.String(), "nothing-here.txt", "the opener New installs must not log into the void")
}
