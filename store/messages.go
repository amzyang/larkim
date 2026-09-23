package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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

// messageFrom is the FROM clause every message query selects messageColumns from.
const messageFrom = `FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id`

func scanMessage(sc scanner) (Message, error) {
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

// UnrenderedMessageIDs returns up to limit live message ids that still need
// rendering, newest first. Messages with attachments still to download are
// left out: the download step renders them in the same lark-cli call.
func (s *Store) UnrenderedMessageIDs(ctx context.Context, limit int) ([]string, error) {
	return queryAll(ctx, s.db, scanOne[string], `SELECT m.message_id FROM messages m WHERE m.rendered_at = 0 AND m.deleted = 0
 AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.message_id = m.message_id AND r.status IN ('pending', 'failed'))
 ORDER BY m.create_ms DESC LIMIT ?`, limit)
}

// UnknownMessageIDs filters ids down to those not yet stored, preserving order.
func (s *Store) UnknownMessageIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for chunk := range slices.Chunk(ids, 500) {
		q := `SELECT message_id FROM messages WHERE message_id IN ` + inClause(len(chunk))
		found, err := queryAll(ctx, s.db, scanOne[string], q, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			known[id] = true
		}
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
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` `+messageFrom+` WHERE m.message_id = ?`, messageID)
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return m, ErrNotFound
	}
	return m, err
}

// MessageQuery filters ListMessages. Zero values mean "no filter".
type MessageQuery struct {
	ChatID   string
	ThreadID string
	SenderID string
	MsgType  string
	SinceMs  int64
	UntilMs  int64
	// BeforeID and AfterID page by cursor: an exclusive anchor compared on
	// the canonical sort key, so consecutive pages neither repeat nor drop a
	// row when several messages share a millisecond. Desc picks which side
	// of the anchor the page walks into.
	BeforeID string
	AfterID  string
	// ExcludeThreadReplies drops the folded replies of a chat's threads, so
	// a limit counts only what the caller will keep. A reply is any message
	// whose position is negative; the API picks the sentinel.
	ExcludeThreadReplies bool
	IncludeDeleted       bool
	Unread               bool // is_read_remote = 0
	Unconsumed           bool // consumed_at = 0
	Desc                 bool
	Limit                int
	Offset               int
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
	for _, c := range []struct {
		id string
		op string
	}{{q.BeforeID, "<"}, {q.AfterID, ">"}} {
		if c.id == "" {
			continue
		}
		anchor, err := s.GetMessage(ctx, c.id)
		if err != nil {
			return nil, fmt.Errorf("cursor %s: %w", c.id, err)
		}
		add("(m.create_ms, m.message_position, m.id) "+c.op+" (?, ?, ?)", anchor.CreateMs, anchor.MessagePosition, anchor.ID)
	}
	if q.ExcludeThreadReplies {
		add("m.message_position >= 0")
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
	query := `SELECT ` + messageColumns + ` ` + messageFrom
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	order := "ASC"
	if q.Desc {
		order = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY m.create_ms %s, m.message_position %s, m.id %s", order, order, order)
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, q.Offset)
	return queryAll(ctx, s.db, scanMessage, query, args...)
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

// ThreadReplyCounts counts the live replies of each thread id in a chat,
// keyed by thread id; threads with no reply are absent. A reply is any
// message whose position is negative: the API hands out several such
// sentinels, so the sign is the only thing to test. The stored rows are
// counted rather than a loaded page, which would undercount a thread whose
// root is older than the page.
func (s *Store) ThreadReplyCounts(ctx context.Context, chatID string, threadIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(threadIDs))
	for chunk := range slices.Chunk(threadIDs, 500) {
		args := append([]any{chatID}, anySlice(chunk)...)
		counts, err := queryCounts(ctx, s.db, `SELECT thread_id, count(*) FROM messages
 WHERE chat_id = ? AND message_position < 0 AND deleted = 0 AND thread_id IN `+inClause(len(chunk))+` GROUP BY thread_id`, args...)
		if err != nil {
			return nil, err
		}
		for id, n := range counts {
			out[id] = int(n)
		}
	}
	return out, nil
}
