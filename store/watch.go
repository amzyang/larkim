package store

import (
	"context"
	"time"
)

// MessagesAfterRowID returns messages ingested after rowID, oldest first.
func (s *Store) MessagesAfterRowID(ctx context.Context, rowID int64, chatID string, limit int) ([]Message, error) {
	q := `SELECT ` + messageColumns + ` ` + messageFrom + ` WHERE m.id > ?`
	args := []any{rowID}
	if chatID != "" {
		q += ` AND m.chat_id = ?`
		args = append(args, chatID)
	}
	if limit <= 0 {
		limit = 1000
	}
	q += ` ORDER BY m.id LIMIT ?`
	args = append(args, limit)
	return queryAll(ctx, s.db, scanMessage, q, args...)
}

// Watch polls the ingest counter every interval and delivers each batch of
// newly stored messages (optionally for one chat) until ctx is done.
// Rendering may lag a tick behind ingestion; consumers that need content
// re-read by message id. It reports inserts only: a consumer that displays
// rows wants WatchRev instead.
func (s *Store) Watch(ctx context.Context, every time.Duration, chatID string) <-chan []Message {
	ch := make(chan []Message)
	go func() {
		defer close(ch)
		last, _ := s.MaxMessageRowID(ctx)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			cur, err := s.MaxMessageRowID(ctx)
			if err != nil || cur == last {
				continue
			}
			msgs, err := s.MessagesAfterRowID(ctx, last, chatID, 1000)
			if err != nil {
				continue
			}
			last = cur
			if len(msgs) == 0 {
				continue
			}
			select {
			case ch <- msgs:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// DataRev returns the database's change counter. Every insert and update to
// the synced tables advances it, so a consumer that sees a new value must
// re-read whatever it displays. Unlike the ingest id it moves on updates too:
// renderings, read status, card refreshes, attachment downloads.
func (s *Store) DataRev(ctx context.Context) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx, `SELECT rev FROM data_rev WHERE id = 1`).Scan(&rev)
	return rev, err
}

// WatchRev polls DataRev every interval and delivers each new value until ctx
// is done. A receive on nudge brings the next comparison forward; it only
// changes when the same comparison happens, so a spurious nudge costs one
// query and delivers nothing. Pass nil when nothing can signal.
func (s *Store) WatchRev(ctx context.Context, every time.Duration, nudge <-chan struct{}) <-chan int64 {
	ch := make(chan int64)
	go func() {
		defer close(ch)
		last, _ := s.DataRev(ctx)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-nudge:
			}
			cur, err := s.DataRev(ctx)
			if err != nil || cur == last {
				continue
			}
			last = cur
			select {
			case ch <- cur:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}
