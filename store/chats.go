package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Chat is one row of chats.
type Chat struct {
	ChatID          string `json:"chat_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	ChatMode        string `json:"chat_mode"`
	ChatStatus      string `json:"chat_status"`
	OwnerID         string `json:"owner_id,omitempty"`
	External        bool   `json:"external"`
	P2PTargetID     string `json:"p2p_target_id,omitempty"`
	P2PTargetType   string `json:"p2p_target_type,omitempty"`
	AvatarURL       string `json:"avatar_url,omitempty"`
	AvatarPath      string `json:"avatar_path,omitempty"`
	CursorMs        int64  `json:"cursor_ms"`
	BackfillDoneAt  int64  `json:"backfill_done_at"`
	MembersSyncedAt int64  `json:"members_synced_at"`
	FirstSeenAt     int64  `json:"first_seen_at"`
	LastSeenAt      int64  `json:"last_seen_at"`
	LeftAt          int64  `json:"left_at,omitempty"`
	SyncError       string `json:"sync_error,omitempty"`
	RawJSON         string `json:"-"`
	// Derived for listings.
	LastMessageMs int64 `json:"last_message_ms"`
	MessageCount  int64 `json:"message_count"`
}

const chatColumns = `c.chat_id, c.name, c.description, c.chat_mode, c.chat_status, c.owner_id, c.external, c.p2p_target_id, c.p2p_target_type,
 c.avatar_url, c.avatar_path, c.cursor_ms, c.backfill_done_at, c.members_synced_at, c.first_seen_at, c.last_seen_at, c.left_at, c.sync_error, c.raw_json,
 COALESCE((SELECT max(create_ms) FROM messages m WHERE m.chat_id = c.chat_id), 0),
 (SELECT count(*) FROM messages m WHERE m.chat_id = c.chat_id)`

func scanChat(sc interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	err := sc.Scan(&c.ChatID, &c.Name, &c.Description, &c.ChatMode, &c.ChatStatus, &c.OwnerID, &c.External, &c.P2PTargetID, &c.P2PTargetType,
		&c.AvatarURL, &c.AvatarPath, &c.CursorMs, &c.BackfillDoneAt, &c.MembersSyncedAt, &c.FirstSeenAt, &c.LastSeenAt, &c.LeftAt, &c.SyncError, &c.RawJSON,
		&c.LastMessageMs, &c.MessageCount)
	return c, err
}

// UpsertChats inserts or refreshes chats from a listing; sync-owned columns
// (cursor, backfill, members, avatar_path) are preserved. A chat seen again
// after leaving is revived.
func (s *Store) UpsertChats(ctx context.Context, chats []Chat, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO chats (chat_id, name, description, chat_mode, chat_status, owner_id, external, p2p_target_id, p2p_target_type,
 avatar_url, first_seen_at, last_seen_at, raw_json) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(chat_id) DO UPDATE SET
   name = excluded.name, description = excluded.description, chat_mode = excluded.chat_mode, chat_status = excluded.chat_status,
   owner_id = excluded.owner_id, external = excluded.external, p2p_target_id = excluded.p2p_target_id, p2p_target_type = excluded.p2p_target_type,
   avatar_url = excluded.avatar_url, last_seen_at = excluded.last_seen_at, left_at = 0, raw_json = excluded.raw_json`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range chats {
		if _, err := stmt.ExecContext(ctx, c.ChatID, c.Name, c.Description, c.ChatMode, c.ChatStatus, c.OwnerID, c.External, c.P2PTargetID, c.P2PTargetType,
			c.AvatarURL, now, now, c.RawJSON); err != nil {
			return fmt.Errorf("upsert chat %s: %w", c.ChatID, err)
		}
	}
	return tx.Commit()
}

// MarkChatsLeft flags chats not seen by the full listing at or after seenAt.
func (s *Store) MarkChatsLeft(ctx context.Context, seenAt int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE chats SET left_at = ? WHERE last_seen_at < ? AND left_at = 0`, seenAt, seenAt)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EnsureChat creates a placeholder for a chat first seen through a message.
func (s *Store) EnsureChat(ctx context.Context, chatID string, now int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats (chat_id, first_seen_at, last_seen_at) VALUES (?, ?, ?) ON CONFLICT(chat_id) DO NOTHING`, chatID, now, now)
	return err
}

// SetChatCursor advances the per-chat pull cursor (never backwards).
func (s *Store) SetChatCursor(ctx context.Context, chatID string, cursorMs int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET cursor_ms = max(cursor_ms, ?) WHERE chat_id = ?`, cursorMs, chatID)
	return err
}

// SetChatBackfillDone marks a chat's historical pull complete.
func (s *Store) SetChatBackfillDone(ctx context.Context, chatID string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET backfill_done_at = ?, sync_error = '' WHERE chat_id = ?`, now, chatID)
	return err
}

// SetChatSyncError records a permanent per-chat API rejection and stops the
// backfill from retrying it; the chat's messages still arrive via search.
func (s *Store) SetChatSyncError(ctx context.Context, chatID, msg string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET sync_error = ?, backfill_done_at = ? WHERE chat_id = ?`, msg, now, chatID)
	return err
}

// ChatsNeedingBackfill returns up to limit chats never backfilled; chats that
// already have discovered messages come first so active conversations fill in
// before dormant ones.
func (s *Store) ChatsNeedingBackfill(ctx context.Context, limit int) ([]Chat, error) {
	return s.queryChats(ctx, `WHERE c.backfill_done_at = 0 AND c.left_at = 0 ORDER BY 20 DESC, c.last_seen_at DESC LIMIT ?`, limit)
}

// GetChat loads one chat.
func (s *Store) GetChat(ctx context.Context, chatID string) (Chat, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats c WHERE c.chat_id = ?`, chatID)
	c, err := scanChat(row)
	if err == sql.ErrNoRows {
		return c, ErrNotFound
	}
	return c, err
}

// ChatQuery filters ListChats.
type ChatQuery struct {
	Mode        string // group | topic | p2p
	Search      string // case-insensitive substring of name
	IncludeLeft bool
	Limit       int
}

// ListChats returns chats ordered by most recent message.
func (s *Store) ListChats(ctx context.Context, q ChatQuery) ([]Chat, error) {
	var where []string
	var args []any
	if q.Mode != "" {
		where = append(where, "c.chat_mode = ?")
		args = append(args, q.Mode)
	}
	if q.Search != "" {
		where = append(where, "instr(lower(c.name), lower(?)) > 0")
		args = append(args, q.Search)
	}
	if !q.IncludeLeft {
		where = append(where, "c.left_at = 0")
	}
	sql := ""
	if len(where) > 0 {
		sql = "WHERE " + strings.Join(where, " AND ") + " "
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	sql += "ORDER BY 20 DESC, c.name LIMIT ?"
	args = append(args, limit)
	return s.queryChats(ctx, sql, args...)
}

func (s *Store) queryChats(ctx context.Context, tail string, args ...any) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+chatColumns+` FROM chats c `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
