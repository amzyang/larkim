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
	q := `SELECT ` + messageColumns + ` ` + messageFrom
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
	return queryAll(ctx, s.db, scanMessage, q, args...)
}

// ChatsForRepair returns chats with messages newer than sinceMs that were not
// repaired since startedAt, most active first.
func (s *Store) ChatsForRepair(ctx context.Context, sinceMs, startedAt int64, limit int) ([]Chat, error) {
	return s.queryChats(ctx, `WHERE c.left_at = 0 AND c.sync_error = '' AND c.repaired_at < ?
 AND EXISTS (SELECT 1 FROM messages m WHERE m.chat_id = c.chat_id AND m.create_ms > ?) ORDER BY c.last_message_ms DESC LIMIT ?`, startedAt, sinceMs, limit)
}

// SetChatRepaired stamps a completed repair for a chat.
func (s *Store) SetChatRepaired(ctx context.Context, chatID string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET repaired_at = ? WHERE chat_id = ?`, now, chatID)
	return err
}

// ChatsNeedingMembers returns chats whose member list is older than beforeMs,
// most recently active first.
func (s *Store) ChatsNeedingMembers(ctx context.Context, beforeMs int64, limit int) ([]Chat, error) {
	return s.queryChats(ctx, `WHERE c.left_at = 0 AND c.sync_error = '' AND c.chat_mode <> 'p2p' AND c.members_synced_at < ? ORDER BY c.last_message_ms DESC LIMIT ?`, beforeMs, limit)
}

// SetChatMembers replaces a chat's member list and stamps members_synced_at.
// truncated records that the server capped the list it came from, so a reader
// is never shown a part of a roster as the whole of it.
func (s *Store) SetChatMembers(ctx context.Context, chatID string, members []Contact, truncated bool, now int64) error {
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
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET members_synced_at = ?, members_truncated = ? WHERE chat_id = ?`, now, truncated, chatID); err != nil {
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
	return queryAll(ctx, s.db, scanContact, `SELECT `+contactColumns+` FROM contacts c WHERE c.is_bot = 0 AND c.avatar_url = ''
 ORDER BY (SELECT max(create_ms) FROM messages m WHERE m.sender_id = c.open_id) DESC LIMIT ?`, limit)
}

// BotRef pairs a bot's contact id with the app it belongs to.
type BotRef struct {
	OpenID string
	AppID  string
}

// BotsNeedingAvatar returns bots whose avatar is still unknown, newest
// speaker first. A bot's picture is its app's icon, and the only place the
// app id appears is the sender of a message it sent, so a bot that has never
// spoken is simply not listed.
func (s *Store) BotsNeedingAvatar(ctx context.Context, limit int) ([]BotRef, error) {
	return queryAll(ctx, s.db, func(sc scanner) (BotRef, error) {
		var b BotRef
		err := sc.Scan(&b.OpenID, &b.AppID)
		return b, err
	}, `SELECT c.open_id, json_extract(m.raw_json, '$.sender.id')
 FROM contacts c JOIN messages m ON m.sender_id = c.open_id AND m.sender_type = 'app'
 WHERE c.is_bot = 1 AND c.avatar_url = '' AND json_extract(m.raw_json, '$.sender.id_type') = 'app_id'
 GROUP BY c.open_id ORDER BY max(m.create_ms) DESC LIMIT ?`, limit)
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

// avatarFile is a stored avatar path with the sentinels taken out, leaving
// only a path there is a picture behind.
func avatarFile(path string) string {
	if path == AvatarNone || path == AvatarFailed {
		return ""
	}
	return path
}

// AvatarsToDownload lists chats and contacts whose avatar URL has no local copy yet.
func (s *Store) AvatarsToDownload(ctx context.Context, limit int) (chats []Chat, contacts []Contact, err error) {
	chats, err = s.queryChats(ctx, `WHERE c.avatar_url NOT IN ('', 'none') AND c.avatar_path = '' AND c.left_at = 0 ORDER BY c.last_message_ms DESC LIMIT ?`, limit)
	if err != nil {
		return nil, nil, err
	}
	contacts, err = queryAll(ctx, s.db, scanContact, `SELECT `+contactColumns+` FROM contacts WHERE avatar_url NOT IN ('', 'none') AND avatar_path = '' LIMIT ?`, limit)
	if err != nil {
		return nil, nil, err
	}
	return chats, contacts, nil
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

// MentionsOf returns the messages naming the reader, newest first, across
// every chat. The marker on a chat row says somebody is waiting; this is the
// list that says who, and it is answered from mentions_json alone — no round
// trip, since the rendering pass already resolved every @ into an open id.
//
// Unread and read alike: a mention the reader has already seen is still the
// thing they were asked about, and dropping it the moment the chat is opened
// would empty the list exactly when it is being used. Silenced messages are
// left out, on the same rule as the badge: a rule that says "do not pull me by
// this" holds here too.
func (s *Store) MentionsOf(ctx context.Context, self string, limit int) ([]Message, error) {
	if self == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	return queryAll(ctx, s.db, scanMessage,
		`SELECT `+messageColumns+` `+messageFrom+
			` WHERE m.deleted = 0 AND m.silenced = 0 AND `+namesSelf+
			` ORDER BY m.create_ms DESC LIMIT ?`, self, limit)
}
