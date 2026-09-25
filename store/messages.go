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
	// Silenced is set by the configured silence rules: the message is read
	// and listed as any other, but carries no badge and does not move its
	// chat up the list.
	Silenced      bool   `json:"silenced"`
	DeletedSeenAt int64  `json:"deleted_seen_at,omitempty"`
	ThreadID      string `json:"thread_id,omitempty"`
	ReplyTo       string `json:"reply_to,omitempty"`
	MentionsJSON  string `json:"mentions_json,omitempty"`
	ReactionsJSON string `json:"reactions_json,omitempty"`
	RawJSON       string `json:"-"`
	RenderedAt    int64  `json:"rendered_at"`
	EditedAt      int64  `json:"edited_at,omitempty"`
	FirstSeenAt   int64  `json:"first_seen_at"`
	LastSeenAt    int64  `json:"last_seen_at"`
	// From read_state; IsReadRemote is nil when never checked.
	IsReadRemote *bool `json:"is_read_remote"`
	LocalReadAt  int64 `json:"local_read_at"`
}

const messageColumns = `m.id, m.message_id, m.chat_id, m.msg_type, m.sender_id, m.sender_type, m.sender_name,
 m.content_raw, m.content, m.create_ms, m.update_ms, m.message_position, m.updated, m.deleted, m.silenced, m.deleted_seen_at,
 m.thread_id, m.reply_to, m.mentions_json, m.reactions_json, m.raw_json, m.rendered_at, m.edited_at, m.first_seen_at, m.last_seen_at,
 r.is_read_remote, COALESCE(r.local_read_at, 0)`

// messageFrom is the FROM clause every message query selects messageColumns from.
const messageFrom = `FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id`

func scanMessage(sc scanner) (Message, error) {
	var m Message
	var isRead sql.NullBool
	err := sc.Scan(&m.ID, &m.MessageID, &m.ChatID, &m.MsgType, &m.SenderID, &m.SenderType, &m.SenderName,
		&m.ContentRaw, &m.Content, &m.CreateMs, &m.UpdateMs, &m.MessagePosition, &m.Updated, &m.Deleted, &m.Silenced, &m.DeletedSeenAt,
		&m.ThreadID, &m.ReplyTo, &m.MentionsJSON, &m.ReactionsJSON, &m.RawJSON, &m.RenderedAt, &m.EditedAt, &m.FirstSeenAt, &m.LastSeenAt,
		&isRead, &m.LocalReadAt)
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
   edited_at = CASE WHEN excluded.msg_type IN ('text', 'post') AND messages.content_raw <> ''
                     AND excluded.content_raw <> '' AND excluded.content_raw <> messages.content_raw
                THEN excluded.last_seen_at ELSE messages.edited_at END,
   last_seen_at = excluded.last_seen_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	n := 0
	touched := map[string]struct{}{}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if _, err := stmt.ExecContext(ctx, m.MessageID, m.ChatID, m.MsgType, m.SenderID, m.SenderType, m.SenderName,
			m.ContentRaw, m.CreateMs, m.UpdateMs, m.MessagePosition, m.Updated, m.Deleted, m.DeletedSeenAt, m.ThreadID, m.ReplyTo,
			m.RawJSON, now, now); err != nil {
			return n, fmt.Errorf("upsert %s: %w", m.MessageID, err)
		}
		touched[m.ChatID] = struct{}{}
		ids = append(ids, m.MessageID)
		n++
	}
	// Before the summaries: the sort key they compute reads the flag.
	if err := s.applySilence(ctx, tx, ids); err != nil {
		return n, err
	}
	for chatID := range touched {
		if err := refreshChatSummary(ctx, tx, chatID); err != nil {
			return n, err
		}
	}
	return n, tx.Commit()
}

// UpdateRendered stores the human-readable rendering of a message. When the
// message is its chat's newest, the chat's cold-stored summary picks up the
// rendering in the same transaction.
func (s *Store) UpdateRendered(ctx context.Context, messageID, content, mentionsJSON, reactionsJSON string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET content = ?, mentions_json = ?, reactions_json = ?, rendered_at = ? WHERE message_id = ?`,
		content, mentionsJSON, reactionsJSON, now, messageID); err != nil {
		return err
	}
	// A contains rule reads the rendering, so this is where a card first
	// becomes matchable and where an edit can stop matching.
	if err := s.applySilence(ctx, tx, []string{messageID}); err != nil {
		return err
	}
	var chatID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT chat_id FROM messages WHERE message_id = ?), '')`, messageID).Scan(&chatID); err != nil {
		return err
	}
	// The summary is recomputed rather than patched: the flag this message
	// just took decides whether it still holds the chat's sort key.
	if err := refreshChatSummary(ctx, tx, chatID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateReactions stores a message's reaction summary on its own. Reactions
// are the one part of a message that keeps changing after it is rendered —
// Feishu does not move update_time when somebody reacts, so the rendering
// pass never comes back for them — and this is the write that keeps them
// current. It deliberately leaves rendered_at and content alone: the FTS
// triggers watch content, so a reaction never churns the index, while the
// data revision trigger fires all the same and the panes reload.
//
// When the message is its chat's newest, the chat's cold-stored summary picks
// the block up in the same transaction, which is what puts a reaction on the
// chat list.
//
// An unchanged summary is not written at all: the revision trigger counts
// every UPDATE, value changed or not, and a refresh that re-states what the
// row already holds would have every open pane reload for nothing.
func (s *Store) UpdateReactions(ctx context.Context, messageID, reactionsJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET reactions_json = ? WHERE message_id = ? AND reactions_json <> ?`,
		reactionsJSON, messageID, reactionsJSON); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET last_reactions_json = ? WHERE last_message_id = ? AND last_reactions_json <> ?`,
		reactionsJSON, messageID, reactionsJSON); err != nil {
		return err
	}
	return tx.Commit()
}

// UnrenderedMessageIDs returns up to limit live message ids that still need
// rendering, newest first. Messages with attachments still to download are
// left out: the download step renders them in the same lark-cli call. The
// types larkim renders from bodies it already holds are left out too;
// UnrenderedLocalMessages owns them.
func (s *Store) UnrenderedMessageIDs(ctx context.Context, limit int) ([]string, error) {
	return queryAll(ctx, s.db, scanOne[string], `SELECT m.message_id FROM messages m
 WHERE m.rendered_at = 0 AND m.deleted = 0 AND m.msg_type NOT IN ('system', 'video_chat')
 AND NOT EXISTS (SELECT 1 FROM message_resources mr JOIN resources r ON r.file_key = mr.file_key
   WHERE mr.message_id = m.message_id AND r.status IN ('pending', 'failed'))
 ORDER BY m.create_ms DESC LIMIT ?`, limit)
}

// PendingLocalMessage is a message larkim renders itself, awaiting that
// rendering. CallRaw is the body of the newest video_chat message before it
// in the same chat, which is where Feishu leaves the length of a call that
// just ended.
type PendingLocalMessage struct {
	MessageID  string
	MsgType    string
	ContentRaw string
	CreateMs   int64
	CallRaw    string
}

// UnrenderedLocalMessages returns up to limit live messages that still need
// rendering and whose text follows from bodies already on disk, newest
// first: system messages and the calls they close.
func (s *Store) UnrenderedLocalMessages(ctx context.Context, limit int) ([]PendingLocalMessage, error) {
	scan := func(sc scanner) (PendingLocalMessage, error) {
		var m PendingLocalMessage
		err := sc.Scan(&m.MessageID, &m.MsgType, &m.ContentRaw, &m.CreateMs, &m.CallRaw)
		return m, err
	}
	return queryAll(ctx, s.db, scan, `SELECT m.message_id, m.msg_type, m.content_raw, m.create_ms, COALESCE((
   SELECT v.content_raw FROM messages v
    WHERE v.chat_id = m.chat_id AND v.msg_type = 'video_chat' AND v.create_ms <= m.create_ms
    ORDER BY v.create_ms DESC LIMIT 1), '')
 FROM messages m
 WHERE m.rendered_at = 0 AND m.deleted = 0 AND m.msg_type IN ('system', 'video_chat')
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

// MessagesByIDs loads messages by id, leaving out the ones the store has
// never seen. A recalled message still answers, because a reply quoting it
// says more with "(recalled)" than with nothing.
func (s *Store) MessagesByIDs(ctx context.Context, ids []string) (map[string]Message, error) {
	out := make(map[string]Message, len(ids))
	for chunk := range slices.Chunk(ids, 500) {
		q := `SELECT ` + messageColumns + ` ` + messageFrom + ` WHERE m.message_id IN ` + inClause(len(chunk))
		rows, err := queryAll(ctx, s.db, scanMessage, q, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			out[m.MessageID] = m
		}
	}
	return out, nil
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
