package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// Resource is one row of resources: a Feishu resource key and where its
// bytes landed. The key is globally unique, so one row serves every message
// that references it.
type Resource struct {
	FileKey       string `json:"file_key"`
	Type          string `json:"type"`
	LocalPath     string `json:"local_path,omitempty"` // relative to the data dir
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt int64  `json:"next_attempt_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

// ResourceRef is a message that can be asked for a key. The key identifies
// the bytes, but fetching them needs a message to ask under, so the
// references are what make a resource reachable at all.
type ResourceRef struct {
	MessageID string `json:"message_id"`
	FileKey   string `json:"file_key"`
	Type      string `json:"type"`
}

const resourceColumns = `r.file_key, r.type, r.local_path, r.size_bytes, r.status, r.attempts, r.next_attempt_at, r.last_error`

// resourceFrom is the FROM clause the due queries select from: a resource is
// ordered by the newest message that can be asked for it.
const resourceFrom = `FROM resources r
 JOIN message_resources mr ON mr.file_key = r.file_key
 JOIN messages m ON m.message_id = mr.message_id`

// resourceDue is a resource worth another attempt: never fetched, or failed
// with its scheduled retry now owed. It takes the current time as its one
// parameter.
const resourceDue = `(r.status = 'pending' OR (r.status = 'failed' AND r.next_attempt_at > 0 AND r.next_attempt_at <= ?))`

func scanResource(sc scanner) (Resource, error) {
	var r Resource
	err := sc.Scan(&r.FileKey, &r.Type, &r.LocalPath, &r.SizeBytes, &r.Status, &r.Attempts, &r.NextAttemptAt, &r.LastError)
	return r, err
}

// AddPendingResources registers keys and the messages that reference them.
// A key already known keeps the state it has: the second message to mention a
// picture inherits the download rather than asking for it again.
func (s *Store) AddPendingResources(ctx context.Context, refs []ResourceRef) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO resources (file_key, type) VALUES (?, ?)
 ON CONFLICT(file_key) DO NOTHING`, r.FileKey, r.Type); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_resources (message_id, file_key) VALUES (?, ?)
 ON CONFLICT(message_id, file_key) DO NOTHING`, r.MessageID, r.FileKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SeedResource records a key whose bytes are already on disk before any
// message references it: the file this process just uploaded. A key some
// message already registered is settled too, unless its download finished.
// The references come later, from the message's own ingest, and leave the
// row as they find it.
func (s *Store) SeedResource(ctx context.Context, fileKey, typ, localPath string, size int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO resources (file_key, type, local_path, size_bytes, status) VALUES (?, ?, ?, ?, 'done')
 ON CONFLICT(file_key) DO UPDATE SET status = 'done', local_path = excluded.local_path,
   size_bytes = excluded.size_bytes, last_error = ''
 WHERE resources.status <> 'done'`, fileKey, typ, localPath, size)
	return err
}

// MarkResourceDone records a completed download.
func (s *Store) MarkResourceDone(ctx context.Context, fileKey, localPath string, size int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET status = 'done', local_path = ?, size_bytes = ?, last_error = '' WHERE file_key = ?`, localPath, size, fileKey)
	return err
}

// MarkResourceSkipped records a resource deliberately not kept (too large).
func (s *Store) MarkResourceSkipped(ctx context.Context, fileKey string, size int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET status = 'skipped', size_bytes = ?, last_error = ? WHERE file_key = ?`, size, reason, fileKey)
	return err
}

// MarkResourceFailed records a failed attempt and when to retry; after
// maxAttempts the row becomes permanently failed (next_attempt_at = 0).
func (s *Store) MarkResourceFailed(ctx context.Context, fileKey, reason string, nextAttemptAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET status = 'failed', attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE file_key = ?`, nextAttemptAt, reason, fileKey)
	return err
}

// DueResource is a resource worth fetching and the message to ask under. The
// key identifies the bytes and is what the queue is keyed by; the message is
// the handle the endpoint needs, and the newest one referencing the key is
// taken — a key named by many messages is fetched once, not once per message.
type DueResource struct {
	Resource
	MessageID string
}

// ResourcesDue returns the resources worth an attempt now, newest first.
// Stickers are left out: nothing downloads them, copyStickers takes that
// picture out of the Lark client's own storage.
func (s *Store) ResourcesDue(ctx context.Context, now int64, limit int) ([]DueResource, error) {
	scan := func(sc scanner) (DueResource, error) {
		var d DueResource
		err := sc.Scan(&d.FileKey, &d.Type, &d.LocalPath, &d.SizeBytes, &d.Status,
			&d.Attempts, &d.NextAttemptAt, &d.LastError, &d.MessageID)
		return d, err
	}
	return queryAll(ctx, s.db, scan, `SELECT `+resourceColumns+`,
 (SELECT mr2.message_id FROM message_resources mr2 JOIN messages m2 ON m2.message_id = mr2.message_id
   WHERE mr2.file_key = r.file_key ORDER BY m2.create_ms DESC LIMIT 1) `+resourceFrom+`
 WHERE r.type <> 'sticker' AND `+resourceDue+`
 GROUP BY r.file_key ORDER BY max(m.create_ms) DESC LIMIT ?`, now, limit)
}

// StickerResourcesDue returns sticker rows still waiting for their picture,
// newest messages first.
func (s *Store) StickerResourcesDue(ctx context.Context, now int64, limit int) ([]Resource, error) {
	return queryAll(ctx, s.db, scanResource, `SELECT `+resourceColumns+` `+resourceFrom+`
 WHERE r.type = 'sticker' AND `+resourceDue+`
 GROUP BY r.file_key ORDER BY max(m.create_ms) DESC LIMIT ?`, now, limit)
}

// ResourcesFor lists the resources of one message.
func (s *Store) ResourcesFor(ctx context.Context, messageID string) ([]Resource, error) {
	return queryAll(ctx, s.db, scanResource, `SELECT `+resourceColumns+` FROM resources r
 JOIN message_resources mr ON mr.file_key = r.file_key
 WHERE mr.message_id = ? ORDER BY r.file_key`, messageID)
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

// ReadProbe is one chat's newest unread message. Unchecked says Feishu has
// never answered for it, stored unread on arrival or not, which makes the
// probe's answer its first check.
type ReadProbe struct {
	MessageID, ChatID string
	Unchecked         bool
}

// ReadStatusProbes returns one message id per chat that still has unread
// messages: the newest of them, newest chats first. Reading a chat in the
// Feishu client flips its messages in one go, so one id per chat is enough to
// notice that it happened, and 50 chats fit in the single call that asking
// about 50 messages of one chat would have cost.
//
// q.DueAt is ignored on purpose: the probe is the thing that overtakes the
// per-message backoff.
//
// A message Feishu refused to speak for is left out: it comes back among the
// invalid ids however often it is asked about, and a chat gets one slot, so
// spending it there would leave the badge that chat does show waiting on the
// ladder.
//
// An accepted-but-unconfirmed read keeps its slot so the probe can confirm or
// revert the watermark POST.
func (s *Store) ReadStatusProbes(ctx context.Context, q ReadCheckQuery) ([]ReadProbe, error) {
	scan := func(sc scanner) (ReadProbe, error) {
		var p ReadProbe
		return p, sc.Scan(&p.MessageID, &p.ChatID, &p.Unchecked)
	}
	// The bare columns belong to the max(m.create_ms) row: SQLite takes them
	// from the row its single min/max aggregate selected.
	return queryAll(ctx, s.db, scan, `SELECT message_id, chat_id, unchecked FROM
 (SELECT m.message_id AS message_id, m.chat_id AS chat_id, COALESCE(r.remote_checked_at, 0) = 0 AS unchecked, max(m.create_ms) AS newest `+messageFrom+`
  WHERE m.sender_id <> ? AND m.deleted = 0 AND m.create_ms > ?
   AND (r.message_id IS NULL OR r.is_read_remote = 0
     OR (r.is_read_remote = 1 AND r.remote_checked_at = 0))
  GROUP BY m.chat_id)
 ORDER BY newest DESC LIMIT ?`, q.Self, q.SinceMs, q.Limit)
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
// next check time.
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

// stillUnread is every live message Feishu still reports unread.
const stillUnread = `r.is_read_remote = 0 AND m.deleted = 0`

// unreadBadge is what the chat list treats as unread. Thread replies are out —
// a thread exists so that answering an old topic does not pull the whole chat
// back into everyone's view — so neither the counter nor the chat's place in
// the list moves for one.
const unreadBadge = stillUnread + ` AND m.message_position >= 0`

// unreadCounted is what the badge shows: the badge's messages, minus the ones
// a silence rule matched.
const unreadCounted = unreadBadge + ` AND m.silenced = 0`

// ChatUnread is a chat the Feishu client still has a red dot for, and the
// newest message it is drawn over.
type ChatUnread struct {
	ChatID string `json:"chat_id"`
	// Position addresses that message inside the chat. The watermark settles
	// everything up to the newest unread position rather than on its own
	// which for a deep backlog is somewhere in the middle of the history.
	Position int64 `json:"position"`
}

// ChatsWithUnread names every chat a clear would still do something for,
// oldest id first. Thread replies and recalls are out: the watermark path does
// not settle thread replies, so their receipts never flip and the chat would
// be listed on every press regardless.
func (s *Store) ChatsWithUnread(ctx context.Context) ([]ChatUnread, error) {
	return queryAll(ctx, s.db, scanChatUnread, `SELECT m.chat_id, max(m.message_position)
 FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE `+unreadBadge+` GROUP BY m.chat_id ORDER BY m.chat_id`)
}

// ChatWithUnread is ChatsWithUnread's entry for one chat, ok=false when the
// client has no dot for it.
func (s *Store) ChatWithUnread(ctx context.Context, chatID string) (ChatUnread, bool, error) {
	rows, err := queryAll(ctx, s.db, scanChatUnread, `SELECT m.chat_id, max(m.message_position)
 FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE m.chat_id = ? AND `+unreadBadge+` GROUP BY m.chat_id`, chatID)
	if err != nil || len(rows) == 0 {
		return ChatUnread{}, false, err
	}
	return rows[0], true, nil
}

func scanChatUnread(sc scanner) (ChatUnread, error) {
	var c ChatUnread
	return c, sc.Scan(&c.ChatID, &c.Position)
}

// ChatAt names the chat holding the message sent at createMs with position.
// The pair is how a chat is recognised across id spaces that share nothing
// else — the web client's numeric chat ids against the OpenAPI's oc_ ones — so
// anything short of exactly one chat is ok=false rather than a guess: a wrong
// answer here marks some other chat read.
func (s *Store) ChatAt(ctx context.Context, createMs, position int64) (string, bool, error) {
	ids, err := queryAll(ctx, s.db, func(sc scanner) (string, error) {
		var id string
		return id, sc.Scan(&id)
	}, `SELECT DISTINCT chat_id FROM messages WHERE create_ms = ? AND message_position = ? LIMIT 2`,
		createMs, position)
	if err != nil || len(ids) != 1 {
		return "", false, err
	}
	return ids[0], true, nil
}

// WebChatID is the web client's id for a chat, empty until one was matched.
func (s *Store) WebChatID(ctx context.Context, chatID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT web_chat_id FROM chats WHERE chat_id = ?`, chatID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// SetWebChatIDs records matched web ids. A chat not stored yet is skipped
// rather than created: the match came from its messages, so the row exists
// unless the chat was never synced, and a row with nothing but an id would
// be a chat the list cannot draw.
func (s *Store) SetWebChatIDs(ctx context.Context, ids map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE chats SET web_chat_id = ? WHERE chat_id = ? AND web_chat_id <> ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for chatID, webID := range ids {
		if _, err := stmt.ExecContext(ctx, webID, chatID, webID); err != nil {
			return fmt.Errorf("set web chat id %s: %w", chatID, err)
		}
	}
	return tx.Commit()
}

// UnreadAnchor is where one chat's backlog starts.
type UnreadAnchor struct {
	ChatID string
	// FirstMs is the oldest message the badge still counts. read_state rows
	// exist only for messages the poller has checked and ones stored unread on
	// arrival, so this is the oldest the badge knows about rather than the
	// oldest never read — the two have to agree for the list's count and the
	// feed's to match.
	FirstMs int64
}

// UnreadAnchors is, per chat, where its backlog starts. The predicate is
// unreadCounted, the chat list's own, so the anchor opens on exactly the set
// the badge beside that chat counts.
func (s *Store) UnreadAnchors(ctx context.Context) ([]UnreadAnchor, error) {
	scan := func(sc scanner) (UnreadAnchor, error) {
		var a UnreadAnchor
		return a, sc.Scan(&a.ChatID, &a.FirstMs)
	}
	return queryAll(ctx, s.db, scan, `SELECT m.chat_id, min(m.create_ms)
 FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE `+unreadCounted+` GROUP BY m.chat_id ORDER BY m.chat_id`)
}

// AcceptRemoteRead records a successful mark-read watermark: main-flow messages
// up to position flip to read and stay unconfirmed until the probe checks.
func (s *Store) AcceptRemoteRead(ctx context.Context, chatID string, position int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE read_state SET is_read_remote = 1, remote_checked_at = 0
 WHERE message_id IN (
   SELECT m.message_id FROM messages m JOIN read_state r ON r.message_id = m.message_id
   WHERE m.chat_id = ? AND r.is_read_remote = 0 AND m.deleted = 0
     AND m.message_position >= 0 AND m.message_position <= ?)`, chatID, position)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ConfirmAcceptedRead stamps Feishu confirmation on accepted reads without
// changing is_read_remote, so data_rev stays quiet.
func (s *Store) ConfirmAcceptedRead(ctx context.Context, chatID string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE read_state SET remote_checked_at = ?
 WHERE is_read_remote = 1 AND remote_checked_at = 0
   AND message_id IN (SELECT message_id FROM messages WHERE chat_id = ?)`, now, chatID)
	return err
}

// RevertUnconfirmedRead puts the badge back when the watermark POST lied.
func (s *Store) RevertUnconfirmedRead(ctx context.Context, chatID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE read_state SET is_read_remote = 0
 WHERE is_read_remote = 1 AND remote_checked_at = 0
   AND message_id IN (SELECT message_id FROM messages WHERE chat_id = ?)`, chatID)
	return err
}

// UnreadCount is every message Feishu still reports as unread, thread replies
// included: the sync backlog behind the read-status poller, not what the chat
// list badges.
func (s *Store) UnreadCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM messages m JOIN read_state r ON r.message_id = m.message_id WHERE r.is_read_remote = 0 AND m.deleted = 0`).Scan(&n)
	return n, err
}

// ScanRow is a message summary for the resource back-scan.
type ScanRow struct {
	ID         int64
	MessageID  string
	MsgType    string
	ContentRaw string
	Content    string
}

// MessagesAfterIDForScan returns live media-bearing messages ingested after
// rowID, oldest first, so resources of messages stored before downloads
// existed can be registered.
func (s *Store) MessagesAfterIDForScan(ctx context.Context, rowID int64, limit int) ([]ScanRow, error) {
	return queryAll(ctx, s.db, func(sc scanner) (ScanRow, error) {
		var r ScanRow
		err := sc.Scan(&r.ID, &r.MessageID, &r.MsgType, &r.ContentRaw, &r.Content)
		return r, err
	}, `SELECT id, message_id, msg_type, content_raw, content FROM messages
 WHERE id > ? AND deleted = 0 AND msg_type IN ('image','file','audio','media','video','post','sticker','interactive','merge_forward') ORDER BY id LIMIT ?`, rowID, limit)
}

// ResourcesForMessages lists the resources of many messages at once, keyed by
// message id; messages without attachments are absent from the map.
func (s *Store) ResourcesForMessages(ctx context.Context, messageIDs []string) (map[string][]Resource, error) {
	out := make(map[string][]Resource, len(messageIDs))
	for chunk := range slices.Chunk(messageIDs, 500) {
		type owned struct {
			MessageID string
			Resource
		}
		rows, err := queryAll(ctx, s.db, func(sc scanner) (owned, error) {
			var o owned
			err := sc.Scan(&o.MessageID, &o.FileKey, &o.Type, &o.LocalPath, &o.SizeBytes, &o.Status,
				&o.Attempts, &o.NextAttemptAt, &o.LastError)
			return o, err
		}, `SELECT mr.message_id, `+resourceColumns+` FROM resources r
 JOIN message_resources mr ON mr.file_key = r.file_key
 WHERE mr.message_id IN `+inClause(len(chunk))+` ORDER BY mr.message_id, r.file_key`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, o := range rows {
			out[o.MessageID] = append(out[o.MessageID], o.Resource)
		}
	}
	return out, nil
}
