package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

func readAllApp(t *testing.T, n int, clearErr error) (*App, *[]store.ChatUnread, *error) {
	t.Helper()
	dir := t.TempDir()
	st, err := storetest.Open(t, filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	ctx := t.Context()
	var msgs []store.Message
	for i := range n {
		msgs = append(msgs, store.Message{MessageID: "om_" + strconv.Itoa(i), ChatID: "oc_" + strconv.Itoa(i),
			MsgType: "text", SenderID: "ou_x", CreateMs: int64(100 + i), MessagePosition: 1})
	}
	if n > 0 {
		_, err = st.UpsertMessages(ctx, msgs, 1)
		require.NoError(t, err)
	}
	unread := false
	for _, x := range msgs {
		require.NoError(t, st.SetReadStatus(ctx, x.MessageID, &unread, 100, 0))
	}
	require.NoError(t, st.Close())

	var out bytes.Buffer
	var cleared []store.ChatUnread
	refuse := clearErr
	a := &App{Out: &out, Err: &out, jsonOut: true, cfg: config.Config{DataDir: dir},
		clearBadge: func(_ context.Context, c store.ChatUnread) error {
			cleared = append(cleared, c)
			return refuse
		}}
	return a, &cleared, &refuse
}

func runReadAll(t *testing.T, a *App, args ...string) string {
	t.Helper()
	cmd := a.readAllCmd()
	cmd.SetOut(a.Out)
	cmd.SetErr(a.Err)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
	return a.Out.(*bytes.Buffer).String()
}

func TestReadAllCmd_ClearsEveryChatThatWasWaiting(t *testing.T) {
	a, cleared, _ := readAllApp(t, 3, nil)

	out := runReadAll(t, a)

	require.Equal(t, []store.ChatUnread{
		{ChatID: "oc_0", Position: 1},
		{ChatID: "oc_1", Position: 1},
		{ChatID: "oc_2", Position: 1},
	}, *cleared)
	require.Contains(t, out, `"messages": 3`)
	require.Contains(t, out, `"chats": 3`)
	require.Contains(t, out, `"failed": 0`)
	require.NotContains(t, out, `"mode"`)
}

func TestReadAllCmd_LeavesRemoteUnreadWhenTheClearFails(t *testing.T) {
	a, cleared, _ := readAllApp(t, 2, errors.New("session cookie missing"))

	out := runReadAll(t, a)

	require.Len(t, *cleared, 2)
	require.Contains(t, out, `"messages": 0`)
	require.Contains(t, out, `"failed": 2`)

	st, err := storetest.Open(t, a.cfg.DBPath())
	require.NoError(t, err)
	defer st.Close()
	for _, id := range []string{"om_0", "om_1"} {
		m, err := st.GetMessage(t.Context(), id)
		require.NoError(t, err)
		require.False(t, *m.IsReadRemote)
	}
	left, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	require.Len(t, left, 2)
}

func TestReadAllCmd_ClearsAgainWhatTheLastPassFailedToClear(t *testing.T) {
	a, cleared, refuse := readAllApp(t, 2, errors.New("session cookie missing"))
	runReadAll(t, a)
	require.Len(t, *cleared, 2)
	*refuse, *cleared = nil, nil

	out := runReadAll(t, a)

	require.Len(t, *cleared, 2,
		"no receipt said the dots came down, so the chats are still the client's and the pass is repeatable")
	require.Contains(t, out, `"chats": 2`)
	require.Contains(t, out, `"failed": 0`)
	require.Contains(t, out, `"messages": 2`)
}

func TestReadAllCmd_DryRunCountsAndWritesNothing(t *testing.T) {
	a, cleared, _ := readAllApp(t, 2, nil)

	out := runReadAll(t, a, "--dry-run")

	require.Empty(t, *cleared)
	require.Contains(t, out, `"chats": 2`)
	require.Contains(t, out, `"messages": 0`)
	require.NotContains(t, out, `"mode"`)

	st, err := storetest.Open(t, a.cfg.DBPath())
	require.NoError(t, err)
	defer st.Close()
	left, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	require.Len(t, left, 2, "a look must not be a write")
}

func TestReadAllCmd_OnAReadStoreClearsNothing(t *testing.T) {
	a, cleared, _ := readAllApp(t, 0, nil)

	out := runReadAll(t, a)

	require.Empty(t, *cleared)
	require.Contains(t, out, `"chats": 0`)
}

func TestReadAllCmd_SaysClearedInText(t *testing.T) {
	a, _, _ := readAllApp(t, 2, nil)
	a.jsonOut = false
	var out bytes.Buffer
	a.Out, a.Err = &out, &out
	a.clearBadge = func(_ context.Context, c store.ChatUnread) error {
		if c.ChatID == "oc_1" {
			return errors.New("session cookie missing")
		}
		return nil
	}
	cmd := a.readAllCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	require.NoError(t, cmd.Execute())
	text := out.String()
	require.Contains(t, text, "1 messages read, 1 chats cleared in Feishu")
	require.Contains(t, text, "1 chats kept their red dot: not matched to the web client, or refused")
}
