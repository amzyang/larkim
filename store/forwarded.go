package store

import (
	"context"
	"database/sql"
)

// Forwarded is one message inside a merged-forward bundle. It is not a copy:
// MessageID and ChatID name the original message in the chat it was sent to,
// which is usually not the chat the bundle landed in and is often one larkim
// has never synced. That is why these rows live here and not in messages.
type Forwarded struct {
	RootMessageID  string `json:"root_message_id"`
	UpperMessageID string `json:"upper_message_id"`
	MessageID      string `json:"message_id"`
	Seq            int    `json:"seq"`
	ChatID         string `json:"chat_id"`
	MsgType        string `json:"msg_type"`
	SenderID       string `json:"sender_id"`
	SenderName     string `json:"sender_name"`
	CreateMs       int64  `json:"create_ms"`
	ContentRaw     string `json:"content_raw"`
	MentionsJSON   string `json:"mentions_json,omitempty"`
	RawJSON        string `json:"-"`
}

// ForwardRoot is one bundle's queue row: whether it has been expanded, how
// many children the top level holds, and the backoff behind a failure.
type ForwardRoot struct {
	RootMessageID string `json:"root_message_id"`
	// FetchedAt is when the bundle stopped being worth asking about, whether
	// because it expanded or because Feishu refused it for good. Zero means
	// still owed.
	FetchedAt     int64  `json:"fetched_at"`
	ChildCount    int    `json:"child_count"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt int64  `json:"next_attempt_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

const forwardedColumns = `root_message_id, upper_message_id, message_id, seq, chat_id, msg_type,
 sender_id, sender_name, create_ms, content_raw, mentions_json, raw_json`

func scanForwarded(sc scanner) (Forwarded, error) {
	var f Forwarded
	err := sc.Scan(&f.RootMessageID, &f.UpperMessageID, &f.MessageID, &f.Seq, &f.ChatID, &f.MsgType,
		&f.SenderID, &f.SenderName, &f.CreateMs, &f.ContentRaw, &f.MentionsJSON, &f.RawJSON)
	return f, err
}

const forwardRootColumns = `root_message_id, fetched_at, child_count, attempts, next_attempt_at, last_error`

func scanForwardRoot(sc scanner) (ForwardRoot, error) {
	var r ForwardRoot
	err := sc.Scan(&r.RootMessageID, &r.FetchedAt, &r.ChildCount, &r.Attempts, &r.NextAttemptAt, &r.LastError)
	return r, err
}

// AddForwardRoots queues bundles for expansion. A bundle already queued keeps
// the state it has, expanded or refused: the ids arrive again on every
// listing that re-reads its overlap.
func (s *Store) AddForwardRoots(ctx context.Context, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range messageIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO forwarded_roots (root_message_id) VALUES (?)
 ON CONFLICT(root_message_id) DO NOTHING`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ForwardRootsDue returns the bundles worth asking about now, newest bundle
// first: the newest forwards are the ones a reader is about to open, and the
// queue is short enough that this is the whole of the scheduling.
//
// fetched_at is what settles a row, so a bundle Feishu refused for good drops
// out on that alone — next_attempt_at going back to zero would otherwise read
// as "ask immediately".
func (s *Store) ForwardRootsDue(ctx context.Context, now int64, limit int) ([]ForwardRoot, error) {
	return queryAll(ctx, s.db, scanForwardRoot, `SELECT `+forwardRootColumns+` FROM forwarded_roots f
 JOIN messages m ON m.message_id = f.root_message_id
 WHERE f.fetched_at = 0 AND f.next_attempt_at <= ? AND m.deleted = 0
 ORDER BY m.create_ms DESC LIMIT ?`, now, limit)
}

// GetForwardRoot reads one bundle's queue row.
func (s *Store) GetForwardRoot(ctx context.Context, rootMessageID string) (ForwardRoot, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+forwardRootColumns+` FROM forwarded_roots WHERE root_message_id = ?`, rootMessageID)
	r, err := scanForwardRoot(row)
	if err == sql.ErrNoRows {
		return ForwardRoot{}, ErrNotFound
	}
	return r, err
}

// SaveForwarded replaces a bundle's children and settles its queue row. The
// children are written whole rather than merged: a bundle is frozen, so a
// second answer is the same answer, and replacing keeps a retry idempotent.
//
// child_count counts the top level alone — the children of the bundle itself,
// not of the bundles nested inside it — because that is what one frame lists.
func (s *Store) SaveForwarded(ctx context.Context, rootMessageID string, kids []Forwarded, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM forwarded_messages WHERE root_message_id = ?`, rootMessageID); err != nil {
		return err
	}
	top := 0
	for _, k := range kids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO forwarded_messages (`+forwardedColumns+`)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			rootMessageID, k.UpperMessageID, k.MessageID, k.Seq, k.ChatID, k.MsgType,
			k.SenderID, k.SenderName, k.CreateMs, k.ContentRaw, compactJSON(k.MentionsJSON), k.RawJSON); err != nil {
			return err
		}
		if k.UpperMessageID == rootMessageID {
			top++
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE forwarded_roots
 SET fetched_at = ?, child_count = ?, attempts = 0, next_attempt_at = 0, last_error = ''
 WHERE root_message_id = ?`, now, top, rootMessageID); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkForwardRefused settles a bundle nobody will expand: the reader left the
// source chat, or Feishu will not hand this one over. A forward is frozen, so
// asking again cannot change the answer — the row is stamped fetched and the
// summary line reads last_error to say the bundle cannot be opened.
func (s *Store) MarkForwardRefused(ctx context.Context, rootMessageID, reason string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE forwarded_roots
 SET fetched_at = ?, attempts = attempts + 1, next_attempt_at = 0, last_error = ?
 WHERE root_message_id = ?`, now, reason, rootMessageID)
	return err
}

// MarkForwardFailed records one attempt that failed for a reason worth
// retrying and when the next one is owed.
func (s *Store) MarkForwardFailed(ctx context.Context, rootMessageID, reason string, nextAttemptAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE forwarded_roots
 SET attempts = attempts + 1, next_attempt_at = ?, last_error = ?
 WHERE root_message_id = ?`, nextAttemptAt, reason, rootMessageID)
	return err
}

// ForwardChildren lists one level of a bundle: the messages whose direct
// parent is upperMessageID, which is the bundle's own id at the top level.
// One level is one frame — a nested bundle comes back as a single row of type
// merge_forward, and opening it asks for its own children.
func (s *Store) ForwardChildren(ctx context.Context, rootMessageID, upperMessageID string) ([]Forwarded, error) {
	return queryAll(ctx, s.db, scanForwarded, `SELECT `+forwardedColumns+` FROM forwarded_messages
 WHERE root_message_id = ? AND upper_message_id = ? ORDER BY seq`, rootMessageID, upperMessageID)
}
