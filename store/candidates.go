package store

import (
	"context"
	json "encoding/json/v2"
)

// Candidate is one draft triage wrote for an urgent source message: a reply
// to send, or a reaction to put on the source. The writer is the process
// holding daemon.lock; the TUI reads and clears, so unlike drafts this table is
// counted by the data_rev triggers.
type Candidate struct {
	Mid    string // source message the drafts answer
	ChatID string
	// Text is the reply, empty on a reaction candidate.
	Text string
	// Reaction is the emoji_type to react with, spelled the way Feishu takes
	// it, and empty on a reply candidate.
	Reaction  string
	Format    string // text | markdown on a reply, empty on a reaction
	CreatedMs int64
	// Answered reports that the reader has spoken in this chat after the
	// candidate was drafted, or reacted to the source, so the picker draws it
	// dimmed.
	Answered bool
}

// answeredSince is the Answered predicate, binding the reader's open id twice:
// the reader has spoken in chat after since, or reacted to the source message
// mid. The chat-list marker, the message view and triage's own skips must agree
// on what is still pending, so all of them take it from here, each anchoring it
// on its own columns.
//
// The reaction half matches text the way namesPerson does, which rests on
// reactions_json being stored minified.
func answeredSince(chat, since, mid string) string {
	return `(EXISTS(SELECT 1 FROM messages
	               WHERE chat_id = ` + chat + ` AND sender_id = ? AND create_ms > ` + since + `
	                 AND deleted = 0)
	 OR EXISTS(SELECT 1 FROM messages
	           WHERE message_id = ` + mid + `
	             AND instr(reactions_json, '"operator_id":"' || ? || '"') > 0))`
}

// candidateAnswered anchors answeredSince on a draft_candidates row.
var candidateAnswered = answeredSince("draft_candidates.chat_id", "draft_candidates.created_ms", "draft_candidates.mid")

// ChatCandidates expands the draft_candidates rows of one chat into one
// Candidate per draft, ordered oldest pending first, then the replies in draft
// order and the reactions after them: a reply is the substantive answer. The
// JSON columns are written minimal, so a decode failure drops what that column
// held rather than the row. An empty chatID lists every chat — the CLI's view;
// the TUI always names its chat.
func (s *Store) ChatCandidates(ctx context.Context, chatID, selfID string) ([]Candidate, error) {
	query := `SELECT mid, chat_id, draft, format, extras, reactions, created_ms, ` + candidateAnswered + `
	 FROM draft_candidates`
	args := []any{selfID, selfID}
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
		var mid, rowChat, draft, format, extras, reactions string
		var created int64
		var answered bool
		if err := rows.Scan(&mid, &rowChat, &draft, &format, &extras, &reactions, &created, &answered); err != nil {
			return nil, err
		}
		var rest, keys []string
		if json.Unmarshal([]byte(extras), &rest) != nil {
			rest = nil
		}
		if json.Unmarshal([]byte(reactions), &keys) != nil {
			keys = nil
		}
		base := Candidate{Mid: mid, ChatID: rowChat, Format: format, CreatedMs: created, Answered: answered}
		for _, text := range append([]string{draft}, rest...) {
			if text != "" {
				c := base
				c.Text = text
				out = append(out, c)
			}
		}
		for _, key := range keys {
			c := base
			c.Reaction, c.Format = key, ""
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// PutCandidates stores the drafts for one source message: reply 0 in draft
// (” when there are only reactions), the other replies in extras and the
// reactions in reactions, both as minimal JSON. A same-mid re-draft overwrites.
func (s *Store) PutCandidates(ctx context.Context, mid, chatID string, drafts, reactions []string, format string, nowMs int64) error {
	var first string
	var rest []string
	if len(drafts) > 0 {
		first, rest = drafts[0], drafts[1:]
	}
	extras, err := json.Marshal(rest)
	if err != nil {
		return err
	}
	keys, err := json.Marshal(reactions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO draft_candidates(mid, chat_id, draft, format, extras, reactions, created_ms) VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(mid) DO UPDATE SET chat_id = excluded.chat_id, draft = excluded.draft,
		   format = excluded.format, extras = excluded.extras, reactions = excluded.reactions,
		   created_ms = excluded.created_ms`,
		mid, chatID, first, format, string(extras), string(keys), nowMs)
	return err
}

// CandidateChats counts the draft_candidates rows per chat the reader has not
// answered, feeding the chat-list marker. Chats with none are absent from the
// map.
func (s *Store) CandidateChats(ctx context.Context, selfID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT chat_id, COUNT(*) FROM draft_candidates WHERE NOT `+candidateAnswered+` GROUP BY chat_id`, selfID, selfID)
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
