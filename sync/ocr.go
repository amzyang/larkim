package sync

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	stdsync "sync"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// readImageText reads the writing in pictures already on disk. A chat's
// screenshots are content its message bodies never repeat, and the assistant
// and the reaction suggester are handed those bodies; without this the whole
// of a stack trace someone pasted as a picture is the token [Image: img_…].
//
// Keyed by resource rather than by message, like the download it follows: the
// same screenshot forwarded into four chats is one picture and one reading.
func (s *Syncer) readImageText(ctx context.Context, now time.Time) (int, error) {
	if s.Opt().DataDir == "" {
		return 0, nil
	}
	due, err := s.Store.ImagesForTextDue(ctx, now.UnixMilli(), s.Opt().ImageTextPerTick)
	if err != nil {
		return 0, err
	}
	// Read together and recorded afterwards, as the download does: the lane
	// bounds how many subprocesses run at once and the store keeps its single
	// writer.
	text := make([][]string, len(due))
	errs := make([]error, len(due))
	var wg stdsync.WaitGroup
	for i, d := range due {
		if d.SizeBytes > larkcli.MaxOCRBytes {
			continue
		}
		wg.Go(func() { text[i], errs[i] = s.Client.RecognizeText(ctx, filepath.Join(s.Opt().DataDir, d.LocalPath)) })
	}
	wg.Wait()
	done := 0
	for i, d := range due {
		var err error
		switch {
		case d.SizeBytes > larkcli.MaxOCRBytes:
			err = s.Store.MarkResourceTextSkipped(ctx, d.FileKey,
				fmt.Sprintf("larger than %d bytes", larkcli.MaxOCRBytes))
		case errs[i] != nil:
			// A shutdown cancelled every call still in flight at once, the
			// way it does mid-download; charging each an attempt would spend
			// a picture's whole retry budget on one Ctrl-C.
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			err = s.failImageText(ctx, d, errs[i], now)
		default:
			// Empty is an answer: a photo with no writing in it settles here
			// rather than coming round again every tick forever.
			if err = s.Store.MarkResourceText(ctx, d.FileKey, strings.Join(text[i], "\n")); err == nil {
				done++
			}
		}
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// failImageText records a failed reading and when to try again, on the same
// ladder a failed download walks.
func (s *Syncer) failImageText(ctx context.Context, d store.ImageForText, cause error, now time.Time) error {
	next := int64(0)
	if !permanentFailure(cause) {
		if delay := ResourceRetryDelay(d.Attempts + 1); delay > 0 {
			next = now.Add(delay).UnixMilli()
		}
	}
	return s.Store.MarkResourceTextFailed(ctx, d.FileKey, cause.Error(), next)
}
