package store

import (
	"context"
	"database/sql"
	"slices"
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

func scanResource(sc scanner) (Resource, error) {
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
	return queryAll(ctx, s.db, scanOne[string], `SELECT DISTINCT r.message_id FROM resources r JOIN messages m ON m.message_id = r.message_id
 WHERE (r.status = 'pending' OR (r.status = 'failed' AND r.next_attempt_at > 0 AND r.next_attempt_at <= ?))
 ORDER BY m.create_ms DESC LIMIT ?`, now, limit)
}

// ResourcesFor lists the resources of one message.
func (s *Store) ResourcesFor(ctx context.Context, messageID string) ([]Resource, error) {
	return queryAll(ctx, s.db, scanResource, `SELECT `+resourceColumns+` FROM resources WHERE message_id = ? ORDER BY file_key`, messageID)
}

// ResourceCounts summarizes resource states.
func (s *Store) ResourceCounts(ctx context.Context) (map[string]int64, error) {
	return queryCounts(ctx, s.db, `SELECT status, count(*) FROM resources GROUP BY status`)
}

// ReadCheckQuery selects messages worth asking Feishu about: those from
// others whose remote read flag is unknown or unread.
type ReadCheckQuery struct {
	// Self is the user's open_id; their own messages carry no read flag.
	Self string
	// SinceMs bounds how far back to look, matching the polling horizon.
	SinceMs int64
	// DueAt keeps a message out until its scheduled check; 0 asks now,
	// which is how opening a chat overtakes the backoff.
	DueAt int64
	// ChatID narrows the query to one chat.
	ChatID string
	Limit  int
}

// ReadStatusCandidates returns the message ids q selects, newest first.
func (s *Store) ReadStatusCandidates(ctx context.Context, q ReadCheckQuery) ([]string, error) {
	sql := `SELECT m.message_id ` + messageFrom + `
 WHERE m.sender_id <> ? AND m.deleted = 0 AND m.create_ms > ? AND COALESCE(r.is_read_remote, 0) = 0`
	args := []any{q.Self, q.SinceMs}
	if q.DueAt > 0 {
		sql += ` AND COALESCE(r.next_check_at, 0) <= ?`
		args = append(args, q.DueAt)
	}
	if q.ChatID != "" {
		sql += ` AND m.chat_id = ?`
		args = append(args, q.ChatID)
	}
	sql += ` ORDER BY m.create_ms DESC LIMIT ?`
	return queryAll(ctx, s.db, scanOne[string], sql, append(args, q.Limit)...)
}

// ExpireReadStatus relaxes to unknown the unread flags of messages older than
// beforeMs, which the polling horizon has put out of reach. Left at 0 they
// would claim "unread" forever on evidence that can no longer be refreshed.
func (s *Store) ExpireReadStatus(ctx context.Context, beforeMs int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE read_state SET is_read_remote = NULL, next_check_at = 0
 WHERE is_read_remote = 0 AND EXISTS (SELECT 1 FROM messages m WHERE m.message_id = read_state.message_id AND m.create_ms <= ?)`, beforeMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

// unreadBadge is what the chat list treats as unread: a live main-flow
// message Feishu still reports as unseen. Thread replies are out — a thread
// exists so that answering an old topic does not pull the whole chat back
// into everyone's view — so neither the counter nor the chat's place in the
// list moves for one.
const unreadBadge = `r.is_read_remote = 0 AND m.deleted = 0 AND m.message_position >= 0`

// UnreadCount is every message Feishu still reports as unread, thread replies
// included: the sync backlog behind the read-status poller, not what the chat
// list badges.
func (s *Store) UnreadCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM messages m JOIN read_state r ON r.message_id = m.message_id WHERE r.is_read_remote = 0 AND m.deleted = 0`).Scan(&n)
	return n, err
}

// UnreadCountsByChat returns the per-chat badge counts. Muted chats are in
// it: do-not-disturb decides how the counter is drawn, not whether it exists.
func (s *Store) UnreadCountsByChat(ctx context.Context) (map[string]int64, error) {
	return queryCounts(ctx, s.db, `SELECT m.chat_id, count(*) FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE `+unreadBadge+` GROUP BY m.chat_id`)
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
	return queryAll(ctx, s.db, func(sc scanner) (ScanRow, error) {
		var r ScanRow
		err := sc.Scan(&r.ID, &r.MessageID, &r.MsgType, &r.ContentRaw)
		return r, err
	}, `SELECT id, message_id, msg_type, content_raw FROM messages
 WHERE id > ? AND deleted = 0 AND msg_type IN ('image','file','audio','media','video','post') ORDER BY id LIMIT ?`, rowID, limit)
}

// ResourcesForMessages lists the resources of many messages at once, keyed by
// message id; messages without attachments are absent from the map.
func (s *Store) ResourcesForMessages(ctx context.Context, messageIDs []string) (map[string][]Resource, error) {
	out := make(map[string][]Resource, len(messageIDs))
	for chunk := range slices.Chunk(messageIDs, 500) {
		rows, err := queryAll(ctx, s.db, scanResource, `SELECT `+resourceColumns+` FROM resources WHERE message_id IN `+inClause(len(chunk))+` ORDER BY message_id, file_key`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.MessageID] = append(out[r.MessageID], r)
		}
	}
	return out, nil
}
