package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// seedPictures puts two downloaded pictures and one file in the archive, the
// picture on the newer message first.
func seedPictures(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_old", ChatID: "oc", CreateMs: 10, RawJSON: "{}"},
		{MessageID: "om_new", ChatID: "oc", CreateMs: 20, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{
		{MessageID: "om_old", FileKey: "img_old", Type: "image"},
		{MessageID: "om_new", FileKey: "img_new", Type: "image"},
		{MessageID: "om_new", FileKey: "file_b", Type: "file"},
	}))
	require.NoError(t, s.MarkResourceDone(ctx, "img_old", "resources/lark-im-resources/img_old.jpg", 100))
	require.NoError(t, s.MarkResourceDone(ctx, "img_new", "resources/lark-im-resources/img_new.png", 200))
	require.NoError(t, s.MarkResourceDone(ctx, "file_b", "resources/lark-im-resources/file_b.pdf", 300))
}

func dueTextKeys(due []ImageForText) []string {
	out := make([]string, len(due))
	for i, d := range due {
		out[i] = d.FileKey
	}
	return out
}

func TestImagesForTextDue_OnlyDownloadedPicturesNotYetRead(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedPictures(t, s)

	due, err := s.ImagesForTextDue(ctx, 100, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"img_new", "img_old"}, dueTextKeys(due), "newest message's picture first; a file is not a picture")
	require.Equal(t, "resources/lark-im-resources/img_new.png", due[0].LocalPath)
	require.EqualValues(t, 200, due[0].SizeBytes)
	require.Zero(t, due[0].Attempts)

	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{{MessageID: "om_new", FileKey: "img_owed", Type: "image"}}))
	due, _ = s.ImagesForTextDue(ctx, 100, 10)
	require.Equal(t, []string{"img_new", "img_old"}, dueTextKeys(due), "a picture whose bytes never landed has nothing to read")
}

func TestImagesForTextDue_SettledPicturesStayOut(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedPictures(t, s)

	require.NoError(t, s.MarkResourceText(ctx, "img_new", ""))
	require.NoError(t, s.MarkResourceTextSkipped(ctx, "img_old", "larger than 5242880 bytes"))
	due, err := s.ImagesForTextDue(ctx, 1e12, 10)
	require.NoError(t, err)
	require.Empty(t, due, "empty text is an answer, and so is a deliberate skip")
}

func TestImagesForTextDue_AFailedPictureWaitsForItsRetry(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedPictures(t, s)
	require.NoError(t, s.MarkResourceText(ctx, "img_old", "x"))

	require.NoError(t, s.MarkResourceTextFailed(ctx, "img_new", "timeout", 500))
	due, _ := s.ImagesForTextDue(ctx, 100, 10)
	require.Empty(t, due)
	due, _ = s.ImagesForTextDue(ctx, 500, 10)
	require.Equal(t, []string{"img_new"}, dueTextKeys(due))
	require.Equal(t, 1, due[0].Attempts, "the ladder is walked from what the row already cost")

	require.NoError(t, s.MarkResourceTextFailed(ctx, "img_new", "gave up", 0))
	due, _ = s.ImagesForTextDue(ctx, 1e12, 10)
	require.Empty(t, due, "next_attempt_at = 0 means permanently failed")
}

func TestImageTextsFor_KeyedByMessageWithBlanksLeftOut(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedPictures(t, s)
	// The older picture is shared: the same screenshot forwarded on.
	require.NoError(t, s.AddPendingResources(ctx, []ResourceRef{{MessageID: "om_new", FileKey: "img_old", Type: "image"}}))
	require.NoError(t, s.MarkResourceText(ctx, "img_old", "排期表"))
	require.NoError(t, s.MarkResourceText(ctx, "img_new", ""))

	got, err := s.ImageTextsFor(ctx, []string{"om_old", "om_new"})
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"om_old": {"排期表"}, "om_new": {"排期表"}}, got,
		"one reading serves every message naming the key; a picture with nothing in it leaves no entry")

	got, err = s.ImageTextsFor(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}
