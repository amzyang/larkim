package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// picturesOnDisk seeds one message with the named pictures already
// downloaded, each file sized as asked, and returns the directory they are
// under.
func picturesOnDisk(t *testing.T, s *Syncer, sizes map[string]int64) string {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir = dir
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_a", CreateMs: 10, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	for key, size := range sizes {
		rel := filepath.Join("resources", "lark-im-resources", key+".png")
		require.NoError(t, os.WriteFile(filepath.Join(dir, rel), make([]byte, 1), 0o644))
		require.NoError(t, s.Store.AddPendingResources(ctx, []store.ResourceRef{
			{MessageID: "om_a", FileKey: key, Type: "image"}}))
		require.NoError(t, s.Store.MarkResourceDone(ctx, key, rel, size))
	}
	return dir
}

func TestReadImageText_ReadsEachPictureOnce(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	dir := picturesOnDisk(t, s, map[string]int64{"img_code": 100, "img_photo": 100})
	f.Recognized = map[string][]string{
		filepath.Join(dir, "resources", "lark-im-resources", "img_code.png"): {"NullPointerException", "at Foo.java:42"},
	}

	n, err := s.readImageText(ctx, clk.Now())
	require.NoError(t, err)
	require.Equal(t, 2, n)

	got, err := s.Store.ImageTextsFor(ctx, []string{"om_a"})
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"om_a": {"NullPointerException\nat Foo.java:42"}}, got,
		"the regions arrive one per line; a picture with nothing in it leaves no entry")

	require.Empty(t, mustDue(t, s, clk.Now()), "an empty reading is an answer, not an omission")
	n, err = s.readImageText(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 2, recognizeCalls(f), "a settled picture is never read again")
}

func TestReadImageText_SkipsAPictureTooLargeForTheRecognizer(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	picturesOnDisk(t, s, map[string]int64{"img_huge": larkcli.MaxOCRBytes + 1})

	n, err := s.readImageText(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, recognizeCalls(f), "the limit is known here, so the call is never spent")
	require.Empty(t, mustDue(t, s, clk.Now().Add(365*24*time.Hour)))
}

func TestReadImageText_WalksTheLadderAndSettlesAPermanentRefusal(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	picturesOnDisk(t, s, map[string]int64{"img_a": 100})
	f.RecognizeErr = &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network", Message: "timeout"}

	for i := 1; i < maxResourceAttempts; i++ {
		n, err := s.readImageText(ctx, clk.Now())
		require.NoError(t, err)
		require.Zero(t, n)
		due := mustDue(t, s, clk.Now())
		require.Empty(t, due, "a failed reading waits for its retry")
		clk.t = clk.t.Add(ResourceRetryDelay(i))
		due = mustDue(t, s, clk.Now())
		require.Len(t, due, 1)
		require.Equal(t, i, due[0].Attempts)
	}
	n, err := s.readImageText(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, mustDue(t, s, clk.Now().Add(365*24*time.Hour)), "the ladder runs out")
}

func TestReadImageText_APermanentRefusalSettlesOnTheFirstAttempt(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	picturesOnDisk(t, s, map[string]int64{"img_a": 100})
	f.RecognizeErr = &larkcli.Error{ExitCode: larkcli.ExitAPI, Type: "authorization",
		Subtype: "app_scope_not_applied", Message: "scope not applied"}

	_, err := s.readImageText(ctx, clk.Now())
	require.NoError(t, err, "one refused picture does not stop the sweep")
	require.Empty(t, mustDue(t, s, clk.Now().Add(365*24*time.Hour)),
		"a refusal the server will repeat is not worth five attempts")
	require.Equal(t, 1, recognizeCalls(f))
}

func TestReadImageText_NothingToDoWithoutADataDir(t *testing.T) {
	s, f, clk := newSyncer(t)
	picturesOnDisk(t, s, map[string]int64{"img_a": 100})
	s.Opt().DataDir = ""

	n, err := s.readImageText(t.Context(), clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, recognizeCalls(f))
}

func TestTick_ReadsTheWritingInPicturesItDownloaded(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	dir := t.TempDir()
	s.Opt().DataDir = dir
	resDir := filepath.Join(dir, "resources", "lark-im-resources")
	require.NoError(t, os.MkdirAll(resDir, 0o755))
	shot := filepath.Join(resDir, "img_shot.png")
	require.NoError(t, os.WriteFile(shot, []byte("png"), 0o644))

	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	img := msg("om_img", "oc_a", clk.t.Add(-time.Minute), "")
	img.MsgType, img.Body.Content = "image", `{"image_key":"img_shot"}`
	f.AddMessage(img)
	f.Resources["om_img/img_shot"] = larkcli.Resource{LocalPath: shot, SizeBytes: 3}
	f.Recognized = map[string][]string{shot: {"排期表"}}

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Downloaded)
	require.Equal(t, 1, rep.ImageText, "the download and the reading land on the same tick")
	got, _ := s.Store.ImageTextsFor(ctx, []string{"om_img"})
	require.Equal(t, map[string][]string{"om_img": {"排期表"}}, got)
}

// recognizeCalls counts the readings the fake was asked for; each records the
// path it was given, so they cannot be counted by name alone.
func recognizeCalls(f *larkcli.Fake) int {
	n := 0
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "recognize:") {
			n++
		}
	}
	return n
}

func mustDue(t *testing.T, s *Syncer, now time.Time) []store.ImageForText {
	t.Helper()
	due, err := s.Store.ImagesForTextDue(t.Context(), now.UnixMilli(), 10)
	require.NoError(t, err)
	return due
}
