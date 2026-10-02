package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// readAllApp is n chats each carrying one message Feishu still reports
// unseen, with the opener recorded instead of reaching macOS.
func readAllApp(t *testing.T, n int, openErr error) (*App, *[][]string, *error) {
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
	var walked [][]string
	refuse := openErr
	// A pace of 1ms rather than the configured second: what these cases are
	// about is which chats get walked, not how far apart.
	a := &App{Out: &out, Err: &out, jsonOut: true, cfg: config.Config{DataDir: dir, ApplinkPaceMS: 1},
		openURL: func(targets []string, background bool) error {
			require.True(t, background, "the walk must leave the screen to whatever the reader is in")
			walked = append(walked, targets)
			return refuse
		}}
	return a, &walked, &refuse
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
	a, walked, _ := readAllApp(t, 3, nil)

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
	a, walked, _ := readAllApp(t, 2, errors.New("no application knows how to open URL"))

	out := runReadAll(t, a)

	require.Len(t, *walked, 2, "the chat behind a refusal has a dot of its own")
	require.Contains(t, out, `"failed": 2`)

	st, err := storetest.Open(t, a.cfg.DBPath())
	require.NoError(t, err)
	defer st.Close()
	chats, err := st.ListChats(t.Context(), store.ChatQuery{})
	require.NoError(t, err)
	for _, c := range chats {
		require.Zero(t, c.UnreadCount, "the durable half landed before the best-effort half was tried")
	}
	left, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	require.Len(t, left, 2, "and the refused chats are still the client's, so the next pass finds them")
}

func TestReadAllCmd_WalksAgainWhatTheLastPassFailedToClear(t *testing.T) {
	a, walked, refuse := readAllApp(t, 2, errors.New("no application knows how to open URL"))
	runReadAll(t, a)
	require.Len(t, *walked, 2)
	*refuse, *walked = nil, nil

	out := runReadAll(t, a)

	require.Len(t, *walked, 2,
		"no receipt said the dots came down, so the chats are still the client's and the pass is repeatable")
	require.Contains(t, out, `"chats": 2`)
	require.Contains(t, out, `"failed": 0`)
	require.Contains(t, out, `"messages": 0`, "the local half was already settled by the first pass")
}

func TestReadAllCmd_DryRunCountsAndWritesNothing(t *testing.T) {
	a, walked, _ := readAllApp(t, 2, nil)

	out := runReadAll(t, a, "--dry-run")

	require.Empty(t, *walked)
	require.Contains(t, out, `"chats": 2`)
	require.Contains(t, out, `"messages": 0`)

	st, err := storetest.Open(t, a.cfg.DBPath())
	require.NoError(t, err)
	defer st.Close()
	left, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	require.Len(t, left, 2, "a look must not be a write")
}

func TestReadAllCmd_OnAReadStoreOpensNothing(t *testing.T) {
	a, walked, _ := readAllApp(t, 0, nil)

	out := runReadAll(t, a)

	require.Empty(t, *walked)
	require.Contains(t, out, `"chats": 0`)
}
