package store

import (
	"context"
	json "encoding/json/v2"
)

// Candidate is one reply draft triage wrote for an urgent source message.
// The writer is the process holding daemon.lock; the TUI reads and clears, so
// unlike drafts this table is counted by the data_rev triggers.
type Candidate struct {
	Mid       string // source message the drafts answer
	ChatID    string
	Text      string
	Format    string // text | markdown, applies to all candidates of the mid
	CreatedMs int64
	// Replied reports that the reader has spoken in this chat after the
	// candidate was drafted, so the picker draws it dimmed.
	Replied bool
}

// repliedPast is the Replied predicate, binding the reader's open id. The
// chat-list marker and the message view must agree on what is still pending,
// so both queries take it from here.
const repliedPast = `EXISTS(SELECT 1 FROM messages
	               WHERE chat_id = draft_candidates.chat_id
	                 AND sender_id = ? AND create_ms > draft_candidates.created_ms
	                 AND deleted = 0)`

// ChatCandidates expands the draft_candidates rows of one chat into one
// Candidate per draft, ordered oldest pending first, draft order within a mid.
// The extras column is written as minimal JSON, so a decode failure drops the
// later candidates rather than the row. An empty chatID lists every chat —
// the CLI's view; the TUI always names its chat.
func (s *Store) ChatCandidates(ctx context.Context, chatID, selfID string) ([]Candidate, error) {
	query := `SELECT mid, chat_id, draft, format, extras, created_ms, ` + repliedPast + `
	 FROM draft_candidates`
	args := []any{selfID}
	if chatID != "" {
		query += ` WHERE chat_id = ?`
		args = append(args, chatID)
	}
	query += ` ORDER BY created_ms, mid`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var mid, rowChat, draft, format, extras string
		var created int64
		var replied bool
		if err := rows.Scan(&mid, &rowChat, &draft, &format, &extras, &created, &replied); err != nil {
			return nil, err
		}
		var rest []string
		if json.Unmarshal([]byte(extras), &rest) != nil {
			rest = nil
		}
		for _, text := range append([]string{draft}, rest...) {
			out = append(out, Candidate{
				Mid: mid, ChatID: rowChat, Text: text, Format: format,
				CreatedMs: created, Replied: replied,
			})
		}
	}
	return out, rows.Err()
}

// PutCandidates stores the drafts for one source message: candidate 0 in
// draft, the rest in extras as minimal JSON. A same-mid re-draft overwrites.
func (s *Store) PutCandidates(ctx context.Context, mid, chatID string, drafts []string, format string, nowMs int64) error {
	extras, err := json.Marshal(drafts[1:])
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO draft_candidates(mid, chat_id, draft, format, extras, created_ms) VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(mid) DO UPDATE SET chat_id = excluded.chat_id, draft = excluded.draft,
		   format = excluded.format, extras = excluded.extras, created_ms = excluded.created_ms`,
		mid, chatID, drafts[0], format, string(extras), nowMs)
	return err
}

// CandidateChats counts the draft_candidates rows per chat the reader has not
// replied past, feeding the chat-list marker. Chats with none are absent from
// the map.
func (s *Store) CandidateChats(ctx context.Context, selfID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT chat_id, COUNT(*) FROM draft_candidates WHERE NOT `+repliedPast+` GROUP BY chat_id`, selfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var chatID string
		var n int
		if err := rows.Scan(&chatID, &n); err != nil {
			return nil, err
		}
		out[chatID] = n
	}
	return out, rows.Err()
}

// ClearCandidate drops one mid's drafts, which is what the TUI does after a
// send left a composer it filled with one of them.
func (s *Store) ClearCandidate(ctx context.Context, mid string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM draft_candidates WHERE mid = ?`, mid)
	return err
}
