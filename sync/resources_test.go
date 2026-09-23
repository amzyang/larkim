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
	require.Empty(t, ExtractResources("om", "sticker", `{"file_key":"x"}`))
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
	fil := msg("om_file", "oc_a", clk.t.Add(-2*time.Minute), "")
	fil.MsgType, fil.Body.Content = "file", `{"file_key":"file_big"}`
	f.AddMessage(fil)
	lost := msg("om_lost", "oc_a", clk.t.Add(-3*time.Minute), "")
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
	require.Equal(t, clk.t.Add(time.Minute).UnixMilli(), rs[0].NextAttemptAt)
	m, _ := s.Store.GetMessage(ctx, "om_img")
	require.NotZero(t, m.RenderedAt, "download step also renders")
	require.Equal(t, 1, countCalls(f.Calls, "render:true"), "one download call covers the batch")
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
	require.Equal(t, []store.Resource{
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
