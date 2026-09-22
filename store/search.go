package store

import (
	"context"
	"strings"
)

// SearchMessages finds messages whose rendered content or sender name
// contains every whitespace-separated term, newest first. Terms of three or
// more characters use the trigram FTS index; shorter ones (common in Chinese)
// fall back to a substring scan, so every query returns exact substring hits.
func (s *Store) SearchMessages(ctx context.Context, query, chatID string, limit int) ([]Message, error) {
	var ftsTerms, shortTerms []string
	for _, t := range strings.Fields(query) {
		if len([]rune(t)) >= 3 {
			ftsTerms = append(ftsTerms, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		} else {
			shortTerms = append(shortTerms, t)
		}
	}
	if len(ftsTerms)+len(shortTerms) == 0 {
		return nil, nil
	}
	var where []string
	var args []any
	q := `SELECT ` + messageColumns + ` FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id`
	if len(ftsTerms) > 0 {
		q = `SELECT ` + messageColumns + ` FROM messages_fts f JOIN messages m ON m.id = f.rowid LEFT JOIN read_state r ON r.message_id = m.message_id`
		where = append(where, `messages_fts MATCH ?`)
		args = append(args, strings.Join(ftsTerms, " AND "))
	}
	for _, t := range shortTerms {
		where = append(where, `(instr(lower(m.content), lower(?)) > 0 OR instr(lower(m.sender_name), lower(?)) > 0)`)
		args = append(args, t, t)
	}
	where = append(where, `m.deleted = 0`)
	if chatID != "" {
		where = append(where, `m.chat_id = ?`)
		args = append(args, chatID)
	}
	if limit <= 0 {
		limit = 50
	}
	q += ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY m.create_ms DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
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

// ChatsForRepair returns chats with messages newer than sinceMs that were not
// repaired since startedAt, most active first.
func (s *Store) ChatsForRepair(ctx context.Context, sinceMs, startedAt int64, limit int) ([]Chat, error) {
	return s.queryChats(ctx, `WHERE c.left_at = 0 AND c.sync_error = '' AND c.repaired_at < ?
 AND EXISTS (SELECT 1 FROM messages m WHERE m.chat_id = c.chat_id AND m.create_ms > ?) ORDER BY 21 DESC LIMIT ?`, startedAt, sinceMs, limit)
}

// SetChatRepaired stamps a completed repair for a chat.
func (s *Store) SetChatRepaired(ctx context.Context, chatID string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET repaired_at = ? WHERE chat_id = ?`, now, chatID)
	return err
}

// ChatsNeedingMembers returns chats whose member list is older than beforeMs,
// most recently active first.
func (s *Store) ChatsNeedingMembers(ctx context.Context, beforeMs int64, limit int) ([]Chat, error) {
	return s.queryChats(ctx, `WHERE c.left_at = 0 AND c.sync_error = '' AND c.chat_mode <> 'p2p' AND c.members_synced_at < ? ORDER BY 21 DESC LIMIT ?`, beforeMs, limit)
}

// SetChatMembers replaces a chat's member list and stamps members_synced_at.
func (s *Store) SetChatMembers(ctx context.Context, chatID string, members []Contact, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_members WHERE chat_id = ?`, chatID); err != nil {
		return err
	}
	for _, m := range members {
		typ := "user"
		if m.IsBot {
			typ = "bot"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_members (chat_id, member_id, member_type, seen_at) VALUES (?, ?, ?, ?)`, chatID, m.OpenID, typ, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET members_synced_at = ? WHERE chat_id = ?`, now, chatID); err != nil {
		return err
	}
	return tx.Commit()
}

// ChatMemberCount returns how many members are recorded for a chat.
func (s *Store) ChatMemberCount(ctx context.Context, chatID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM chat_members WHERE chat_id = ?`, chatID).Scan(&n)
	return n, err
}

// ContactsNeedingAvatar returns user contacts without an avatar URL that are
// worth fetching: p2p partners and recent senders first.
func (s *Store) ContactsNeedingAvatar(ctx context.Context, limit int) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+` FROM contacts c WHERE c.is_bot = 0 AND c.avatar_url = ''
 ORDER BY (SELECT max(create_ms) FROM messages m WHERE m.sender_id = c.open_id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetContactAvatar records the avatar URL of a contact.
func (s *Store) SetContactAvatar(ctx context.Context, openID, url string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE contacts SET avatar_url = ?, updated_at = ? WHERE open_id = ?`, url, now, openID)
	return err
}

// AvatarNone marks a contact known to have no fetchable avatar; AvatarFailed
// marks a download that will not be retried.
const (
	AvatarNone   = "none"
	AvatarFailed = "-"
)

// AvatarsToDownload lists chats and contacts whose avatar URL has no local copy yet.
func (s *Store) AvatarsToDownload(ctx context.Context, limit int) (chats []Chat, contacts []Contact, err error) {
	chats, err = s.queryChats(ctx, `WHERE c.avatar_url NOT IN ('', 'none') AND c.avatar_path = '' AND c.left_at = 0 ORDER BY 21 DESC LIMIT ?`, limit)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+` FROM contacts WHERE avatar_url NOT IN ('', 'none') AND avatar_path = '' LIMIT ?`, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, nil, err
		}
		contacts = append(contacts, c)
	}
	return chats, contacts, rows.Err()
}

// SetChatAvatarPath / SetContactAvatarPath record a downloaded avatar
// (relative to the data dir); an empty path with a reason marks failure so the
// URL is not retried every tick.
func (s *Store) SetChatAvatarPath(ctx context.Context, chatID, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET avatar_path = ? WHERE chat_id = ?`, path, chatID)
	return err
}

func (s *Store) SetContactAvatarPath(ctx context.Context, openID, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE contacts SET avatar_path = ? WHERE open_id = ?`, path, openID)
	return err
}
