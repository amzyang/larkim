package sync

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestExtractResources(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	require.Equal(t, time.Minute, ReadCheckDelay(1))
	require.Equal(t, 6*time.Hour, ReadCheckDelay(9))
	require.Equal(t, 5*time.Minute, ResourceRetryDelay(2))
	require.Zero(t, ResourceRetryDelay(5))
}

func TestTick_DownloadsResourcesAndAppliesSizeCap(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir, s.Opt().MaxBytes, s.Opt().DownloadPerTick = dir, 100, 1
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
	f.Resources["om_img/img_small"] = larkcli.Resource{LocalPath: small, SizeBytes: 4}
	f.Resources["om_file/file_big"] = larkcli.Resource{LocalPath: big, SizeBytes: 500}

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
	require.Equal(t, 1, countCalls(f.Calls, "download:om_file:file_big"),
		"the oversized file is fetched before its size is known, and once")
}

func TestTick_FetchesAVideoCoverOnItsOwn(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir, s.Opt().DownloadPerTick = dir, 1
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
	f.Resources["om_vid/file_clip"] = larkcli.Resource{LocalPath: clip, SizeBytes: 4}
	f.Resources["om_vid/img_cover"] = larkcli.Resource{LocalPath: cover, SizeBytes: 5}

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
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().DataDir, s.Opt().DownloadPerTick = t.TempDir(), 1

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
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
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
	t.Parallel()
	s, _, _ := newSyncer(t)
	ctx := t.Context()
	s.Opt().DataDir = t.TempDir()
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
	t.Parallel()
	require.Equal(t, []store.ResourceRef{
		{MessageID: "om", FileKey: "img_a", Type: "image"},
		{MessageID: "om", FileKey: "img_b", Type: "image"},
	}, ExtractResources("om", "interactive", cardAttachment))
}

func TestExtractResources_CardAttachmentAlsoArrivesAsAString(t *testing.T) {
	t.Parallel()
	raw := `{"json_card":"{}","json_attachment":"{\"images\":{\"1\":{\"origin_key\":\"img_a\"}}}"}`
	rs := ExtractResources("om", "interactive", raw)
	require.Len(t, rs, 1)
	require.Equal(t, "img_a", rs[0].FileKey)

	require.Empty(t, ExtractResources("om", "interactive", `{"json_card":"{}"}`), "a card with no images")
	require.Empty(t, ExtractResources("om", "interactive", `{"json_attachment":"not json"}`))
}

func TestExtractResources_FindsImagesInsideAnMdPostTag(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	// Only an uploaded key is downloadable; a path or a URL never became one.
	post := `{"zh_cn":{"content":[[{"tag":"md","text":"![x](./a.png) ![y](https://example.com/b.png)"}]]}}`
	require.Empty(t, ExtractResources("om", "post", post))
}

func TestTick_DownloadsAPostImageTheBatchLeftOut(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir, s.Opt().DownloadPerTick = dir, 1
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
	f.Resources["om_post/img_p"] = larkcli.Resource{LocalPath: shot, SizeBytes: 4}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Downloaded)

	rs, _ := s.Store.ResourcesFor(ctx, "om_post")
	require.Len(t, rs, 1)
	require.Equal(t, "done", rs[0].Status)
	require.Equal(t, filepath.Join("resources", "lark-im-resources", "img_p.png"), rs[0].LocalPath)
	require.Equal(t, 1, countCalls(f.Calls, "download:om_post:img_p"))
}

func TestAPIType_MapsEveryStoredTypeOntoTheTwoTheEndpointTakes(t *testing.T) {
	t.Parallel()
	// A video's cover is an image of its own, whatever the clip beside it is.
	require.Equal(t, "image", apiType("image"))
	require.Equal(t, "image", apiType("cover"))
	require.Equal(t, "file", apiType("file"))
}

func TestTick_AFailureThatMightPassNextTimeKeepsItsRetries(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().DataDir, s.Opt().DownloadPerTick = t.TempDir(), 1

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	vid := msg("om_vid", "oc_a", clk.t.Add(-time.Minute), "")
	vid.MsgType, vid.Body.Content = "media", `{"file_key":"file_clip","image_key":"img_cover"}`
	f.AddMessage(vid)
	f.Resources["om_vid/file_clip"] = larkcli.Resource{
		LocalPath: filepath.Join(s.Opt().DataDir, "resources", "lark-im-resources", "gone.mp4")}

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

func TestTick_ReadProbeOvertakesTheBackoff(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{
		{ChatID: "oc_a", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_b", Name: "项目协作群", ChatMode: "group"},
	}
	for _, m := range []struct{ id, chat string }{{"om_a_old", "oc_a"}, {"om_a_new", "oc_a"}, {"om_b", "oc_b"}} {
		raw := msg(m.id, m.chat, clk.t.Add(-time.Minute), "hi")
		raw.Sender = larkcli.RawSender{ID: "ou_them", SenderType: "user"}
		f.AddMessage(raw)
	}
	_, err := s.EnsureIdentity(ctx)
	require.NoError(t, err)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, rep.ReadChecks, "the ladder's first pass asks about all three")

	// Well inside the ladder's one-minute first step: only the probe can see
	// this, and the whole chat has to come with it.
	f.Read["om_a_old"], f.Read["om_a_new"] = true, true
	clk.t = clk.t.Add(5 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.ReadChecks, "the read chat is re-asked in full; the other is not")

	for _, id := range []string{"om_a_old", "om_a_new"} {
		m, err := s.Store.GetMessage(ctx, id)
		require.NoError(t, err)
		require.True(t, *m.IsReadRemote, id)
	}
	m, err := s.Store.GetMessage(ctx, "om_b")
	require.NoError(t, err)
	require.False(t, *m.IsReadRemote, "a chat whose probe came back unread keeps its schedule")
}

func TestTick_AMessageArrivesUnreadWithItsRow(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	raw := msg("om_arrived", "oc_a", clk.t.Add(-time.Second), "hi")
	raw.Sender = larkcli.RawSender{ID: "ou_them", SenderType: "user"}
	f.AddMessage(raw)
	_, err := s.EnsureIdentity(ctx)
	require.NoError(t, err)
	// The first nudge that finds the message is the first page a reader could
	// draw it on, and it has to carry the flag: a page that has the message
	// without it draws it read, outside the unread block it then joins.
	var first *store.Message
	s.OnChange = func() {
		if m, err := s.Store.GetMessage(ctx, "om_arrived"); err == nil && first == nil {
			first = &m
		}
	}

	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, first.IsReadRemote, "the row and its flag land in one write")
	require.False(t, *first.IsReadRemote)
	require.Equal(t, 1, countCalls(f.Calls, "read-status"), "Feishu is still asked, once")
	n, err := s.Store.ReadCheckCount(ctx, "om_arrived")
	require.NoError(t, err)
	require.Equal(t, 1, n, "the answer is recorded as the first check")
}

func TestTick_AnArrivalReadElsewhereIsSettledBeforeTheSweeps(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().RepairEvery = 0
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_seed", "oc_a", clk.t.Add(-time.Hour), "seed"))
	_, err := s.EnsureIdentity(ctx)
	require.NoError(t, err)
	for range 2 {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
		clk.t = clk.t.Add(5 * time.Second)
	}

	// Read in the client as it landed: stored unread on arrival, which only
	// Feishu's answer can take back.
	raw := msg("om_seen", "oc_a", clk.t.Add(-time.Second), "hi")
	raw.Sender = larkcli.RawSender{ID: "ou_them", SenderType: "user"}
	f.AddMessage(raw)
	f.Read["om_seen"] = true
	f.SearchHidden = []string{"om_seen"}
	clk.t = clk.t.Add(s.Opt().ChatsRefreshEvery) // a round with the full listing in it
	f.Calls = nil

	_, err = s.Tick(ctx)
	require.NoError(t, err)
	m, err := s.Store.GetMessage(ctx, "om_seen")
	require.NoError(t, err)
	require.True(t, *m.IsReadRemote)
	asked, listed := slices.Index(f.Calls, "read-status"), slices.Index(f.Calls, "chats")
	require.GreaterOrEqual(t, listed, 0, "the round is one that lists every chat")
	require.Less(t, asked, listed,
		"the answer is fetched before the round's sweeps, which is as long as the false unread lasts")
}

func TestTick_ReadProbeRecordsAnUncheckedMessageInOneCall(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	// Too old to count as arriving, so nothing stores it unread before the
	// probe answers for it.
	raw := msg("om_fresh", "oc_a", clk.t.Add(-arrivalWindow-time.Minute), "hi")
	raw.Sender = larkcli.RawSender{ID: "ou_them", SenderType: "user"}
	f.AddMessage(raw)
	_, err := s.EnsureIdentity(ctx)
	require.NoError(t, err)
	// The first nudge that could put the message on the badge, and whether
	// the tick had already finished by then: Tick nudges once more at its end,
	// which is a whole round of later steps after the flag was written.
	var flagged, afterTick bool
	s.OnChange = func() {
		m, err := s.Store.GetMessage(ctx, "om_fresh")
		if err != nil || flagged || m.IsReadRemote == nil {
			return
		}
		flagged = true
		last, err := s.stateTime(ctx, KeyLastTickAt)
		require.NoError(t, err)
		afterTick = !last.IsZero()
	}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, countCalls(f.Calls, "read-status"),
		"the probe's answer is the first check; asking the ladder again is a second round trip for the same fact")
	require.Equal(t, 1, rep.ReadChecks)
	m, err := s.Store.GetMessage(ctx, "om_fresh")
	require.NoError(t, err)
	require.False(t, *m.IsReadRemote)
	require.True(t, flagged)
	require.False(t, afterTick, "the flag's write is announced as it lands, not with the end of the tick")

	// The probe recorded a first check, so the ladder's schedule holds.
	clk.t = clk.t.Add(30 * time.Second)
	rep, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, rep.ReadChecks, "a checked message's still-unread answer is dropped; the next step is 1m out")
	n, err := s.Store.ReadCheckCount(ctx, "om_fresh")
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestTick_ADownloadNeverRendersItsMessage(t *testing.T) {
	t.Parallel()
	// Two errands. Carrying them in one lark-cli call had every message whose
	// attachment had not landed re-rendered on each tick it waited.
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().DataDir, s.Opt().DownloadPerTick = t.TempDir(), 1

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	cd := msg("om_card", "oc_a", clk.t.Add(-time.Minute), "")
	cd.MsgType, cd.Body.Content = "interactive", cardAttachment
	f.AddMessage(cd)
	// lark-cli says it wrote the file and no file is there, which is the
	// failure worth another tick.
	for _, key := range []string{"img_a", "img_b"} {
		f.Resources["om_card/"+key] = larkcli.Resource{
			LocalPath: filepath.Join(s.Opt().DataDir, "resources", "lark-im-resources", "gone.png")}
	}

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	m, err := s.Store.GetMessage(ctx, "om_card")
	require.NoError(t, err)
	require.NotZero(t, m.RenderedAt, "a message waiting on an attachment is still rendered")

	clk.t = clk.t.Add(2 * time.Minute)
	_, err = s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, countCalls(f.Calls, "download:om_card:img_a"), "the retry was owed")
	again, err := s.Store.GetMessage(ctx, "om_card")
	require.NoError(t, err)
	require.Equal(t, m.RenderedAt, again.RenderedAt,
		"the retry tick re-stamped nothing: a download writes to resources, not to messages")
}

func TestTick_AKeyTwoMessagesNameIsFetchedOnce(t *testing.T) {
	t.Parallel()
	// A bot reuses one card header across thousands of messages; the bytes are
	// the same bytes, so the queue is keyed by resource rather than by message.
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir, s.Opt().DownloadPerTick = dir, 1
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	shot := filepath.Join(resDir, "img_shared.png")
	require.NoError(t, os.WriteFile(shot, []byte("shot"), 0o644))

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	for _, id := range []string{"om_one", "om_two"} {
		m := msg(id, "oc_a", clk.t.Add(-time.Minute), "")
		m.MsgType, m.Body.Content = "image", `{"image_key":"img_shared"}`
		f.AddMessage(m)
	}
	f.Resources["om_one/img_shared"] = larkcli.Resource{LocalPath: shot, SizeBytes: 4}
	f.Resources["om_two/img_shared"] = larkcli.Resource{LocalPath: shot, SizeBytes: 4}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Downloaded)
	require.Equal(t, 1, countCalls(f.Calls, "download:om_one:img_shared")+
		countCalls(f.Calls, "download:om_two:img_shared"), "one key, one call")
	for _, id := range []string{"om_one", "om_two"} {
		rs, err := s.Store.ResourcesFor(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "done", rs[0].Status, id)
	}
}
