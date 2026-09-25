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

func TestExtractResources(t *testing.T) {
	require.Equal(t, "img_1", ExtractResources("om", "image", `{"image_key":"img_1"}`)[0].FileKey)
	rs := ExtractResources("om", "file", `{"file_key":"file_1","file_name":"a.pdf"}`)
	require.Equal(t, "file", rs[0].Type)
	post := `{"zh_cn":{"title":"t","content":[[{"tag":"text","text":"x"},{"tag":"img","image_key":"img_p"}],[{"tag":"media","file_key":"file_m"},{"tag":"img","image_key":"img_p"}]]}}`
	rs = ExtractResources("om", "post", post)
	require.Len(t, rs, 2, "duplicates collapse")
	require.Equal(t, []store.ResourceRef{{MessageID: "om", FileKey: "v3_s", Type: "sticker"}},
		ExtractResources("om", "sticker", `{"file_key":"v3_s"}`))
	require.Equal(t, []store.ResourceRef{
		{MessageID: "om", FileKey: "file_clip", Type: "file"},
		{MessageID: "om", FileKey: "img_cover", Type: "cover"},
	}, ExtractResources("om", "media", `{"file_key":"file_clip","image_key":"img_cover","duration":25046}`),
		"a video is its clip and the frame the client shows it as")
	require.Empty(t, ExtractResources("om", "text", `not json`))
}

func TestSchedules(t *testing.T) {
	require.Equal(t, time.Minute, ReadCheckDelay(1))
	require.Equal(t, 6*time.Hour, ReadCheckDelay(9))
	require.Equal(t, 5*time.Minute, ResourceRetryDelay(2))
	require.Zero(t, ResourceRetryDelay(5))
}

func TestTick_DownloadsResourcesAndAppliesSizeCap(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.Opt.DataDir, s.Opt.MaxBytes, s.Opt.DownloadPerTick = dir, 100, 1
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	small := filepath.Join(resDir, "img_small.jpg")
	big := filepath.Join(resDir, "file_big.bin")
	require.NoError(t, os.WriteFile(small, []byte("tiny"), 0o644))
	require.NoError(t, os.WriteFile(big, make([]byte, 500), 0o644))

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	img := msg("om_img", "oc_a", clk.t.Add(-time.Minute), "")
	img.MsgType, img.Body.Content = "image", `{"image_key":"img_small"}`
	f.AddMessage(img)
	fil := msg("om_file", "oc_a", clk.t.Add(-30*time.Second), "")
	fil.MsgType, fil.Body.Content = "file", `{"file_key":"file_big"}`
	f.AddMessage(fil)
	lost := msg("om_lost", "oc_a", clk.t.Add(-45*time.Second), "")
	lost.MsgType, lost.Body.Content = "image", `{"image_key":"img_lost"}`
	f.AddMessage(lost)
	f.Resources["om_img"] = []larkcli.Resource{{MessageID: "om_img", Key: "img_small", Type: "image", LocalPath: small, SizeBytes: 4}}
	f.Resources["om_file"] = []larkcli.Resource{{MessageID: "om_file", Key: "file_big", Type: "file", LocalPath: big, SizeBytes: 500}}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Downloaded)
	rs, _ := s.Store.ResourcesFor(ctx, "om_img")
	require.Equal(t, "done", rs[0].Status)
	require.Equal(t, filepath.Join("resources", "lark-im-resources", "img_small.jpg"), rs[0].LocalPath)
	rs, _ = s.Store.ResourcesFor(ctx, "om_file")
	require.Equal(t, "skipped", rs[0].Status)
	_, statErr := os.Stat(big)
	require.True(t, os.IsNotExist(statErr), "oversized download removed")
	rs, _ = s.Store.ResourcesFor(ctx, "om_lost")
	require.Equal(t, "failed", rs[0].Status)
	require.Equal(t, 1, rs[0].Attempts)
	require.Zero(t, rs[0].NextAttemptAt, "\"File not in msg\" is the same answer every time; one attempt is all it gets")
	m, _ := s.Store.GetMessage(ctx, "om_img")
	require.NotZero(t, m.RenderedAt, "download step also renders")
	require.Equal(t, 1, countCalls(f.Calls, "render:true"), "one download call covers the batch")
}

func TestTick_FetchesAVideoCoverOnItsOwn(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.Opt.DataDir, s.Opt.DownloadPerTick = dir, 1
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	clip := filepath.Join(resDir, "file_clip.mp4")
	cover := filepath.Join(resDir, "img_cover.png")
	require.NoError(t, os.WriteFile(clip, []byte("clip"), 0o644))
	require.NoError(t, os.WriteFile(cover, []byte("frame"), 0o644))

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	vid := msg("om_vid", "oc_a", clk.t.Add(-time.Minute), "")
	vid.MsgType, vid.Body.Content = "media", `{"file_key":"file_clip","image_key":"img_cover","duration":25046}`
	f.AddMessage(vid)
	// lark-cli's batch download extracts the clip alone, which is why the
	// cover has to be asked for by itself.
	f.Resources["om_vid"] = []larkcli.Resource{{MessageID: "om_vid", Key: "file_clip", Type: "file", LocalPath: clip, SizeBytes: 4}}
	f.Singles["om_vid/img_cover"] = larkcli.Resource{LocalPath: cover, SizeBytes: 5}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Downloaded)
	rs, _ := s.Store.ResourcesFor(ctx, "om_vid")
	require.Len(t, rs, 2)
	for _, r := range rs {
		require.Equal(t, "done", r.Status, "key %s", r.FileKey)
	}
	require.Equal(t, 1, countCalls(f.Calls, "download:om_vid:img_cover"), "one call per cover, not one per tick")

	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, countCalls(f.Calls, "download:om_vid:img_cover"), "a cover already here is not asked for again")
}

func TestTick_ACoverFeishuRefusesStopsRetryingLikeAnyOtherResource(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir, s.Opt.DownloadPerTick = t.TempDir(), 1

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	vid := msg("om_vid", "oc_a", clk.t.Add(-time.Minute), "")
	vid.MsgType, vid.Body.Content = "media", `{"file_key":"file_clip","image_key":"img_gone"}`
	f.AddMessage(vid)

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	rs, _ := s.Store.ResourcesFor(ctx, "om_vid")
	for _, r := range rs {
		require.Equal(t, "failed", r.Status, "key %s", r.FileKey)
		require.Equal(t, 1, r.Attempts)
		require.Zero(t, r.NextAttemptAt, "key %s", r.FileKey)
	}
	ev, err := s.Store.LastEvents(ctx, 10)
	require.NoError(t, err)
	require.Len(t, ev, 2, "both keys are on the record, not only in a log file")
	require.Equal(t, store.EventResourceGone, ev[0].Kind)
	require.Equal(t, []string{"file_clip", "img_gone"},
		[]string{min(ev[0].Subject, ev[1].Subject), max(ev[0].Subject, ev[1].Subject)},
		"the record is about the key, which is what will not be served")
}

func TestTick_PollsReadStatusOnSchedule(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	other := msg("om_other", "oc_a", clk.t.Add(-time.Minute), "hi")
	other.Sender = larkcli.RawSender{ID: "ou_them", SenderType: "user"}
	f.AddMessage(other)
	mine := msg("om_mine", "oc_a", clk.t.Add(-time.Minute), "me")
	mine.Sender = larkcli.RawSender{ID: "ou_self", SenderType: "user"}
	f.AddMessage(mine)
	_, err := s.EnsureIdentity(ctx)
	require.NoError(t, err)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.ReadChecks, "only the other's message is asked about")
	m, _ := s.Store.GetMessage(ctx, "om_other")
	require.False(t, *m.IsReadRemote)

	clk.t = clk.t.Add(30 * time.Second)
	rep, _ = s.Tick(ctx)
	require.Zero(t, rep.ReadChecks, "next check in 1m")

	f.Read["om_other"] = true
	clk.t = clk.t.Add(time.Minute)
	rep, _ = s.Tick(ctx)
	require.Equal(t, 1, rep.ReadChecks)
	m, _ = s.Store.GetMessage(ctx, "om_other")
	require.True(t, *m.IsReadRemote)
	clk.t = clk.t.Add(time.Hour)
	rep, _ = s.Tick(ctx)
	require.Zero(t, rep.ReadChecks, "read messages are final")
}

func TestRegisterExistingResources_BackScansOldRows(t *testing.T) {
	s, _, _ := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_old_img", ChatID: "oc", MsgType: "image", ContentRaw: `{"image_key":"img_old"}`, CreateMs: 1, RawJSON: "{}"},
		{MessageID: "om_old_txt", ChatID: "oc", MsgType: "text", ContentRaw: `{"text":"x"}`, CreateMs: 2, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.registerExistingResources(ctx))
	rs, _ := s.Store.ResourcesFor(ctx, "om_old_img")
	require.Len(t, rs, 1)
	require.Equal(t, "pending", rs[0].Status)
	v, _, _ := s.Store.GetState(ctx, KeyResourceScanID)
	require.Equal(t, "2", v, "scan cursor at the newest row seen")
	require.NoError(t, s.registerExistingResources(ctx), "idempotent when caught up")
}

// cardAttachment is the real shape a card message carries: the body names an
// imageID, and only the attachment table knows the key behind it.
const cardAttachment = `{"json_card":"{\"body\":{\"elements\":[{\"tag\":\"img\",\"property\":{\"imageID\":\"2\"}}]}}",` +
	`"json_attachment":{"images":{"1":{"origin_key":"img_a"},"2":{"origin_key":"img_b","token":"tok"}}},"card_schema":1}`

func TestExtractResources_CardImagesComeFromTheAttachmentTable(t *testing.T) {
	require.Equal(t, []store.ResourceRef{
		{MessageID: "om", FileKey: "img_a", Type: "image"},
		{MessageID: "om", FileKey: "img_b", Type: "image"},
	}, ExtractResources("om", "interactive", cardAttachment))
}

func TestExtractResources_CardAttachmentAlsoArrivesAsAString(t *testing.T) {
	raw := `{"json_card":"{}","json_attachment":"{\"images\":{\"1\":{\"origin_key\":\"img_a\"}}}"}`
	rs := ExtractResources("om", "interactive", raw)
	require.Len(t, rs, 1)
	require.Equal(t, "img_a", rs[0].FileKey)

	require.Empty(t, ExtractResources("om", "interactive", `{"json_card":"{}"}`), "a card with no images")
	require.Empty(t, ExtractResources("om", "interactive", `{"json_attachment":"not json"}`))
}

func TestExtractResources_FindsImagesInsideAnMdPostTag(t *testing.T) {
	// The shape lark-cli's --markdown builds: one md element holding the
	// whole body, so the image key is named nowhere else.
	post := `{"zh_cn":{"content":[[{"tag":"md","text":"## 周报\n\n![截图](img_p)\n\n见图"}]]}}`
	require.Equal(t, []store.ResourceRef{{MessageID: "om", FileKey: "img_p", Type: "image"}},
		ExtractResources("om", "post", post))

	both := `{"zh_cn":{"content":[[{"tag":"md","text":"![a](img_p) ![b](img_q)"}],[{"tag":"img","image_key":"img_p"}]]}}`
	rs := ExtractResources("om", "post", both)
	require.Len(t, rs, 2, "a key named twice registers once")
	require.Equal(t, "img_p", rs[0].FileKey)
	require.Equal(t, "img_q", rs[1].FileKey)
}

func TestExtractResources_IgnoresANonImgRefInsideMd(t *testing.T) {
	// Only an uploaded key is downloadable; a path or a URL never became one.
	post := `{"zh_cn":{"content":[[{"tag":"md","text":"![x](./a.png) ![y](https://example.com/b.png)"}]]}}`
	require.Empty(t, ExtractResources("om", "post", post))
}

func TestTick_DownloadsAPostImageTheBatchLeftOut(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.Opt.DataDir, s.Opt.DownloadPerTick = dir, 1
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	shot := filepath.Join(resDir, "img_p.png")
	require.NoError(t, os.WriteFile(shot, []byte("shot"), 0o644))

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	post := msg("om_post", "oc_a", clk.t.Add(-time.Minute), "")
	post.MsgType = "post"
	post.Body.Content = `{"zh_cn":{"content":[[{"tag":"md","text":"## 周报\n\n![截图](img_p)"}]]}}`
	f.AddMessage(post)
	// lark-cli's worklist walks img and media elements only, so a key named
	// from inside an md element never reaches the batch.
	f.Singles["om_post/img_p"] = larkcli.Resource{LocalPath: shot, SizeBytes: 4}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Downloaded)

	rs, _ := s.Store.ResourcesFor(ctx, "om_post")
	require.Len(t, rs, 1)
	require.Equal(t, "done", rs[0].Status)
	require.Equal(t, filepath.Join("resources", "lark-im-resources", "img_p.png"), rs[0].LocalPath)
	require.Equal(t, 1, countCalls(f.Calls, "download:om_post:img_p"))
}

func TestFetchAside_StillRefusesAFileKey(t *testing.T) {
	s, _, _ := newSyncer(t)
	// Loosening the gate for images does not open it: a file lark-cli's batch
	// left out is a gap to report, not a call to spend.
	_, err := s.fetchAside(context.Background(), "om", store.Resource{FileKey: "file_x", Type: "file"})
	require.ErrorContains(t, err, "not returned by lark-cli")
	_, err = s.fetchAside(context.Background(), "om", store.Resource{FileKey: "v3_s", Type: "sticker"})
	require.ErrorContains(t, err, "not returned by lark-cli")
}

func TestTick_AFailureThatMightPassNextTimeKeepsItsRetries(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir, s.Opt.DownloadPerTick = t.TempDir(), 1

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	vid := msg("om_vid", "oc_a", clk.t.Add(-time.Minute), "")
	vid.MsgType, vid.Body.Content = "media", `{"file_key":"file_clip","image_key":"img_cover"}`
	f.AddMessage(vid)
	f.Resources["om_vid"] = []larkcli.Resource{{MessageID: "om_vid", Key: "file_clip", Type: "file",
		LocalPath: filepath.Join(s.Opt.DataDir, "resources", "lark-im-resources", "gone.mp4")}}

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	rs, _ := s.Store.ResourcesFor(ctx, "om_vid")
	var clip store.Resource
	for _, r := range rs {
		if r.FileKey == "file_clip" {
			clip = r
		}
	}
	require.Equal(t, "failed", clip.Status)
	require.Contains(t, clip.LastError, "downloaded file missing")
	require.Equal(t, clk.t.Add(time.Minute).UnixMilli(), clip.NextAttemptAt,
		"lark-cli said it wrote the file, so the next tick is worth a try")
	ev, err := s.Store.LastEvents(ctx, 10)
	require.NoError(t, err)
	for _, e := range ev {
		require.NotEqual(t, "file_clip", e.Subject, "only a permanent failure goes on the record")
	}
}
