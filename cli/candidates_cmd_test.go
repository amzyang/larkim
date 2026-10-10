package cli

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// candidatesApp is one chat holding one message from someone else.
func candidatesApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := storetest.Open(t, filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	ctx := t.Context()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{
		{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"},
	}, 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_ask", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_a",
			SenderType: "user", SenderName: "张三", ContentRaw: `{"text":"接口什么时候好"}`,
			Content: "接口什么时候好", CreateMs: 100, MessagePosition: 1, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	var out bytes.Buffer
	return &App{Out: &out, Err: &out, cfg: config.Config{DataDir: dir}}, st
}

func TestCandidatesList_AnswersForEveryChatAndForOne(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()
	ctx := t.Context()
	require.NoError(t, st.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"first line\nsecond line"}, nil, "markdown", 1))

	for _, args := range [][]string{{"list"}, {"list", "--chat", "平台组"}} {
		a.Out.(*bytes.Buffer).Reset()
		cmd := a.candidatesCmd()
		cmd.SetArgs(args)
		require.NoError(t, cmd.Execute())
		require.Contains(t, a.Out.(*bytes.Buffer).String(), "first line …")
	}
}

func TestCandidatesList_NamesAReactionRow(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()
	require.NoError(t, st.PutCandidates(t.Context(), "om_ask", "oc_quiet", nil, []string{"THUMBSUP"}, "text", 1))

	cmd := a.candidatesCmd()
	cmd.SetArgs([]string{"list"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, a.Out.(*bytes.Buffer).String(), "[Like]")
}
