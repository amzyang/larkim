package cli

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/sync"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSendCmd_RefusesAnythingButOneBody(t *testing.T) {
	require.ErrorContains(t, outgoingFlags{}.check(), "exactly one")
	require.ErrorContains(t, outgoingFlags{text: "hi", markdown: "## hi"}.check(), "exactly one")
	require.ErrorContains(t, outgoingFlags{markdown: "## hi", image: "a.png"}.check(), "exactly one")
	require.NoError(t, outgoingFlags{text: "hi"}.check())
	require.NoError(t, outgoingFlags{markdown: "## hi"}.check())
	require.NoError(t, outgoingFlags{image: "a.png"}.check())
	require.NoError(t, outgoingFlags{file: "a.pdf"}.check())
	require.ErrorContains(t, outgoingFlags{image: "a.png", file: "a.pdf"}.check(), "exactly one")
}

func TestSendCmd_FileFlagUploadsFirst(t *testing.T) {
	f := larkcli.NewFake()
	path := filepath.Join(t.TempDir(), "发布说明.pdf")
	require.NoError(t, os.WriteFile(path, []byte("pdf"), 0o644))

	msg, err := outgoingFlags{file: path}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Equal(t, "file_fake_1", msg.FileKey)
	require.Equal(t, []string{path}, f.Uploads)
}

func TestSendCmd_FileFlagPassesAKeyThrough(t *testing.T) {
	f := larkcli.NewFake()
	msg, err := outgoingFlags{file: "file_v3_report"}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Equal(t, "file_v3_report", msg.FileKey)
	require.Empty(t, f.Uploads, "a key Feishu already holds needs no upload")
}

func TestSendCmd_TextIsStillVerbatim(t *testing.T) {
	// A script piping a changelog into --text must not start sending posts
	// just because the text happens to look like markdown.
	msg, err := outgoingFlags{text: "## 发布说明\n- a"}.outgoing(t.Context(), larkcli.NewFake(), noFetch(t))
	require.NoError(t, err)
	require.Equal(t, "## 发布说明\n- a", msg.Text)
	require.Empty(t, msg.Markdown)
}

func TestSendCmd_MarkdownFlagSendsAPost(t *testing.T) {
	msg, err := outgoingFlags{markdown: "## 发布说明"}.outgoing(t.Context(), larkcli.NewFake(), noFetch(t))
	require.NoError(t, err)
	require.Equal(t, "## 发布说明", msg.Markdown)
	require.Empty(t, msg.Text)
}

func TestSendCmd_ImageFlagUploadsFirst(t *testing.T) {
	f := larkcli.NewFake()
	path := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(path, []byte("png"), 0o644))

	msg, err := outgoingFlags{image: path}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Equal(t, "img_fake_1", msg.ImageKey)
	require.Equal(t, []string{path}, f.Uploads)
}

func TestSendCmd_ImageFlagPassesAKeyThrough(t *testing.T) {
	f := larkcli.NewFake()
	msg, err := outgoingFlags{image: "img_v3_shot"}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Equal(t, "img_v3_shot", msg.ImageKey)
	require.Empty(t, f.Uploads, "a key Feishu already holds needs no upload")
}

func TestExpandPath_ResolvesTildeAndRelativePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got, err := expandPath("~/Desktop/shot.png")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "Desktop", "shot.png"), got)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	got, err = expandPath("./a.png")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(cwd, "a.png"), got)
}

func TestSendCmd_FlagValidationRunsBeforeAnythingElse(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir, "send", "--chat", "oc_quiet")
	require.ErrorContains(t, err, "exactly one of --text, --markdown, --image or --file")

	_, err = run(t, dir, "send", "--chat", "oc_quiet", "--to", "ou_a", "--text", "hi")
	require.ErrorContains(t, err, "exactly one of --to or --chat")

	_, err = run(t, dir, "reply", "om_elsewhere", "--text", "hi", "--markdown", "## hi")
	require.ErrorContains(t, err, "exactly one of --text, --markdown, --image or --file")
}

func TestIdempotencyKey_KeepsTheOneTheCallerChose(t *testing.T) {
	require.Equal(t, "om_1-d-3f2a", idempotencyKey("om_1-d-3f2a"))
	require.Equal(t, "om_1-d-3f2a", idempotencyKey("  om_1-d-3f2a  "))
}

func TestIdempotencyKey_MintsADistinctOneWhenNobodyChose(t *testing.T) {
	// Two deliberate sends of the same text must both arrive, so the default
	// cannot be a constant.
	require.NotEqual(t, idempotencyKey(""), idempotencyKey(" "))
	require.NotEmpty(t, idempotencyKey(""))
}

func TestSendAndReply_BothTakeAnIdempotencyKey(t *testing.T) {
	a := &App{}
	for _, cmd := range []*cobra.Command{a.sendCmd(), a.replyCmd()} {
		require.NotNil(t, cmd.Flags().Lookup("idempotency-key"), cmd.Name())
	}
}

func TestOutgoingFlags_LeavesAnInlineBodyAlone(t *testing.T) {
	o := outgoingFlags{text: "看 @张三 收一下"}
	require.NoError(t, o.resolve(nil))
	require.Equal(t, "看 @张三 收一下", o.text)
}

func TestOutgoingFlags_ReadsABodyFromAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("## 发布说明\n\n- 修了 A\n"), 0o644))

	o := outgoingFlags{markdown: "@" + path}
	require.NoError(t, o.resolve(nil))
	require.Equal(t, "## 发布说明\n\n- 修了 A", o.markdown,
		"the newline a file ends with is punctuation, not a trailing blank line in the message")
}

func TestOutgoingFlags_ReadsABodyFromStdin(t *testing.T) {
	o := outgoingFlags{text: "-"}
	require.NoError(t, o.resolve(strings.NewReader("收到\n")))
	require.Equal(t, "收到", o.text)
}

func TestOutgoingFlags_DoubleAtIsALiteralAt(t *testing.T) {
	o := outgoingFlags{text: "@@张三 看一下"}
	require.NoError(t, o.resolve(nil))
	require.Equal(t, "@张三 看一下", o.text)
}

func TestOutgoingFlags_WhatASourceHoldsIsNotReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at.txt")
	require.NoError(t, os.WriteFile(path, []byte("@./somewhere-else"), 0o644))

	o := outgoingFlags{text: "@" + path}
	require.NoError(t, o.resolve(nil))
	require.Equal(t, "@./somewhere-else", o.text, "a source holds a body, not another source")

	o = outgoingFlags{markdown: "-"}
	require.NoError(t, o.resolve(strings.NewReader("@@still-two")))
	require.Equal(t, "@@still-two", o.markdown)
}

func TestOutgoingFlags_StripsTheBOMAFileCarries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bom.txt")
	require.NoError(t, os.WriteFile(path, []byte("\ufeff收到"), 0o644))

	o := outgoingFlags{text: "@" + path}
	require.NoError(t, o.resolve(nil))
	require.Equal(t, "收到", o.text, "a BOM would ride along as an invisible first character")
}

func TestOutgoingFlags_MissingFileKeepsTheCause(t *testing.T) {
	o := outgoingFlags{text: "@" + filepath.Join(t.TempDir(), "nowhere.txt")}

	err := o.resolve(nil)

	require.ErrorIs(t, err, fs.ErrNotExist)
	require.ErrorContains(t, err, "--text")
}

func TestOutgoingFlags_RefusesASourceHoldingNoMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blank.md")
	require.NoError(t, os.WriteFile(path, []byte("\n\n"), 0o644))

	require.ErrorContains(t, (&outgoingFlags{markdown: "@" + path}).resolve(nil), "holds no message")
	require.ErrorContains(t, (&outgoingFlags{text: "-"}).resolve(strings.NewReader("")), "holds no message")
	require.ErrorContains(t, (&outgoingFlags{text: "@"}).resolve(nil), "no file path after @")
}

func TestOutgoingFlags_PathFlagsTakeNoSource(t *testing.T) {
	// --image and --file already name a path, so @ in front of one has no
	// second reading to pick from and stays part of the path.
	o := outgoingFlags{image: "@shot.png"}
	require.NoError(t, o.resolve(nil))
	require.Equal(t, "@shot.png", o.image)
}

func TestSendCmd_MarkdownFromStdinReachesFeishu(t *testing.T) {
	a, f := bodySourceApp(t, "## 发布说明\n\n- 修了 A\n")

	runBodyCmd(t, a, a.sendCmd(), "--chat", "oc_quiet", "--markdown", "-")

	require.Len(t, f.Sent, 1)
	require.Equal(t, "## 发布说明\n\n- 修了 A", f.Sent[0].Markdown)
}

func TestReplyCmd_TextFromAFileReachesFeishu(t *testing.T) {
	a, f := bodySourceApp(t, "")
	f.Messages["om_elsewhere"] = larkcli.RawMessage{MessageID: "om_elsewhere", ChatID: "oc_quiet"}
	path := filepath.Join(t.TempDir(), "reply.txt")
	require.NoError(t, os.WriteFile(path, []byte("收到\n"), 0o644))

	runBodyCmd(t, a, a.replyCmd(), "om_elsewhere", "--text", "@"+path)

	require.Len(t, f.Sent, 1)
	require.Equal(t, "收到", f.Sent[0].Text)
}

func bodySourceApp(t *testing.T, stdin string) (*App, *larkcli.Fake) {
	t.Helper()
	f := larkcli.NewFake()
	a := &App{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, In: strings.NewReader(stdin),
		cfg: config.Config{DataDir: t.TempDir()}, larkClient: f}
	return a, f
}

func runBodyCmd(t *testing.T, a *App, cmd *cobra.Command, args ...string) {
	t.Helper()
	cmd.SetOut(a.Out)
	cmd.SetErr(a.Err)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
}

// noFetch is the fetcher for a body naming no remote picture: reaching for
// the network would mean the test is not testing what it says it is.
func noFetch(t *testing.T) sync.Fetcher {
	return func(context.Context, string) ([]byte, string, error) {
		t.Fatal("this body names no remote picture")
		return nil, "", nil
	}
}

func TestSendCmd_MarkdownUploadsThePicturesItNames(t *testing.T) {
	f := larkcli.NewFake()
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	require.NoError(t, os.WriteFile(shot, []byte("png"), 0o644))

	msg, err := outgoingFlags{markdown: "## 周报\n\n![截图](" + shot + ")\n\n见图"}.
		outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Equal(t, []string{shot}, f.Uploads)
	require.Equal(t, "## 周报\n\n![截图](img_fake_1)\n\n见图", msg.Markdown)
}

func TestSendCmd_MarkdownPassesAnImageKeyThrough(t *testing.T) {
	f := larkcli.NewFake()
	msg, err := outgoingFlags{markdown: "![截图](img_v3_shot)"}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Empty(t, f.Uploads, "a key Feishu already holds needs no upload")
	require.Equal(t, "![截图](img_v3_shot)", msg.Markdown)
}

func TestSendCmd_MarkdownLeavesAReferenceInsideAFenceAlone(t *testing.T) {
	f := larkcli.NewFake()
	body := "写法是：\n\n```markdown\n![截图](./shot.png)\n```"
	msg, err := outgoingFlags{markdown: body}.outgoing(t.Context(), f, noFetch(t))
	require.NoError(t, err)
	require.Empty(t, f.Uploads)
	require.Equal(t, body, msg.Markdown)
}

func TestSendCmd_MarkdownNamesTheLineOfAPictureThatIsNotThere(t *testing.T) {
	f := larkcli.NewFake()
	_, err := outgoingFlags{markdown: "## 周报\n\n![截图](./nope.png)"}.
		outgoing(t.Context(), f, noFetch(t))
	require.ErrorContains(t, err, "line 3")
	require.ErrorContains(t, err, "no such file: ./nope.png")
	require.Empty(t, f.Sent)
}

func TestSendCmd_MarkdownDownloadsARemotePictureBeforeUploading(t *testing.T) {
	f := larkcli.NewFake()
	var asked string
	fetch := func(_ context.Context, url string) ([]byte, string, error) {
		asked = url
		return []byte("png"), "image/png", nil
	}

	msg, err := outgoingFlags{markdown: "![图](https://example.com/a.png)\n\n见图"}.
		outgoing(t.Context(), f, fetch)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/a.png", asked)
	require.Len(t, f.Uploads, 1)
	require.Equal(t, "![图](img_fake_1)\n\n见图", msg.Markdown)
}
