package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// readAllApp is n chats each carrying one message Feishu still reports
// unseen, with the opener recorded instead of reaching macOS.
func readAllApp(t *testing.T, n int, openErr error) (*App, *bytes.Buffer, *[][]string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "larkim.db"))
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
	var walked [][]string
	a := &App{Out: &out, Err: &out, jsonOut: true, cfg: config.Config{DataDir: dir},
		openURL: func(targets []string, background bool) error {
			require.True(t, background, "the walk must leave the screen to whatever the reader is in")
			walked = append(walked, targets)
			return openErr
		}}
	return a, &out, &walked
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

func TestReadAllCmd_WalksTheClientOntoEveryChatThatWasWaiting(t *testing.T) {
	a, _, walked := readAllApp(t, 3, nil)

	out := runReadAll(t, a)

	require.Equal(t, [][]string{
		{"lark://applink.feishu.cn/client/chat/open?openChatId=oc_0&position=1"},
		{"lark://applink.feishu.cn/client/chat/open?openChatId=oc_1&position=1"},
		{"lark://applink.feishu.cn/client/chat/open?openChatId=oc_2&position=1"},
	}, *walked, "one applink per chat, each on its own, each landing on that chat's newest unread")
	require.Contains(t, out, `"messages": 3`)
	require.Contains(t, out, `"chats": 3`)
	require.Contains(t, out, `"failed": 0`)
}

func TestReadAllCmd_SettlesTheLocalHalfEvenWhenOpenRefuses(t *testing.T) {
	a, _, walked := readAllApp(t, 2, errors.New("no application knows how to open URL"))

	out := runReadAll(t, a)

	require.Len(t, *walked, 2, "the chat behind a refusal has a dot of its own")
	require.Contains(t, out, `"failed": 2`)

	st, err := store.Open(a.cfg.DBPath())
	require.NoError(t, err)
	defer st.Close()
	left, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	require.Empty(t, left, "the durable half landed before the best-effort half was tried")
}

func TestReadAllCmd_DryRunCountsAndWritesNothing(t *testing.T) {
	a, _, walked := readAllApp(t, 2, nil)

	out := runReadAll(t, a, "--dry-run")

	require.Empty(t, *walked)
	require.Contains(t, out, `"chats": 2`)
	require.Contains(t, out, `"messages": 0`)

	st, err := store.Open(a.cfg.DBPath())
	require.NoError(t, err)
	defer st.Close()
	left, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	require.Len(t, left, 2, "a look must not be a write")
}

func TestReadAllCmd_OnAReadStoreOpensNothing(t *testing.T) {
	a, _, walked := readAllApp(t, 0, nil)

	out := runReadAll(t, a)

	require.Empty(t, *walked)
	require.Contains(t, out, `"chats": 0`)
}
