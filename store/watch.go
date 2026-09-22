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
// re-read by message id.
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
