package store

import (
	"context"
	"slices"
)

// ImageForText is a picture whose bytes are on disk and whose text has not
// been read yet, along with what the attempts so far cost.
type ImageForText struct {
	FileKey   string
	LocalPath string // relative to the data dir
	SizeBytes int64
	Attempts  int
}

// imageTextDue is a picture worth an attempt: never tried, or tried and
// failed with its scheduled retry now owed. It takes the current time as its
// one parameter.
const imageTextDue = `(t.file_key IS NULL OR (t.status = 'failed' AND t.next_attempt_at > 0 AND t.next_attempt_at <= ?))`

// ImagesForTextDue returns pictures worth reading now, newest first: the
// screenshot posted this morning is the one a summary is about, and a
// backfill that starts at the far end of the archive reaches it last.
func (s *Store) ImagesForTextDue(ctx context.Context, now int64, limit int) ([]ImageForText, error) {
	scan := func(sc scanner) (ImageForText, error) {
		var i ImageForText
		err := sc.Scan(&i.FileKey, &i.LocalPath, &i.SizeBytes, &i.Attempts)
		return i, err
	}
	return queryAll(ctx, s.db, scan, `SELECT r.file_key, r.local_path, r.size_bytes, COALESCE(t.attempts, 0) `+
		resourceFrom+` LEFT JOIN resource_text t ON t.file_key = r.file_key
 WHERE r.type IN ('image','cover') AND r.status = 'done' AND r.local_path <> '' AND `+imageTextDue+`
 GROUP BY r.file_key ORDER BY max(m.create_ms) DESC LIMIT ?`, now, limit)
}

// MarkResourceText records the text read out of a picture. Empty text is an
// answer like any other: a photo with no writing in it is done.
func (s *Store) MarkResourceText(ctx context.Context, fileKey, text string) error {
	return s.settleResourceText(ctx, fileKey, text, "done", "")
}

// MarkResourceTextSkipped records a picture deliberately not read — too
// large for the recognizer, or refused in a way repeating cannot fix.
func (s *Store) MarkResourceTextSkipped(ctx context.Context, fileKey, reason string) error {
	return s.settleResourceText(ctx, fileKey, "", "skipped", reason)
}

// settleResourceText writes a final state, counting the attempt it took.
func (s *Store) settleResourceText(ctx context.Context, fileKey, text, status, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_text (file_key, text, status, attempts, last_error)
 VALUES (?, ?, ?, 1, ?)
 ON CONFLICT(file_key) DO UPDATE SET text = excluded.text, status = excluded.status,
 attempts = resource_text.attempts + 1, next_attempt_at = 0, last_error = excluded.last_error`,
		fileKey, text, status, reason)
	return err
}

// MarkResourceTextFailed records a failed attempt and when to try again.
func (s *Store) MarkResourceTextFailed(ctx context.Context, fileKey, reason string, nextAttemptAt int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_text (file_key, status, attempts, next_attempt_at, last_error)
 VALUES (?, 'failed', 1, ?, ?)
 ON CONFLICT(file_key) DO UPDATE SET status = 'failed', attempts = resource_text.attempts + 1,
 next_attempt_at = excluded.next_attempt_at, last_error = excluded.last_error`,
		fileKey, nextAttemptAt, reason)
	return err
}

// ImageTextsFor returns the text read out of each message's pictures, keyed
// by message id. A picture with nothing written in it is left out, and so is
// a message whose pictures are all like that.
func (s *Store) ImageTextsFor(ctx context.Context, messageIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(messageIDs))
	for chunk := range slices.Chunk(messageIDs, 500) {
		type owned struct{ MessageID, Text string }
		rows, err := queryAll(ctx, s.db, func(sc scanner) (owned, error) {
			var o owned
			err := sc.Scan(&o.MessageID, &o.Text)
			return o, err
		}, `SELECT mr.message_id, t.text FROM resource_text t
 JOIN message_resources mr ON mr.file_key = t.file_key
 WHERE t.text <> '' AND mr.message_id IN `+inClause(len(chunk))+`
 ORDER BY mr.message_id, t.file_key`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, o := range rows {
			out[o.MessageID] = append(out[o.MessageID], o.Text)
		}
	}
	return out, nil
}
