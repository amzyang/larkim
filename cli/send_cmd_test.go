package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/larkcli"
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

	msg, err := outgoingFlags{file: path}.outgoing(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, "file_fake_1", msg.FileKey)
	require.Equal(t, []string{path}, f.Uploads)
}

func TestSendCmd_FileFlagPassesAKeyThrough(t *testing.T) {
	f := larkcli.NewFake()
	msg, err := outgoingFlags{file: "file_v3_report"}.outgoing(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, "file_v3_report", msg.FileKey)
	require.Empty(t, f.Uploads, "a key Feishu already holds needs no upload")
}

func TestSendCmd_TextIsStillVerbatim(t *testing.T) {
	// A script piping a changelog into --text must not start sending posts
	// just because the text happens to look like markdown.
	msg, err := outgoingFlags{text: "## 发布说明\n- a"}.outgoing(context.Background(), larkcli.NewFake())
	require.NoError(t, err)
	require.Equal(t, "## 发布说明\n- a", msg.Text)
	require.Empty(t, msg.Markdown)
}

func TestSendCmd_MarkdownFlagSendsAPost(t *testing.T) {
	msg, err := outgoingFlags{markdown: "## 发布说明"}.outgoing(context.Background(), larkcli.NewFake())
	require.NoError(t, err)
	require.Equal(t, "## 发布说明", msg.Markdown)
	require.Empty(t, msg.Text)
}

func TestSendCmd_ImageFlagUploadsFirst(t *testing.T) {
	f := larkcli.NewFake()
	path := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(path, []byte("png"), 0o644))

	msg, err := outgoingFlags{image: path}.outgoing(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, "img_fake_1", msg.ImageKey)
	require.Equal(t, []string{path}, f.Uploads)
}

func TestSendCmd_ImageFlagPassesAKeyThrough(t *testing.T) {
	f := larkcli.NewFake()
	msg, err := outgoingFlags{image: "img_v3_shot"}.outgoing(context.Background(), f)
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
