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
	RepairedAt      int64  `json:"repaired_at,omitempty"`
	RawJSON         string `json:"-"`

	// Newest main-flow message, kept in step by UpsertMessages and
	// UpdateRendered so a listing needs no per-chat subquery.
	LastMessageID  string `json:"last_message_id,omitempty"`
	LastMessageMs  int64  `json:"last_message_ms"`
	LastSenderID   string `json:"last_sender_id,omitempty"`
	LastSenderName string `json:"last_sender_name,omitempty"`
	LastSenderType string `json:"last_sender_type,omitempty"`
	LastMsgType    string `json:"last_msg_type,omitempty"`
	LastContent    string `json:"last_content,omitempty"`
	LastContentRaw string `json:"-"`
	// LastMentionsJSON is that message's rendered mentions, the same
	// `[{key,id,name}]` messages.mentions_json holds.
	LastMentionsJSON string `json:"last_mentions_json,omitempty"`
	// LastReactionsJSON is that message's reaction block, the same shape
	// messages.reactions_json holds.
	LastReactionsJSON string `json:"last_reactions_json,omitempty"`
	LastRenderedAt    int64  `json:"last_rendered_at,omitempty"`
	LastDeleted       bool   `json:"last_deleted,omitempty"`
	// LastUnsilencedMs is the newest main-flow message the silence rules
	// left alone, and the key the list orders on: noise changes what the
	// row says, never where it sits. Zero when every message is silenced,
	// which sinks the chat to the bottom with the empty ones.
	LastUnsilencedMs int64 `json:"last_unsilenced_ms"`

	// Muted is the user's do-not-disturb setting, which only a lookup of its
	// own reports; MuteCheckedAt stamps that lookup's last answer.
	Muted         bool  `json:"muted,omitempty"`
	MuteCheckedAt int64 `json:"mute_checked_at,omitempty"`

	// Derived for listings.
	MessageCount int64 `json:"message_count"`
	// UnreadCount is the badge: main-flow messages Feishu still reports as
	// unseen that no larkim reader has had in front of them, silenced ones
	// left out. Only ListChats fills it; it rides along with the row so a
	// chat's number and its place always come from the same read.
	UnreadCount int64 `json:"unread_count"`
	// PeerAccount is the p2p peer's tenant account address; empty for groups
	// and for peers whose identity lookup has not run.
	PeerAccount string `json:"peer_account,omitempty"`
	// PeerAvatarPath is the p2p peer's downloaded avatar; the chat's own
	// avatar columns stay empty for p2p.
	PeerAvatarPath string `json:"peer_avatar_path,omitempty"`
}

// PeerSuffix disambiguates same-named colleagues in a p2p chat's title.
func (c Chat) PeerSuffix() string { return AccountSuffix(c.PeerAccount) }

// AvatarFile is the chat's picture relative to the data dir: its own for a
// group, the peer's for p2p. Empty when there is none to draw, including the
// sentinels for "no avatar" and "download gave up".
func (c Chat) AvatarFile() string {
	if c.ChatMode == "p2p" {
		return avatarFile(c.PeerAvatarPath)
	}
	return avatarFile(c.AvatarPath)
}

// AvatarSeed is the identity a drawn stand-in belongs to, which for p2p is the
// peer rather than the chat: the chat list and a message block have to land on
// the same colour for the same person, and only the peer's id is common to
// both. Mirrors AvatarFile, whose picture already comes from the peer.
func (c Chat) AvatarSeed() string {
	if c.ChatMode == "p2p" && c.P2PTargetID != "" {
		return c.P2PTargetID
	}
	return c.ChatID
}

// chatColumns selects from `chats c`; the derived columns are named so
// callers can order by them.
const chatColumns = `c.chat_id, c.name, c.description, c.chat_mode, c.chat_status, c.owner_id, c.external, c.p2p_target_id, c.p2p_target_type,
 c.avatar_url, c.avatar_path, c.cursor_ms, c.backfill_done_at, c.members_synced_at, c.first_seen_at, c.last_seen_at, c.left_at, c.sync_error, c.repaired_at, c.raw_json,
 c.last_message_id, c.last_message_ms, c.last_sender_id, c.last_sender_name, c.last_sender_type, c.last_msg_type, c.last_content, c.last_content_raw, c.last_mentions_json, c.last_reactions_json, c.last_rendered_at, c.last_deleted, c.last_unsilenced_ms,
 c.muted, c.mute_checked_at,
 (SELECT count(*) FROM messages m WHERE m.chat_id = c.chat_id) AS message_count,
 COALESCE(NULLIF(ct.enterprise_email, ''), ct.email, '') AS peer_account,
 COALESCE(ct.avatar_path, '') AS peer_avatar_path`

// chatDest are the scan targets for chatColumns, in order.
func chatDest(c *Chat) []any {
	return []any{&c.ChatID, &c.Name, &c.Description, &c.ChatMode, &c.ChatStatus, &c.OwnerID, &c.External, &c.P2PTargetID, &c.P2PTargetType,
		&c.AvatarURL, &c.AvatarPath, &c.CursorMs, &c.BackfillDoneAt, &c.MembersSyncedAt, &c.FirstSeenAt, &c.LastSeenAt, &c.LeftAt, &c.SyncError, &c.RepairedAt, &c.RawJSON,
		&c.LastMessageID, &c.LastMessageMs, &c.LastSenderID, &c.LastSenderName, &c.LastSenderType, &c.LastMsgType, &c.LastContent, &c.LastContentRaw, &c.LastMentionsJSON, &c.LastReactionsJSON, &c.LastRenderedAt, &c.LastDeleted, &c.LastUnsilencedMs,
		&c.Muted, &c.MuteCheckedAt,
		&c.MessageCount, &c.PeerAccount, &c.PeerAvatarPath}
}

func scanChat(sc scanner) (Chat, error) {
	var c Chat
	return c, sc.Scan(chatDest(&c)...)
}

// scanListChat reads chatColumns plus the unread count ListChats appends.
func scanListChat(sc scanner) (Chat, error) {
	var c Chat
	return c, sc.Scan(append(chatDest(&c), &c.UnreadCount)...)
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
	return s.queryChats(ctx, `WHERE c.backfill_done_at = 0 AND c.left_at = 0 ORDER BY c.last_message_ms DESC, c.last_seen_at DESC LIMIT ?`, limit)
}

// ChatsNeedingMute returns up to limit chats whose do-not-disturb setting has
// not been looked up since beforeMs, least recently checked first so a run
// bounded by the API's batch size still comes round to every one of them.
// Chats with nothing since activeAfterMs are left out: the list only marks
// conversations a person is still reading.
func (s *Store) ChatsNeedingMute(ctx context.Context, activeAfterMs, beforeMs int64, limit int) ([]Chat, error) {
	return s.queryChats(ctx, `WHERE c.left_at = 0 AND c.last_message_ms >= ? AND c.mute_checked_at < ?
 ORDER BY c.mute_checked_at, c.last_message_ms DESC LIMIT ?`, activeAfterMs, beforeMs, limit)
}

// SetMuteStatus records a mute lookup. unknown are the chats it declined to
// answer for; they keep their last known setting but are stamped all the
// same, so one unanswerable chat cannot hold the rotation in place.
func (s *Store) SetMuteStatus(ctx context.Context, muted map[string]bool, unknown []string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for chatID, m := range muted {
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET muted = ?, mute_checked_at = ? WHERE chat_id = ?`, m, now, chatID); err != nil {
			return fmt.Errorf("set mute %s: %w", chatID, err)
		}
	}
	for _, chatID := range unknown {
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET mute_checked_at = ? WHERE chat_id = ?`, now, chatID); err != nil {
			return fmt.Errorf("stamp mute %s: %w", chatID, err)
		}
	}
	return tx.Commit()
}

// GetChat loads one chat.
func (s *Store) GetChat(ctx context.Context, chatID string) (Chat, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats c LEFT JOIN contacts ct ON ct.open_id = c.p2p_target_id WHERE c.chat_id = ?`, chatID)
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

// unreadJoin counts each chat's badge, on the same rule the badge is drawn
// by. One grouped pass rather than a correlated subquery per row, and it
// rides in the listing itself so a chat's number and its place cannot come
// from two different revisions of the database.
const unreadJoin = `LEFT JOIN (SELECT m.chat_id, count(*) AS n FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE ` + unreadCounted + ` GROUP BY m.chat_id) u ON u.chat_id = c.chat_id `

// ListChats returns the chats newest message first. Unread does not lift a
// chat: reading one is news about the reader, not about the chat, and a sort
// key the cursor flips by landing on a row rearranges the list under the
// hand that is browsing it.
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
	tail := unreadJoin
	if len(where) > 0 {
		tail += "WHERE " + strings.Join(where, " AND ") + " "
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	// chat_id closes the order: the ms keys are 0 for every chat with no
	// message yet, which is most of them, and a name is neither unique nor
	// stable, so without it equal rows come back in whatever order the
	// sorter happens to produce.
	tail += "ORDER BY c.last_unsilenced_ms DESC, c.last_message_ms DESC, c.name, c.chat_id LIMIT ?"
	args = append(args, limit)
	return queryAll(ctx, s.db, scanListChat,
		`SELECT `+chatColumns+`, COALESCE(u.n, 0) AS unread_count FROM chats c LEFT JOIN contacts ct ON ct.open_id = c.p2p_target_id `+tail, args...)
}

func (s *Store) queryChats(ctx context.Context, tail string, args ...any) ([]Chat, error) {
	return queryAll(ctx, s.db, scanChat, `SELECT `+chatColumns+` FROM chats c LEFT JOIN contacts ct ON ct.open_id = c.p2p_target_id `+tail, args...)
}

// FindChatsByName returns chats whose name equals ref ignoring whitespace and case.
func (s *Store) FindChatsByName(ctx context.Context, ref string) ([]Chat, error) {
	chats, err := s.ListChats(ctx, ChatQuery{})
	if err != nil {
		return nil, err
	}
	want := FoldName(ref)
	var out []Chat
	for _, c := range chats {
		if FoldName(c.Name) == want {
			out = append(out, c)
		}
	}
	return out, nil
}

// FoldName normalizes a display name for equality: whitespace removed, lower case.
func FoldName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// chatSummaryQuery selects the newest main-flow message of a chat. Thread replies
// carry a negative message_position and are left out, so the list shows what
// the chat's main flow shows; thread roots have a non-negative position and
// do count.
const chatSummaryQuery = `SELECT message_id, create_ms, sender_id, sender_name, sender_type, msg_type, content, content_raw, mentions_json, reactions_json, rendered_at, deleted
 FROM messages WHERE chat_id = ? AND message_position >= 0
 ORDER BY create_ms DESC, message_position DESC, id DESC LIMIT 1`

// chatSummarySortKey is the newest main-flow message the silence rules left
// alone. It is a second query rather than a column of the first, because the
// message the list shows and the message that decides the chat's place are
// not the same one once a rule matches.
const chatSummarySortKey = `SELECT COALESCE(max(create_ms), 0) FROM messages
 WHERE chat_id = ? AND message_position >= 0 AND silenced = 0`

const chatSummaryUpdate = `UPDATE chats SET
 last_message_id = ?, last_message_ms = ?, last_sender_id = ?, last_sender_name = ?, last_sender_type = ?,
 last_msg_type = ?, last_content = ?, last_content_raw = ?, last_mentions_json = ?, last_reactions_json = ?, last_rendered_at = ?, last_deleted = ?,
 last_unsilenced_ms = ?
 WHERE chat_id = ?`

// refreshChatSummary recomputes one chat's cold-stored newest message inside
// tx. A chat with no main-flow message has its summary cleared rather than
// left stale, which is what "New chat" renders from.
func refreshChatSummary(ctx context.Context, tx *sql.Tx, chatID string) error {
	var c Chat
	err := tx.QueryRowContext(ctx, chatSummaryQuery, chatID).Scan(
		&c.LastMessageID, &c.LastMessageMs, &c.LastSenderID, &c.LastSenderName, &c.LastSenderType,
		&c.LastMsgType, &c.LastContent, &c.LastContentRaw, &c.LastMentionsJSON, &c.LastReactionsJSON, &c.LastRenderedAt, &c.LastDeleted)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("chat summary %s: %w", chatID, err)
	}
	if err := tx.QueryRowContext(ctx, chatSummarySortKey, chatID).Scan(&c.LastUnsilencedMs); err != nil {
		return fmt.Errorf("chat sort key %s: %w", chatID, err)
	}
	_, err = tx.ExecContext(ctx, chatSummaryUpdate,
		c.LastMessageID, c.LastMessageMs, c.LastSenderID, c.LastSenderName, c.LastSenderType,
		c.LastMsgType, c.LastContent, c.LastContentRaw, c.LastMentionsJSON, c.LastReactionsJSON, c.LastRenderedAt, c.LastDeleted,
		c.LastUnsilencedMs, chatID)
	return err
}
