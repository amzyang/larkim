package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// completionFixture is an App whose database holds two chats, three contacts
// and three messages, which is everything the store-backed completions read.
func completionFixture(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{
		{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group", RawJSON: "{}"},
		{ChatID: "oc_elsewhere", Name: "项目协作群", ChatMode: "group", RawJSON: "{}"},
	}, 1))
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_a", Name: "张三", Email: "zhangsan@example.com", RawJSON: "{}"},
		{OpenID: "ou_b", Name: "李四", Email: "lisi@example.com", RawJSON: "{}"},
		{OpenID: "ou_c", Name: "构建机器人", IsBot: true, RawJSON: "{}"},
	}, 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_one", ChatID: "oc_quiet", CreateMs: 10, MessagePosition: 1, RawJSON: "{}"},
		{MessageID: "om_two", ChatID: "oc_quiet", CreateMs: 20, MessagePosition: 2, RawJSON: "{}"},
		{MessageID: "om_far", ChatID: "oc_elsewhere", CreateMs: 30, MessagePosition: 1, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	return &App{cfg: config.Config{DataDir: dir}}
}

// values drops the descriptions, which are what a shell shows beside a
// candidate rather than what it inserts.
func values(completions []cobra.Completion) []string {
	out := make([]string, len(completions))
	for i, c := range completions {
		out[i], _, _ = strings.Cut(c, "\t")
	}
	return out
}

func TestCompleteChatRef_OffersNamesAndIDs(t *testing.T) {
	a := completionFixture(t)

	got, directive := a.completeChatRef(nil, nil, "平台")
	require.Equal(t, []string{"平台组"}, values(got))
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)

	got, _ = a.completeChatRef(nil, nil, "oc_")
	require.ElementsMatch(t, []string{"oc_quiet", "oc_elsewhere"}, values(got))

	got, _ = a.completeChatRef(nil, nil, "")
	require.Len(t, got, 2, "an empty word offers every chat")
}

func TestCompleteChatRef_CarriesTheIDAsTheDescription(t *testing.T) {
	a := completionFixture(t)
	got, _ := a.completeChatRef(nil, nil, "平台组")
	require.Equal(t, []cobra.Completion{"平台组\toc_quiet"}, got,
		"the name is inserted and the id is what tells two same-named chats apart")
}

func TestCompleteContactRef_OffersNameEmailAndOpenID(t *testing.T) {
	a := completionFixture(t)

	got, _ := a.completeContactRef(nil, nil, "张")
	require.Equal(t, []string{"张三"}, values(got))

	got, _ = a.completeContactRef(nil, nil, "lisi@")
	require.Equal(t, []string{"lisi@example.com"}, values(got))

	got, _ = a.completeContactRef(nil, nil, "ou_")
	require.ElementsMatch(t, []string{"ou_a", "ou_b", "ou_c"}, values(got))
}

func TestCompleteSenderID_OffersOnlyOpenIDs(t *testing.T) {
	a := completionFixture(t)
	got, _ := a.completeSenderID(nil, nil, "")
	require.ElementsMatch(t, []string{"ou_a", "ou_b", "ou_c"}, values(got),
		"--sender takes an open_id, so a name would be a value it rejects")

	got, _ = a.completeSenderID(nil, nil, "张")
	require.Empty(t, got)
}

func TestCompleteMessageID_NewestFirst(t *testing.T) {
	a := completionFixture(t)
	got, directive := a.completeMessageID(&cobra.Command{}, nil, "")
	require.Equal(t, []string{"om_far", "om_two", "om_one"}, values(got))
	require.NotZero(t, directive&cobra.ShellCompDirectiveKeepOrder,
		"the shell must not re-sort opaque ids out of recency order")
}

func TestCompleteMessageID_NarrowsToTheChatFlag(t *testing.T) {
	a := completionFixture(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("chat", "", "")
	require.NoError(t, cmd.Flags().Set("chat", "平台组"))

	got, _ := a.completeMessageID(cmd, nil, "")
	require.Equal(t, []string{"om_two", "om_one"}, values(got),
		"a name --chat resolves the same way the command itself resolves it")

	require.NoError(t, cmd.Flags().Set("chat", "no such chat"))
	got, _ = a.completeMessageID(cmd, nil, "")
	require.Len(t, got, 3, "a --chat that resolves to nothing narrows nothing")
}

func TestCompleteFromStore_AnswersNothingBeforeTheFirstSync(t *testing.T) {
	a := &App{cfg: config.Config{DataDir: t.TempDir()}}
	got, directive := a.completeChatRef(nil, nil, "")
	require.Empty(t, got)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.NoFileExists(t, a.cfg.DBPath(), "pressing Tab must not be what creates the database")
}

// directiveLine is how cobra spells a directive on the last line of a
// completion answer.
func directiveLine(d cobra.ShellCompDirective) string { return ":" + strconv.Itoa(int(d)) }

// completeArgs drives the hidden command a shell calls on Tab and returns the
// lines it printed, candidates first and the directive last.
func completeArgs(t *testing.T, args ...string) []string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	// Separate buffers: the candidates and the directive go to stdout, which
	// is all a shell reads, and the trailer cobra adds goes to stderr.
	var out, errOut bytes.Buffer
	root := New("test", "")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))
	require.NoError(t, root.Execute())
	return strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
}

func TestCompletion_NoCommandOffersFilesByDefault(t *testing.T) {
	for _, args := range [][]string{
		{"status", ""},
		{"db", "path", ""},
		{"messages", "list", "--limit", ""},
		{"silence", ""},
		{"--sentry-dsn", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lines := completeArgs(t, args...)
			require.Equal(t, directiveLine(cobra.ShellCompDirectiveNoFileComp), lines[len(lines)-1],
				"a listing of the working directory answers none of these")
		})
	}
}

func TestCompletion_ConfigFlagStillOffersFiles(t *testing.T) {
	lines := completeArgs(t, "--config", "")
	require.Equal(t, directiveLine(cobra.ShellCompDirectiveFilterFileExt), lines[len(lines)-1])
	require.Equal(t, []string{"yaml", "yml"}, lines[:len(lines)-1])
}

func TestCompletion_ImageAndFileFlagsStillOfferFiles(t *testing.T) {
	for _, flag := range []string{"--image", "--file"} {
		lines := completeArgs(t, "send", flag, "")
		require.Equal(t, directiveLine(cobra.ShellCompDirectiveDefault), lines[len(lines)-1],
			flag+" takes a path, which the shell knows better than larkim does")
	}
}

func TestCompletion_RootStillOffersItsSubcommands(t *testing.T) {
	lines := completeArgs(t, "")
	require.Contains(t, lines, "send\tSend a text, markdown, image or file message to a user or a chat")
}

func TestIsCompletion_CoversTheScriptGenerators(t *testing.T) {
	root := New("test", "")
	// The generators exist only once cobra has been asked to run something.
	root.SetArgs([]string{"completion", "--help"})
	root.SetOut(&bytes.Buffer{})
	require.NoError(t, root.Execute())

	generator, _, err := root.Find([]string{"completion", "fish"})
	require.NoError(t, err)
	require.True(t, isCompletion(generator),
		"Homebrew runs this at install time; it must not write a log or arm the reporter")
	require.False(t, isCompletion(root))
}

func TestCompletionGenerator_WritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	root := New("test", "")
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"completion", "fish"})
	require.NoError(t, root.Execute())
	require.Contains(t, out.String(), "larkim")

	left, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, left, "generating a completion script must leave no data dir behind")
}
