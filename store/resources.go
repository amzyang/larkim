package store

import (
	"context"
	"database/sql"
)

// Resource is one row of resources: an attachment key of a message and its
// local download state.
type Resource struct {
	MessageID     string `json:"message_id"`
	FileKey       string `json:"file_key"`
	Type          string `json:"type"`
	LocalPath     string `json:"local_path,omitempty"` // relative to the data dir
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt int64  `json:"next_attempt_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

const resourceColumns = `message_id, file_key, type, local_path, size_bytes, status, attempts, next_attempt_at, last_error`

func scanResource(sc interface{ Scan(...any) error }) (Resource, error) {
	var r Resource
	err := sc.Scan(&r.MessageID, &r.FileKey, &r.Type, &r.LocalPath, &r.SizeBytes, &r.Status, &r.Attempts, &r.NextAttemptAt, &r.LastError)
	return r, err
}

// AddPendingResources registers attachment keys; existing rows are untouched.
func (s *Store) AddPendingResources(ctx context.Context, rs []Resource) error {
	if len(rs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO resources (message_id, file_key, type) VALUES (?, ?, ?) ON CONFLICT(message_id, file_key) DO NOTHING`, r.MessageID, r.FileKey, r.Type); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkResourceDone records a completed download.
func (s *Store) MarkResourceDone(ctx context.Context, messageID, fileKey, localPath string, size int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET status = 'done', local_path = ?, size_bytes = ?, last_error = '' WHERE message_id = ? AND file_key = ?`, localPath, size, messageID, fileKey)
	return err
}

// MarkResourceSkipped records a resource deliberately not kept (too large).
func (s *Store) MarkResourceSkipped(ctx context.Context, messageID, fileKey string, size int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET status = 'skipped', size_bytes = ?, last_error = ? WHERE message_id = ? AND file_key = ?`, size, reason, messageID, fileKey)
	return err
}

// MarkResourceFailed records a failed attempt and when to retry; after
// maxAttempts the row becomes permanently failed (next_attempt_at = 0).
func (s *Store) MarkResourceFailed(ctx context.Context, messageID, fileKey, reason string, nextAttemptAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET status = 'failed', attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE message_id = ? AND file_key = ?`, nextAttemptAt, reason, messageID, fileKey)
	return err
}

// ResourceMessagesDue returns message ids with pending or retryable
// resources, newest messages first.
func (s *Store) ResourceMessagesDue(ctx context.Context, now int64, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.message_id FROM resources r JOIN messages m ON m.message_id = r.message_id
 WHERE (r.status = 'pending' OR (r.status = 'failed' AND r.next_attempt_at > 0 AND r.next_attempt_at <= ?))
 ORDER BY m.create_ms DESC LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ResourcesFor lists the resources of one message.
func (s *Store) ResourcesFor(ctx context.Context, messageID string) ([]Resource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+resourceColumns+` FROM resources WHERE message_id = ? ORDER BY file_key`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResourceCounts summarizes resource states.
func (s *Store) ResourceCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, count(*) FROM resources GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ReadStatusCandidates returns messages from others newer than sinceMs whose
// remote read flag is unknown or unread and whose next check is due.
func (s *Store) ReadStatusCandidates(ctx context.Context, self string, sinceMs, now int64, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.message_id FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id
 WHERE m.sender_id <> ? AND m.deleted = 0 AND m.create_ms > ? AND COALESCE(r.is_read_remote, 0) = 0 AND COALESCE(r.next_check_at, 0) <= ?
 ORDER BY m.create_ms DESC LIMIT ?`, self, sinceMs, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetReadStatus stores the remote read flag (nil = still unknown) and the
// next check time, leaving consumed_at alone.
func (s *Store) SetReadStatus(ctx context.Context, messageID string, isRead *bool, checkedAt, nextCheckAt int64) error {
	var v sql.NullBool
	if isRead != nil {
		v = sql.NullBool{Bool: *isRead, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO read_state (message_id, is_read_remote, remote_checked_at, check_count, next_check_at)
 VALUES (?, ?, ?, 1, ?)
 ON CONFLICT(message_id) DO UPDATE SET is_read_remote = excluded.is_read_remote, remote_checked_at = excluded.remote_checked_at,
   check_count = read_state.check_count + 1, next_check_at = excluded.next_check_at`, messageID, v, checkedAt, nextCheckAt)
	return err
}

// ReadCheckCount returns how many times a message's remote flag was checked.
func (s *Store) ReadCheckCount(ctx context.Context, messageID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT check_count FROM read_state WHERE message_id = ?), 0)`, messageID).Scan(&n)
	return n, err
}

// UnreadCount counts messages Feishu reports as unread by the user.
func (s *Store) UnreadCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM messages m JOIN read_state r ON r.message_id = m.message_id WHERE r.is_read_remote = 0 AND m.deleted = 0`).Scan(&n)
	return n, err
}

// UnreadCountsByChat returns per-chat counts of messages Feishu reports as
// unread by the user.
func (s *Store) UnreadCountsByChat(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.chat_id, count(*) FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE r.is_read_remote = 0 AND m.deleted = 0 GROUP BY m.chat_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ScanRow is a message summary for the resource back-scan.
type ScanRow struct {
	ID         int64
	MessageID  string
	MsgType    string
	ContentRaw string
}

// MessagesAfterIDForScan returns live media-bearing messages ingested after
// rowID, oldest first, so resources of messages stored before downloads
// existed can be registered.
func (s *Store) MessagesAfterIDForScan(ctx context.Context, rowID int64, limit int) ([]ScanRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, message_id, msg_type, content_raw FROM messages
 WHERE id > ? AND deleted = 0 AND msg_type IN ('image','file','audio','media','video','post') ORDER BY id LIMIT ?`, rowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanRow
	for rows.Next() {
		var r ScanRow
		if err := rows.Scan(&r.ID, &r.MessageID, &r.MsgType, &r.ContentRaw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
