package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// KeySilenceRev is the sync_state row holding the fingerprint of the rules
// the stored silenced flags were computed from.
const KeySilenceRev = "silence_rev"

// SilenceRule names messages that must not pull at the reader: they keep
// their place in the chat and in every query, but stay out of the unread
// badge and out of the key the chat list orders on. The fields it sets are an
// AND and an unset field matches anything, so a rule with nothing set would
// silence the whole account — Validate refuses that one.
type SilenceRule struct {
	Chat     string `yaml:"chat" json:"chat,omitempty"`
	Sender   string `yaml:"sender" json:"sender,omitempty"`
	Contains string `yaml:"contains" json:"contains,omitempty"`
}

// SilenceRules is the configured set; a message matching any rule is silenced.
type SilenceRules []SilenceRule

// Validate rejects a rule that matches every message.
func (rs SilenceRules) Validate() error {
	for i, r := range rs {
		if r.Chat == "" && r.Sender == "" && r.Contains == "" {
			return fmt.Errorf("silence rule %d: set at least one of chat, sender, contains", i+1)
		}
	}
	return nil
}

// Fingerprint identifies the rule set, so a rescan runs exactly when the
// rules the stored flags came from are no longer the configured ones.
func (rs SilenceRules) Fingerprint() string {
	h := sha256.New()
	for _, r := range rs {
		fmt.Fprintf(h, "%q\x00%q\x00%q\n", r.Chat, r.Sender, r.Contains)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// silenceBody is what a contains rule reads: the human-readable rendering
// once lark-cli has produced it, the raw body until then. A rule therefore
// holds from a message's first tick instead of from the moment the render
// queue reaches it, which for a backfilled range can be minutes later.
const silenceBody = `CASE WHEN m.content <> '' THEN m.content ELSE m.content_raw END`

// where is the rule as a predicate over `messages m`. An empty rule yields an
// empty string; Validate keeps those out of a configured set.
func (r SilenceRule) where() (string, []any) {
	var conds []string
	var args []any
	if r.Chat != "" {
		conds = append(conds, "m.chat_id = ?")
		args = append(args, r.Chat)
	}
	if r.Sender != "" {
		conds = append(conds, "m.sender_id = ?")
		args = append(args, r.Sender)
	}
	if r.Contains != "" {
		conds = append(conds, "instr(lower("+silenceBody+"), lower(?)) > 0")
		args = append(args, r.Contains)
	}
	return strings.Join(conds, " AND "), args
}

// match is the whole set as one predicate; ok is false when no rule is
// configured, which no SQL fragment can express.
func (rs SilenceRules) match() (pred string, args []any, ok bool) {
	var parts []string
	for _, r := range rs {
		w, a := r.where()
		if w == "" {
			continue
		}
		parts = append(parts, "("+w+")")
		args = append(args, a...)
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	return strings.Join(parts, " OR "), args, true
}

// applySilence stamps the flag on the given messages inside tx. It clears
// before it sets, so an edited body, a rendering that no longer matches or a
// rule that was dropped un-silences a message on the same path that silenced
// it. Both statements skip rows already carrying the value they would write,
// which keeps data_rev — and with it every consumer's reload — quiet.
func (s *Store) applySilence(ctx context.Context, tx *sql.Tx, ids []string) error {
	pred, pargs, ok := s.Silence.match()
	for chunk := range slices.Chunk(ids, 500) {
		in := inClause(len(chunk))
		clear := `UPDATE messages AS m SET silenced = 0 WHERE m.message_id IN ` + in + ` AND m.silenced = 1`
		args := anySlice(chunk)
		if ok {
			clear += ` AND NOT (` + pred + `)`
			args = append(args, pargs...)
		}
		if _, err := tx.ExecContext(ctx, clear, args...); err != nil {
			return fmt.Errorf("clear silence: %w", err)
		}
		if !ok {
			continue
		}
		set := `UPDATE messages AS m SET silenced = 1 WHERE m.message_id IN ` + in + ` AND m.silenced = 0 AND (` + pred + `)`
		if _, err := tx.ExecContext(ctx, set, append(anySlice(chunk), pargs...)...); err != nil {
			return fmt.Errorf("set silence: %w", err)
		}
	}
	return nil
}

// ReapplySilence rebuilds every flag when the configured rules differ from
// the ones on disk, and reports how many messages changed. The pass is the
// only thing that makes an edited config reach messages already stored; it is
// gated on the fingerprint because it walks the whole table.
func (s *Store) ReapplySilence(ctx context.Context) (int64, error) {
	want := s.Silence.Fingerprint()
	have, _, err := s.GetState(ctx, KeySilenceRev)
	if err != nil {
		return 0, err
	}
	if have == want {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	pred, pargs, ok := s.Silence.match()
	clear := `UPDATE messages AS m SET silenced = 0 WHERE m.silenced = 1`
	var clearArgs []any
	if ok {
		clear += ` AND NOT (` + pred + `)`
		clearArgs = pargs
	}
	res, err := tx.ExecContext(ctx, clear, clearArgs...)
	if err != nil {
		return 0, fmt.Errorf("clear silence: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if ok {
		res, err = tx.ExecContext(ctx, `UPDATE messages AS m SET silenced = 1 WHERE m.silenced = 0 AND (`+pred+`)`, pargs...)
		if err != nil {
			return 0, fmt.Errorf("set silence: %w", err)
		}
		set, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		n += set
	}

	chatIDs, err := txStrings(ctx, tx, `SELECT chat_id FROM chats`)
	if err != nil {
		return 0, err
	}
	for _, chatID := range chatIDs {
		if err := refreshChatSummary(ctx, tx, chatID); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_state(key, value) VALUES (?, ?)
 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, KeySilenceRev, want); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// SilenceMatches counts the stored messages one rule silences and dates the
// newest of them. A rule naming an id that does not exist fails silently by
// construction, so `larkim silence` reports this instead of nothing.
func (s *Store) SilenceMatches(ctx context.Context, r SilenceRule) (count, lastMs int64, err error) {
	where, args := r.where()
	if where == "" {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*), COALESCE(max(m.create_ms), 0) FROM messages m WHERE `+where, args...).
		Scan(&count, &lastMs)
	return count, lastMs, err
}

// txStrings reads a single-column query inside a transaction.
func txStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
