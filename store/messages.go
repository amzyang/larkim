package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Message is one row of messages, joined with read_state for queries.
type Message struct {
	ID              int64  `json:"-"`
	MessageID       string `json:"message_id"`
	ChatID          string `json:"chat_id"`
	MsgType         string `json:"msg_type"`
	SenderID        string `json:"sender_id"`
	SenderType      string `json:"sender_type"`
	SenderName      string `json:"sender_name"`
	ContentRaw      string `json:"content_raw"`
	Content         string `json:"content"`
	CreateMs        int64  `json:"create_ms"`
	UpdateMs        int64  `json:"update_ms"`
	MessagePosition int64  `json:"message_position"`
	Updated         bool   `json:"updated"`
	Deleted         bool   `json:"deleted"`
	DeletedSeenAt   int64  `json:"deleted_seen_at,omitempty"`
	ThreadID        string `json:"thread_id,omitempty"`
	ReplyTo         string `json:"reply_to,omitempty"`
	MentionsJSON    string `json:"mentions_json,omitempty"`
	ReactionsJSON   string `json:"reactions_json,omitempty"`
	RawJSON         string `json:"-"`
	RenderedAt      int64  `json:"rendered_at"`
	FirstSeenAt     int64  `json:"first_seen_at"`
	LastSeenAt      int64  `json:"last_seen_at"`
	// From read_state; IsReadRemote is nil when never checked.
	IsReadRemote *bool `json:"is_read_remote"`
	ConsumedAt   int64 `json:"consumed_at"`
}

const messageColumns = `m.id, m.message_id, m.chat_id, m.msg_type, m.sender_id, m.sender_type, m.sender_name,
 m.content_raw, m.content, m.create_ms, m.update_ms, m.message_position, m.updated, m.deleted, m.deleted_seen_at,
 m.thread_id, m.reply_to, m.mentions_json, m.reactions_json, m.raw_json, m.rendered_at, m.first_seen_at, m.last_seen_at,
 r.is_read_remote, COALESCE(r.consumed_at, 0)`

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var isRead sql.NullBool
	err := sc.Scan(&m.ID, &m.MessageID, &m.ChatID, &m.MsgType, &m.SenderID, &m.SenderType, &m.SenderName,
		&m.ContentRaw, &m.Content, &m.CreateMs, &m.UpdateMs, &m.MessagePosition, &m.Updated, &m.Deleted, &m.DeletedSeenAt,
		&m.ThreadID, &m.ReplyTo, &m.MentionsJSON, &m.ReactionsJSON, &m.RawJSON, &m.RenderedAt, &m.FirstSeenAt, &m.LastSeenAt,
		&isRead, &m.ConsumedAt)
	if isRead.Valid {
		v := isRead.Bool
		m.IsReadRemote = &v
	}
	return m, err
}

// UpsertMessages inserts or refreshes synced fields. Rendering columns
// (content, mentions_json, reactions_json, rendered_at) are owned by
// UpdateRendered and left untouched; a recalled message keeps its last known
// content_raw.
func (s *Store) UpsertMessages(ctx context.Context, msgs []Message, now int64) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages (message_id, chat_id, msg_type, sender_id, sender_type, sender_name,
 content_raw, create_ms, update_ms, message_position, updated, deleted, deleted_seen_at, thread_id, reply_to, raw_json, first_seen_at, last_seen_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(message_id) DO UPDATE SET
   chat_id = excluded.chat_id,
   msg_type = excluded.msg_type,
   sender_id = excluded.sender_id,
   sender_type = excluded.sender_type,
   sender_name = CASE WHEN excluded.sender_name <> '' THEN excluded.sender_name ELSE messages.sender_name END,
   content_raw = CASE WHEN excluded.deleted = 1 AND excluded.content_raw = '' THEN messages.content_raw ELSE excluded.content_raw END,
   create_ms = excluded.create_ms,
   update_ms = excluded.update_ms,
   message_position = CASE WHEN excluded.message_position <> 0 THEN excluded.message_position ELSE messages.message_position END,
   updated = excluded.updated,
   deleted = excluded.deleted,
   deleted_seen_at = CASE WHEN excluded.deleted = 1 AND messages.deleted = 0 THEN excluded.last_seen_at ELSE messages.deleted_seen_at END,
   thread_id = CASE WHEN excluded.thread_id <> '' THEN excluded.thread_id ELSE messages.thread_id END,
   reply_to = CASE WHEN excluded.reply_to <> '' THEN excluded.reply_to ELSE messages.reply_to END,
   raw_json = excluded.raw_json,
   rendered_at = CASE WHEN excluded.update_ms <> messages.update_ms THEN 0 ELSE messages.rendered_at END,
   last_seen_at = excluded.last_seen_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	n := 0
	for _, m := range msgs {
		if _, err := stmt.ExecContext(ctx, m.MessageID, m.ChatID, m.MsgType, m.SenderID, m.SenderType, m.SenderName,
			m.ContentRaw, m.CreateMs, m.UpdateMs, m.MessagePosition, m.Updated, m.Deleted, m.DeletedSeenAt, m.ThreadID, m.ReplyTo,
			m.RawJSON, now, now); err != nil {
			return n, fmt.Errorf("upsert %s: %w", m.MessageID, err)
		}
		n++
	}
	return n, tx.Commit()
}

// UpdateRendered stores the human-readable rendering of a message.
func (s *Store) UpdateRendered(ctx context.Context, messageID, content, mentionsJSON, reactionsJSON string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET content = ?, mentions_json = ?, reactions_json = ?, rendered_at = ? WHERE message_id = ?`,
		content, mentionsJSON, reactionsJSON, now, messageID)
	return err
}

// UnrenderedMessageIDs returns up to limit message ids that still need rendering, oldest first.
func (s *Store) UnrenderedMessageIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id FROM messages WHERE rendered_at = 0 AND deleted = 0 ORDER BY create_ms DESC LIMIT ?`, limit)
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

// UnknownMessageIDs filters ids down to those not yet stored, preserving order.
func (s *Store) UnknownMessageIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for chunk := range chunks(ids, 500) {
		q := `SELECT message_id FROM messages WHERE message_id IN (?` + strings.Repeat(",?", len(chunk)-1) + `)`
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			known[id] = true
		}
		rows.Close()
	}
	var out []string
	for _, id := range ids {
		if !known[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// GetMessage loads one message by id.
func (s *Store) GetMessage(ctx context.Context, messageID string) (Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id WHERE m.message_id = ?`, messageID)
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return m, ErrNotFound
	}
	return m, err
}

// MessageQuery filters ListMessages. Zero values mean "no filter".
type MessageQuery struct {
	ChatID         string
	ThreadID       string
	SenderID       string
	MsgType        string
	SinceMs        int64
	UntilMs        int64
	IncludeDeleted bool
	Unread         bool // is_read_remote = 0
	Unconsumed     bool // consumed_at = 0
	Desc           bool
	Limit          int
	Offset         int
}

// ListMessages returns messages ordered by (create_ms, message_position, id).
func (s *Store) ListMessages(ctx context.Context, q MessageQuery) ([]Message, error) {
	var where []string
	var args []any
	add := func(cond string, v ...any) { where = append(where, cond); args = append(args, v...) }
	if q.ChatID != "" {
		add("m.chat_id = ?", q.ChatID)
	}
	if q.ThreadID != "" {
		add("m.thread_id = ?", q.ThreadID)
	}
	if q.SenderID != "" {
		add("m.sender_id = ?", q.SenderID)
	}
	if q.MsgType != "" {
		add("m.msg_type = ?", q.MsgType)
	}
	if q.SinceMs > 0 {
		add("m.create_ms >= ?", q.SinceMs)
	}
	if q.UntilMs > 0 {
		add("m.create_ms <= ?", q.UntilMs)
	}
	if !q.IncludeDeleted {
		add("m.deleted = 0")
	}
	if q.Unread {
		add("r.is_read_remote = 0")
	}
	if q.Unconsumed {
		add("COALESCE(r.consumed_at, 0) = 0")
	}
	sql := `SELECT ` + messageColumns + ` FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	order := "ASC"
	if q.Desc {
		order = "DESC"
	}
	sql += fmt.Sprintf(" ORDER BY m.create_ms %s, m.message_position %s, m.id %s", order, order, order)
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	sql += " LIMIT ? OFFSET ?"
	args = append(args, limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MaxMessageRowID returns the newest ingest id; consumers poll it to detect changes.
func (s *Store) MaxMessageRowID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT max(id) FROM messages`).Scan(&id); err != nil {
		return 0, err
	}
	return id.Int64, nil
}

// MarkConsumed records local consumption of messages.
func (s *Store) MarkConsumed(ctx context.Context, ids []string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO read_state(message_id, consumed_at) VALUES (?, ?)
 ON CONFLICT(message_id) DO UPDATE SET consumed_at = CASE WHEN read_state.consumed_at = 0 THEN excluded.consumed_at ELSE read_state.consumed_at END`, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrNotFound is returned by single-row lookups.
var ErrNotFound = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }

func chunks[T any](xs []T, n int) func(yield func([]T) bool) {
	return func(yield func([]T) bool) {
		for i := 0; i < len(xs); i += n {
			end := min(i+n, len(xs))
			if !yield(xs[i:end]) {
				return
			}
		}
	}
}
