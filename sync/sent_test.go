package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// answer is a message the way a send's answer carries it: no position, and a
// sender without a name.
func answer(m larkcli.RawMessage) larkcli.SentMessage {
	m.MessagePosition = 0
	m.Sender = larkcli.RawSender{ID: "ou_me", SenderType: "user"}
	m.Raw = []byte(`{"message_id":"` + m.MessageID + `"}`)
	return larkcli.SentMessage{MessageID: m.MessageID, ChatID: m.ChatID, Message: &m}
}

func TestIngestSent_StoresTheAnswerWithoutFetchingItBack(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	require.NoError(t, s.Store.UpsertContacts(ctx, []store.Contact{{OpenID: "ou_me", Name: "林岚"}}, 1))

	require.NoError(t, s.IngestSent(ctx, answer(msg("om_sent", "oc_a", clk.t, "发布好了"))))

	got, err := s.Store.GetMessage(ctx, "om_sent")
	require.NoError(t, err)
	require.Equal(t, "发布好了", got.Content)
	require.NotZero(t, got.RenderedAt, "rendered in the same breath, so the bubble hands over to text")
	require.Equal(t, "林岚", got.SenderName, "a nameless row would draw its sender as an id")
	require.Zero(t, callsTo(f, "mget"))
	require.Zero(t, callsTo(f, "render"))
}

func TestIngestSent_TheNextListingWritesThePositionOverTheZero(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	m := msg("om_sent", "oc_a", clk.t, "发布好了")
	m.Sender = larkcli.RawSender{ID: "ou_me", SenderType: "user"}
	require.NoError(t, s.IngestSent(ctx, answer(m)))

	m.MessagePosition = 42
	f.AddMessage(m)
	require.NoError(t, s.IngestIDs(ctx, []string{"om_sent"}))

	got, err := s.Store.GetMessage(ctx, "om_sent")
	require.NoError(t, err)
	require.EqualValues(t, 42, got.MessagePosition)
	require.Zero(t, got.EditedAt, "the answer's body is the stored one; nothing reads as an edit")
	require.Equal(t, "发布好了", got.Content)
}

func TestIngestSent_FetchesAReplyInsideAThread(t *testing.T) {
	// Whether a thread reply's position is negative depends on the kind of
	// chat, and the sign is what keeps it out of the chat's own flow.
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	m := msg("om_reply", "oc_a", clk.t, "收到")
	m.ThreadID, m.ParentID, m.MessagePosition = "omt_1", "om_root", -3
	f.AddMessage(m)

	require.NoError(t, s.IngestSent(ctx, answer(m)))

	got, err := s.Store.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.EqualValues(t, -3, got.MessagePosition)
	require.Equal(t, 1, callsTo(f, "mget"))
}

func TestIngestSent_FetchesWhatCameBackWithoutTheMessage(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.AddMessage(msg("om_fwd", "oc_a", clk.t, "转发"))

	require.NoError(t, s.IngestSent(ctx, larkcli.SentMessage{MessageID: "om_fwd", ChatID: "oc_a"}))

	_, err := s.Store.GetMessage(ctx, "om_fwd")
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "mget"))
}

func TestKeepSent_TheMessageFindsItsPictureAlreadyDone(t *testing.T) {
	s, _, clk := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir = dir
	src := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(src, []byte("png"), 0o600))

	require.NoError(t, s.KeepSent(ctx, "img_v3_a", "image", src))
	m := msg("om_pic", "oc_a", clk.t, "")
	m.MsgType, m.Body.Content = "image", `{"image_key":"img_v3_a"}`
	require.NoError(t, s.IngestSent(ctx, answer(m)))

	rs, err := s.Store.ResourcesFor(ctx, "om_pic")
	require.NoError(t, err)
	require.Len(t, rs, 1)
	require.Equal(t, "done", rs[0].Status, "the ingest's own registration leaves the kept file alone")
	require.Equal(t, filepath.Join("resources", "sent", "img_v3_a.png"), rs[0].LocalPath)
	b, err := os.ReadFile(filepath.Join(dir, rs[0].LocalPath))
	require.NoError(t, err)
	require.Equal(t, "png", string(b))
}

func TestKeepSent_SettlesAKeyAlreadyWaitingForItsDownload(t *testing.T) {
	s, _, _ := newSyncer(t)
	ctx := t.Context()
	s.Opt().DataDir = t.TempDir()
	require.NoError(t, s.Store.AddPendingResources(ctx, []store.ResourceRef{{MessageID: "om_pic", FileKey: "file_v3_a", Type: "file"}}))
	src := filepath.Join(t.TempDir(), "notes.pdf")
	require.NoError(t, os.WriteFile(src, []byte("pdf"), 0o600))

	require.NoError(t, s.KeepSent(ctx, "file_v3_a", "file", src))

	rs, err := s.Store.ResourcesFor(ctx, "om_pic")
	require.NoError(t, err)
	require.Equal(t, "done", rs[0].Status)
	require.EqualValues(t, 3, rs[0].SizeBytes)
}

func TestKeepSent_LeavesAFilePastTheCapToTheDownloader(t *testing.T) {
	s, _, _ := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir, s.Opt().MaxBytes = dir, 2
	require.NoError(t, s.Store.AddPendingResources(ctx, []store.ResourceRef{{MessageID: "om_pic", FileKey: "img_v3_a", Type: "image"}}))
	src := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(src, []byte("png"), 0o600))

	require.NoError(t, s.KeepSent(ctx, "img_v3_a", "image", src))

	rs, err := s.Store.ResourcesFor(ctx, "om_pic")
	require.NoError(t, err)
	require.Equal(t, "pending", rs[0].Status)
	_, err = os.Stat(filepath.Join(dir, "resources", "sent"))
	require.True(t, os.IsNotExist(err))
}
