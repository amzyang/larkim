package sync

import (
	"bytes"
	"context"
	"image"
	"image/color/palette"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

// clientSticker writes one picture where the Lark client keeps it. name is
// what the client called the file, which for the user's own sets is the bare
// key.
func clientSticker(t *testing.T, dir, name string) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, gif.Encode(&b, image.NewPaletted(image.Rect(0, 0, 1, 1), palette.Plan9), nil))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644))
	return b.Bytes()
}

func stickerMsg(id, key string, at time.Time) larkcli.RawMessage {
	m := msg(id, "oc_a", at, "")
	m.MsgType, m.Body.Content = "sticker", `{"file_key":"`+key+`"}`
	return m
}

func TestTick_StickerPicturesComeFromTheLarkClient(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	data, client := t.TempDir(), t.TempDir()
	s.Opt.DataDir, s.Opt.ClientDir = data, client
	pic := clientSticker(t, filepath.Join(client, "LarkShell", "sdk_storage", "u1", "resources", "stickers"), "v3_recv.png")
	clientSticker(t, filepath.Join(client, "LarkShell-ka-x", "sdk_storage", "u2", "sticker_sets", "7543"), "v3_mine")

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(stickerMsg("om_recv", "v3_recv", clk.t.Add(-time.Minute)))
	f.AddMessage(stickerMsg("om_mine", "v3_mine", clk.t.Add(-2*time.Minute)))
	f.AddMessage(stickerMsg("om_gone", "v3_gone", clk.t.Add(-3*time.Minute)))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Stickers)
	require.Zero(t, countCalls(f.Calls, "render:true"), "a sticker is never asked of lark-cli")

	rs, _ := s.Store.ResourcesFor(ctx, "om_recv")
	require.Equal(t, "done", rs[0].Status)
	require.Equal(t, "sticker", rs[0].Type)
	require.Equal(t, filepath.Join("resources", "stickers", "v3_recv.gif"), rs[0].LocalPath,
		"the extension follows the bytes, not the name the client gave them")
	require.Equal(t, int64(len(pic)), rs[0].SizeBytes)
	stored, err := os.ReadFile(filepath.Join(data, rs[0].LocalPath))
	require.NoError(t, err)
	require.Equal(t, pic, stored)

	rs, _ = s.Store.ResourcesFor(ctx, "om_mine")
	require.Equal(t, filepath.Join("resources", "stickers", "v3_mine.gif"), rs[0].LocalPath,
		"the client keeps the user's own sets unnamed, so the format comes from the bytes alone")

	rs, _ = s.Store.ResourcesFor(ctx, "om_gone")
	require.Equal(t, "failed", rs[0].Status)
	require.Equal(t, 1, rs[0].Attempts)
	require.Contains(t, rs[0].LastError, "Lark client")
	require.Equal(t, clk.t.Add(time.Minute).UnixMilli(), rs[0].NextAttemptAt)
}

func TestTick_StickerPicturesOverTheSizeLimitAreSkipped(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	data, client := t.TempDir(), t.TempDir()
	s.Opt.DataDir, s.Opt.ClientDir, s.Opt.MaxBytes = data, client, 1
	clientSticker(t, filepath.Join(client, "LarkShell", "sdk_storage", "u1", "resources", "stickers"), "v3_big.png")

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(stickerMsg("om_big", "v3_big", clk.t.Add(-time.Minute)))

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.Stickers)

	rs, _ := s.Store.ResourcesFor(ctx, "om_big")
	require.Equal(t, "skipped", rs[0].Status)
	require.Contains(t, rs[0].LastError, "larger than 1 bytes")
	require.NoDirExists(t, filepath.Join(data, stickerSubdir))
}

func TestCopySticker_LeavesNoHalfWrittenCopyBehind(t *testing.T) {
	data, client := t.TempDir(), t.TempDir()
	dir := filepath.Join(client, "LarkShell", "sdk_storage", "u1", "resources", "stickers")
	pic := clientSticker(t, dir, "v3_a.png")

	_, size, err := copySticker(filepath.Join(dir, "v3_a.png"), data, "v3_a")
	require.NoError(t, err)
	require.Equal(t, int64(len(pic)), size)
	left, err := filepath.Glob(filepath.Join(data, stickerSubdir, ".tmp-*"))
	require.NoError(t, err)
	require.Empty(t, left)
}

func TestCopySticker_KeepsThePictureItAlreadyHas(t *testing.T) {
	data, client := t.TempDir(), t.TempDir()
	dir := filepath.Join(client, "LarkShell", "sdk_storage", "u1", "resources", "stickers")
	clientSticker(t, dir, "v3_a.png")

	rel, _, err := copySticker(filepath.Join(dir, "v3_a.png"), data, "v3_a")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(data, rel), []byte("already here"), 0o600))

	again, size, err := copySticker(filepath.Join(dir, "v3_a.png"), data, "v3_a")
	require.NoError(t, err)
	require.Equal(t, rel, again)
	require.Equal(t, int64(len("already here")), size)
	stored, err := os.ReadFile(filepath.Join(data, rel))
	require.NoError(t, err)
	require.Equal(t, "already here", string(stored), "a key names one picture, so the copy is made once")
}

func TestFindSticker_AnswersNothingForAKeyTheClientNeverDrew(t *testing.T) {
	src, size := findSticker(t.TempDir(), "v3_a")
	require.Empty(t, src)
	require.Zero(t, size)
}
