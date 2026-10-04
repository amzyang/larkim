package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// UnreadMsg is one of a chat's messages the Feishu client still counts
// unread, in position order.
type UnreadMsg struct {
	Position int64
	Silenced bool
}

// SettlePlan answers the watermark that takes the silenced unread of msgs off
// the server's count. The watermark settles everything at or below it, so the
// first unsilenced message is the fence it cannot cross: everything below it
// is settled — silenced by construction, the dot survives with a smaller
// count — and everything above keeps its unread state. push is false when
// there is nothing to settle: no unread at all, or a fence at the very head.
func SettlePlan(msgs []UnreadMsg) (watermark int64, push bool) {
	first := slices.IndexFunc(msgs, func(m UnreadMsg) bool { return !m.Silenced })
	switch {
	case len(msgs) == 0, first == 0:
		return 0, false
	case first == -1:
		return msgs[len(msgs)-1].Position, true
	default:
		return msgs[first-1].Position, true
	}
}

// SilenceSettle is one queued chat with the watermark its plan asks for.
type SilenceSettle struct {
	ChatID string
	// Position is the watermark to settle at. Push is false when the plan
	// answers no action and the row is dropped without a write.
	Position int64
	Push     bool
}

// PendingSilenceSettle answers up to limit queued chats with their plans. The
// queue only names the chats; the plan is re-derived here, from the read
// flags the sweep's probe just refreshed, so a row that waited through reads
// and arrivals settles at what is true now rather than at what queued it.
// A plan at or below the chat's settled watermark answers no action: the
// server's watermark is monotone, and Feishu's per-message read state never
// catches up with it (the settle moves the feed count alone), so without the
// floor every re-listing would queue and re-settle the same messages forever.
func (s *Store) PendingSilenceSettle(ctx context.Context, limit int) ([]SilenceSettle, error) {
	chatIDs, err := queryAll(ctx, s.db, scanOne[string],
		`SELECT chat_id FROM silence_settle_queue ORDER BY chat_id LIMIT ?`, limit)
	if err != nil || len(chatIDs) == 0 {
		return nil, err
	}
	out := make([]SilenceSettle, 0, len(chatIDs))
	for _, id := range chatIDs {
		msgs, err := s.unreadOfChat(ctx, id)
		if err != nil {
			return out, err
		}
		var settled int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE((SELECT silence_settled_pos FROM chats WHERE chat_id = ?), 0)`, id).Scan(&settled); err != nil {
			return out, err
		}
		pos, push := SettlePlan(msgs)
		if push && pos <= settled {
			push = false
		}
		out = append(out, SilenceSettle{ChatID: id, Position: pos, Push: push})
	}
	return out, nil
}

// unreadOfChat answers a chat's server-unread main-flow messages, oldest
// position first.
func (s *Store) unreadOfChat(ctx context.Context, chatID string) ([]UnreadMsg, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.message_position, m.silenced
 FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE m.chat_id = ? AND `+unreadBadge+` ORDER BY m.message_position`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnreadMsg
	for rows.Next() {
		var m UnreadMsg
		if err := rows.Scan(&m.Position, &m.Silenced); err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SilenceSettleDone drops a chat's queue row and remembers the watermark the
// settle landed, which is what keeps the messages it covered — unread to
// Feishu's per-message state forever — from queuing again. A zero position
// (a plan that answered no action) only drops the row.
func (s *Store) SilenceSettleDone(ctx context.Context, chatID string, position int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM silence_settle_queue WHERE chat_id = ?`, chatID); err != nil {
		return err
	}
	if position <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET silence_settled_pos = ?
 WHERE chat_id = ? AND silence_settled_pos < ?`, position, chatID, position)
	return err
}

// silenceSettleMaxAttempts retires a chat the gateway keeps refusing; the
// next silence flip in that chat queues it again.
const silenceSettleMaxAttempts = 10

// SilenceSettleFailed counts one failed settle and retires the chat past the
// cap, so a chat the gateway will not take cannot hold the queue.
func (s *Store) SilenceSettleFailed(ctx context.Context, chatID string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE silence_settle_queue SET attempts = attempts + 1 WHERE chat_id = ?`, chatID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM silence_settle_queue WHERE chat_id = ? AND attempts >= ?`, chatID, silenceSettleMaxAttempts)
	return err
}

// queueSilenceSettle queues the chats of the given messages that are silenced
// and still unread server-side above the chat's settled watermark. It runs
// inside the transaction that flipped the flags, so a crash cannot leave a
// flip the sweep never hears about. Rows for messages since read elsewhere
// are free: the plan answers no action.
func queueSilenceSettle(ctx context.Context, tx *sql.Tx, ids []string) error {
	for chunk := range slices.Chunk(ids, 500) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO silence_settle_queue (chat_id)
 SELECT DISTINCT m.chat_id FROM messages m JOIN read_state r ON r.message_id = m.message_id
 WHERE m.message_id IN `+inClause(len(chunk))+` AND m.silenced = 1 AND `+unreadBadge+`
   AND m.message_position > COALESCE((SELECT c.silence_settled_pos FROM chats c WHERE c.chat_id = m.chat_id), 0)`,
			anySlice(chunk)...); err != nil {
			return fmt.Errorf("queue silence settle: %w", err)
		}
	}
	return nil
}
