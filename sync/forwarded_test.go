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

// bundleMsg is a merge_forward message as it arrives in the chat that got it.
// Its own body is the literal Feishu sends for every bundle, which names
// nothing inside.
func bundleMsg(id, chatID string, at time.Time) larkcli.RawMessage {
	m := msg(id, chatID, at, "")
	m.MsgType = "merge_forward"
	m.Body.Content = `{"text":"Merged and Forwarded Message"}`
	return m
}

// child is one item of a bundle's flat listing.
func child(id, upper, chatID, msgType, content string, at time.Time) larkcli.RawForwarded {
	return larkcli.RawForwarded{
		MessageID: id, ChatID: chatID, MsgType: msgType, CreateTime: msAt(at),
		Sender: larkcli.RawSender{ID: "ou_a", SenderType: "user", SenderName: "张三"},
		Body:   larkcli.RawBody{Content: content},

		UpperMessageID: upper,
	}
}

// storeBundle puts a bundle in messages and on the queue, the way a listing
// would, without running a whole tick.
func storeBundle(t *testing.T, s *Syncer, id, chatID string, at time.Time) {
	t.Helper()
	_, _, err := s.upsertRaw(t.Context(), []larkcli.RawMessage{bundleMsg(id, chatID, at)}, at)
	require.NoError(t, err)
}

func TestToForwarded_TellsTheContainerFromItsChildrenByID(t *testing.T) {
	base := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	// The bundle comes back among its own items, and its message_position is
	// a real one. A child's is absent, which decodes to 0 — so the id is the
	// only thing that separates them.
	self := child("om_fwd", "", "oc_a", "merge_forward", `{"text":"Merged and Forwarded Message"}`, base)
	self.MessagePosition = 612
	kids := toForwarded("om_fwd", []larkcli.RawForwarded{
		self,
		child("om_zero", "om_fwd", "oc_src", "text", `{"text":"第一条"}`, base),
	})

	require.Len(t, kids, 1)
	require.Equal(t, "om_zero", kids[0].MessageID, "position 0 is a position, not a marker")
	require.Equal(t, "oc_src", kids[0].ChatID, "the child keeps the chat it was actually sent to")
}

func TestToForwarded_NumbersSiblingsByWhenTheyWereSaid(t *testing.T) {
	base := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	kids := toForwarded("om_fwd", []larkcli.RawForwarded{
		child("om_late", "om_fwd", "oc_src", "text", `{"text":"后"}`, base.Add(time.Minute)),
		child("om_early", "om_fwd", "oc_src", "text", `{"text":"先"}`, base),
	})

	require.Equal(t, []string{"om_early", "om_late"}, []string{kids[0].MessageID, kids[1].MessageID})
	require.Equal(t, []int{0, 1}, []int{kids[0].Seq, kids[1].Seq})
}

func TestExpandForwards_GroupsChildrenByUpperMessageID(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	storeBundle(t, s, "om_fwd", "oc_a", clk.t.Add(-time.Minute))
	f.Bundles["om_fwd"] = []larkcli.RawForwarded{
		child("om_fwd", "", "oc_a", "merge_forward", `{"text":"Merged and Forwarded Message"}`, clk.t),
		child("om_a", "om_fwd", "oc_src", "text", `{"text":"第一条"}`, clk.t.Add(-2*time.Hour)),
		child("om_b", "om_fwd", "oc_src", "text", `{"text":"第二条"}`, clk.t.Add(-time.Hour)),
	}

	n, err := s.expandForwards(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	kids, err := s.Store.ForwardChildren(ctx, "om_fwd", "om_fwd")
	require.NoError(t, err)
	require.Equal(t, []string{"om_a", "om_b"}, []string{kids[0].MessageID, kids[1].MessageID})
	require.Equal(t, "张三", kids[0].SenderName)
}

func TestExpandForwards_ANestedBundleKeepsItsOwnChildren(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	storeBundle(t, s, "om_fwd", "oc_a", clk.t.Add(-time.Minute))
	f.Bundles["om_fwd"] = []larkcli.RawForwarded{
		child("om_fwd", "", "oc_a", "merge_forward", `{"text":"Merged and Forwarded Message"}`, clk.t),
		child("om_a", "om_fwd", "oc_src", "text", `{"text":"第一条"}`, clk.t.Add(-2*time.Hour)),
		child("om_inner", "om_fwd", "oc_src", "merge_forward", `{"text":"Merged and Forwarded Message"}`, clk.t.Add(-time.Hour)),
		child("om_deep", "om_inner", "oc_other", "text", `{"text":"里面那条"}`, clk.t.Add(-3*time.Hour)),
	}

	_, err := s.expandForwards(ctx, clk.t)
	require.NoError(t, err)

	top, err := s.Store.ForwardChildren(ctx, "om_fwd", "om_fwd")
	require.NoError(t, err)
	require.Equal(t, []string{"om_a", "om_inner"}, []string{top[0].MessageID, top[1].MessageID},
		"one frame lists one level; the nested bundle is a single row there")
	deep, err := s.Store.ForwardChildren(ctx, "om_fwd", "om_inner")
	require.NoError(t, err)
	require.Len(t, deep, 1)
	require.Equal(t, "om_deep", deep[0].MessageID)
}

func TestExpandForwards_CountsOnlyTheTopLevelChildren(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	storeBundle(t, s, "om_fwd", "oc_a", clk.t.Add(-time.Minute))
	f.Bundles["om_fwd"] = []larkcli.RawForwarded{
		child("om_fwd", "", "oc_a", "merge_forward", `{"text":"Merged and Forwarded Message"}`, clk.t),
		child("om_a", "om_fwd", "oc_src", "text", `{"text":"第一条"}`, clk.t.Add(-2*time.Hour)),
		child("om_inner", "om_fwd", "oc_src", "merge_forward", `{"text":"Merged and Forwarded Message"}`, clk.t.Add(-time.Hour)),
		child("om_deep", "om_inner", "oc_other", "text", `{"text":"里面那条"}`, clk.t.Add(-3*time.Hour)),
	}

	_, err := s.expandForwards(ctx, clk.t)
	require.NoError(t, err)

	root, err := s.Store.GetForwardRoot(ctx, "om_fwd")
	require.NoError(t, err)
	require.Equal(t, 2, root.ChildCount, "the number on the summary line is what the frame behind it lists")
}

func TestExpandForwards_ARefusedBundleIsStampedAndNotRetried(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	storeBundle(t, s, "om_gone", "oc_a", clk.t.Add(-time.Minute))
	f.BundleErr["om_gone"] = &larkcli.Error{ExitCode: larkcli.ExitAPI, APICode: 230002, APIMessage: "permission denied"}

	n, err := s.expandForwards(ctx, clk.t)
	require.NoError(t, err, "one unreadable forward must not stop the sweep")
	require.Zero(t, n)

	root, err := s.Store.GetForwardRoot(ctx, "om_gone")
	require.NoError(t, err)
	require.NotEmpty(t, root.LastError)
	require.EqualValues(t, clk.t.UnixMilli(), root.FetchedAt)

	// A forward is frozen: asking again a day later cannot change the answer.
	_, err = s.expandForwards(ctx, clk.t.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, countCalls(f.Calls, "forwarded:om_gone"))
}

func TestExpandForwards_ATimeoutIsRetriedAfterItsBackoff(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	storeBundle(t, s, "om_fwd", "oc_a", clk.t.Add(-time.Minute))
	f.BundleErr["om_fwd"] = &larkcli.Error{ExitCode: larkcli.ExitNetwork, Message: "timeout"}

	_, err := s.expandForwards(ctx, clk.t)
	require.NoError(t, err)
	root, err := s.Store.GetForwardRoot(ctx, "om_fwd")
	require.NoError(t, err)
	require.Equal(t, 1, root.Attempts)
	require.Zero(t, root.FetchedAt, "a bundle that timed out is still owed")

	delete(f.BundleErr, "om_fwd")
	f.Bundles["om_fwd"] = []larkcli.RawForwarded{
		child("om_a", "om_fwd", "oc_src", "text", `{"text":"第一条"}`, clk.t.Add(-2*time.Hour)),
	}
	n, err := s.expandForwards(ctx, clk.t.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestExpandForwards_TakesTheNewestBundlesFirst(t *testing.T) {
	s, f, clk := newSyncer(t)
	s.Opt.ForwardsPerTick = 1
	storeBundle(t, s, "om_old", "oc_a", clk.t.Add(-2*time.Hour))
	storeBundle(t, s, "om_new", "oc_a", clk.t.Add(-time.Minute))

	_, err := s.expandForwards(t.Context(), clk.t)
	require.NoError(t, err)
	require.Equal(t, 1, countCalls(f.Calls, "forwarded:om_new"))
	require.Zero(t, countCalls(f.Calls, "forwarded:om_old"))
}

func TestExpandForwards_RegistersAChildAttachmentUnderTheBundle(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt.DataDir = t.TempDir()
	storeBundle(t, s, "om_fwd", "oc_a", clk.t.Add(-time.Minute))
	f.Bundles["om_fwd"] = []larkcli.RawForwarded{
		child("om_pic", "om_fwd", "oc_src", "image", `{"image_key":"img_inside"}`, clk.t.Add(-2*time.Hour)),
		child("om_doc", "om_fwd", "oc_src", "file", `{"file_key":"file_inside","file_name":"季度报告.xlsx"}`, clk.t.Add(-time.Hour)),
	}

	_, err := s.expandForwards(ctx, clk.t)
	require.NoError(t, err)

	// The resource endpoint refuses a child's id, so the bundle is what the
	// bytes are asked for under.
	res, err := s.Store.ResourcesFor(ctx, "om_fwd")
	require.NoError(t, err)
	keys := make([]string, 0, len(res))
	for _, r := range res {
		keys = append(keys, r.FileKey)
	}
	require.ElementsMatch(t, []string{"img_inside", "file_inside"}, keys)

	under, err := s.Store.ResourcesFor(ctx, "om_pic")
	require.NoError(t, err)
	require.Empty(t, under, "nothing may be asked for under a child's id")
}

func TestUpsertRaw_QueuesEveryBundleItStores(t *testing.T) {
	s, _, clk := newSyncer(t)
	ctx := t.Context()
	_, _, err := s.upsertRaw(ctx, []larkcli.RawMessage{
		bundleMsg("om_fwd", "oc_a", clk.t.Add(-time.Minute)),
		msg("om_text", "oc_a", clk.t, "hi"),
	}, clk.t)
	require.NoError(t, err)

	due, err := s.Store.ForwardRootsDue(ctx, clk.t.UnixMilli(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "om_fwd", due[0].RootMessageID)
}
