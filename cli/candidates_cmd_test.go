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

// candidatesApp is one chat holding one message from someone else, which is
// the minimum a `candidates put` needs: a mid whose chat the store can name.
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

func runCandidates(t *testing.T, a *App, args ...string) {
	t.Helper()
	cmd := a.candidatesCmd()
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
}

func TestCandidatesPut_ResolvesTheChatFromTheMessage(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()

	runCandidates(t, a, "put", "om_ask", "--draft", "缓冲话术", "--draft", "放行话术")

	rows, err := st.ChatCandidates(t.Context(), "oc_quiet", "")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "om_ask", rows[0].Mid)
	require.Equal(t, "缓冲话术", rows[0].Text)
}

func TestCandidatesPut_RefusesAMessageTheStoreHasNeverSeen(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()

	cmd := a.candidatesCmd()
	cmd.SetArgs([]string{"put", "om_elsewhere", "--draft", "x"})
	err := cmd.Execute()
	require.ErrorIs(t, err, store.ErrNotFound,
		"a row without a chat would badge nothing and scope the picker to nowhere")
}

func TestCandidatesPut_RequiresAtLeastOneNonBlankDraft(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()

	for _, args := range [][]string{
		{"put", "om_ask"},
		{"put", "om_ask", "--draft", "  "},
		{"put", "om_ask", "--draft", "x", "--format", "rich"},
	} {
		cmd := a.candidatesCmd()
		cmd.SetArgs(args)
		require.Error(t, cmd.Execute(), "args: %v", args)
	}
}

func TestCandidatesClear_DropsTheMirroredDrafts(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()
	ctx := t.Context()
	require.NoError(t, st.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a"}, "text", 1))

	runCandidates(t, a, "clear", "om_ask")

	rows, err := st.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestCandidatesList_AnswersForEveryChatAndForOne(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()
	ctx := t.Context()
	require.NoError(t, st.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"first line\nsecond line"}, "markdown", 1))

	runCandidates(t, a, "list")
	runCandidates(t, a, "list", "--chat", "平台组")
}
