package store

import "context"

// Event is one row of events: something the syncer decided that no other
// table keeps.
type Event struct {
	ID      int64  `json:"id"`
	AtMs    int64  `json:"at_ms"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Event kinds.
const (
	// EventResourceGone is an attachment Feishu will not serve again, so
	// nothing retries it.
	EventResourceGone = "resource_gone"
)

// RecordEvent appends an event and prunes the table to the newest 1000 rows.
func (s *Store) RecordEvent(ctx context.Context, e Event) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO events(at_ms, kind, subject, detail) VALUES (?,?,?,?)`,
		e.AtMs, e.Kind, e.Subject, e.Detail); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM events WHERE id < (SELECT id FROM events ORDER BY id DESC LIMIT 1 OFFSET 999)`)
	return err
}

// LastEvents returns the newest n events, newest first.
func (s *Store) LastEvents(ctx context.Context, n int) ([]Event, error) {
	return queryAll(ctx, s.db, func(sc scanner) (Event, error) {
		var e Event
		err := sc.Scan(&e.ID, &e.AtMs, &e.Kind, &e.Subject, &e.Detail)
		return e, err
	}, `SELECT id, at_ms, kind, subject, detail FROM events ORDER BY id DESC LIMIT ?`, n)
}
