package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestExtractRendered_TakesPicturesOnlyFromAForwardedBundle(t *testing.T) {
	const body = "张三: 见附图\n![Image](img_fwd_a)\n李四: [Image: img_fwd_b]"

	require.Equal(t, []store.ResourceRef{
		{MessageID: "om_fwd", FileKey: "img_fwd_a", Type: "image"},
		{MessageID: "om_fwd", FileKey: "img_fwd_b", Type: "image"},
	}, ExtractRendered("om_fwd", "merge_forward", body))

	// Every other type names its pictures in the body, where ExtractResources
	// reads them. Reading the rendering too would register whatever a sender
	// typed that happens to look like a reference.
	require.Nil(t, ExtractRendered("om_post", "post", body))
	require.Nil(t, ExtractRendered("om_text", "text", body))
}

func TestTick_DownloadsThePicturesInsideAForwardedBundle(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.Opt.DataDir = dir
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	shot := filepath.Join(resDir, "img_fwd.png")
	require.NoError(t, os.WriteFile(shot, []byte("shot"), 0o644))

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	fwd := msg("om_fwd", "oc_a", clk.t.Add(-time.Minute), "")
	fwd.MsgType = "merge_forward"
	// The bundle's own body is this literal whatever it carries, so nothing
	// before the rendering can name the picture inside it.
	fwd.Body.Content = `{"text":"Merged and Forwarded Message"}`
	f.AddMessage(fwd)
	f.Rendered["om_fwd"] = larkcli.RenderedMessage{MessageID: "om_fwd", ChatID: "oc_a",
		MsgType: "merge_forward", Content: "<p>张三: 看这个</p>\n![Image](img_fwd)"}
	f.Singles["om_fwd/img_fwd"] = larkcli.Resource{LocalPath: shot, SizeBytes: 4}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Downloaded)

	rs, err := s.Store.ResourcesFor(ctx, "om_fwd")
	require.NoError(t, err)
	require.Len(t, rs, 1)
	require.Equal(t, "done", rs[0].Status)
	require.Equal(t, filepath.Join("resources", "lark-im-resources", "img_fwd.png"), rs[0].LocalPath)
	require.Equal(t, 1, countCalls(f.Calls, "download:om_fwd:img_fwd"))
}

func TestRegisterExistingResources_ReadsAStoredBundlesRendering(t *testing.T) {
	s, _, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t.UnixMilli()

	_, err := s.Store.UpsertMessages(ctx, []store.Message{{
		MessageID: "om_old", ChatID: "oc_a", MsgType: "merge_forward", SenderID: "ou_a",
		ContentRaw: `{"text":"Merged and Forwarded Message"}`, CreateMs: now, UpdateMs: now,
	}}, now)
	require.NoError(t, err)
	require.NoError(t, s.Store.UpdateRendered(ctx, "om_old", "李四: 见图\n![Image](img_old)", "", "", now))

	require.NoError(t, s.registerExistingResources(ctx))

	rs, err := s.Store.ResourcesFor(ctx, "om_old")
	require.NoError(t, err)
	require.Len(t, rs, 1)
	require.Equal(t, "img_old", rs[0].FileKey)
	require.Equal(t, "image", rs[0].Type)
}
