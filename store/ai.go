package store

import (
	"context"
	"encoding/json"
)

// The assistant's own tables. TUI-owned like drafts: the daemon never writes
// them, and they sit outside the data_rev triggers.

// Turn states as the TUI spells them, persisted by number. Asking is what a
// row holds while its answer streams; a row still asking at load is an answer
// the process never finished, which the TUI reads as interrupted.
const (
	AITurnAsking      = 0
	AITurnDone        = 1
	AITurnFailed      = 2
	AITurnStopped     = 3
	AITurnInterrupted = 4
)

// AISession is one conversation with the assistant, scoped to a chat. A
// session is stored when its first question is asked, not when it is opened:
// an empty one is the reader's to leave behind.
type AISession struct {
	ID, ChatID, Title string
	CreatedMs         int64
}

// AITurn is one question with its answer and the recorded context it was
// asked in. Later actions on the answer replay this record, never the live
// cursor, so what the reader asked about when they asked is the only record
// that says what an answer to it was about.
type AITurn struct {
	ID, SessionID  string
	Seq            int
	Ask, Sent      string
	AnchorID       string // the message the question was about, '' for none
	ThreadID       string // the frame standing under the panel, '' for none
	Window         int    // chat messages the question carried, 0 for the default
	Compose        string // the anchor's composer text at ask time
	Sel            []string
	State          int
	Answer, Err    string
	AtMs           int64
	// CardID is the message an answer streamed into the chat as, '' when the
	// answer stayed in the panel.
	CardID string
}

// SaveAISession writes one session row whole. Upsert, so a title learned at
// the first question lands no matter which write arrives first.
func (s *Store) SaveAISession(ctx context.Context, v AISession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_sessions(id, chat_id, title, created_ms) VALUES (?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title`,
		v.ID, v.ChatID, v.Title, v.CreatedMs)
	return err
}

// SaveAITurn writes one turn row whole: every write carries the complete
// state, so an out-of-order write cannot leave half a turn behind.
func (s *Store) SaveAITurn(ctx context.Context, v AITurn) error {
	sel, err := json.Marshal(v.Sel)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ai_turns(id, session_id, seq, ask, sent, anchor_id,
		                      thread_id, window, compose, sel, state, answer, err, at_ms, card_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   seq = excluded.seq, ask = excluded.ask, sent = excluded.sent,
		   anchor_id = excluded.anchor_id, thread_id = excluded.thread_id,
		   window = excluded.window, compose = excluded.compose,
		   sel = excluded.sel, state = excluded.state,
		   answer = excluded.answer, err = excluded.err, at_ms = excluded.at_ms,
		   card_id = excluded.card_id`,
		v.ID, v.SessionID, v.Seq, v.Ask, v.Sent, v.AnchorID,
		v.ThreadID, v.Window, v.Compose, sel, v.State, v.Answer, v.Err, v.AtMs, v.CardID)
	return err
}

// ListAISessions returns one chat's sessions in creation order.
func (s *Store) ListAISessions(ctx context.Context, chatID string) ([]AISession, error) {
	return queryAll(ctx, s.db, func(sc scanner) (AISession, error) {
		var v AISession
		err := sc.Scan(&v.ID, &v.ChatID, &v.Title, &v.CreatedMs)
		return v, err
	}, `SELECT id, chat_id, title, created_ms FROM ai_sessions
	    WHERE chat_id = ? ORDER BY created_ms, id`, chatID)
}

// ListAITurns returns one session's turns in question order.
func (s *Store) ListAITurns(ctx context.Context, sessionID string) ([]AITurn, error) {
	return queryAll(ctx, s.db, func(sc scanner) (AITurn, error) {
		var v AITurn
		var sel []byte
		err := sc.Scan(&v.ID, &v.SessionID, &v.Seq, &v.Ask, &v.Sent,
			&v.AnchorID, &v.ThreadID, &v.Window, &v.Compose, &sel,
			&v.State, &v.Answer, &v.Err, &v.AtMs, &v.CardID)
		if err != nil {
			return AITurn{}, err
		}
		if len(sel) > 0 {
			if err := json.Unmarshal(sel, &v.Sel); err != nil {
				return AITurn{}, err
			}
		}
		return v, nil
	}, `SELECT id, session_id, seq, ask, sent, anchor_id, thread_id,
	          window, compose, sel, state, answer, err, at_ms, card_id
	   FROM ai_turns WHERE session_id = ? ORDER BY seq`, sessionID)
}

// DeleteAISession drops a session and its turns together: a turn without its
// session has no chat to come back to.
func (s *Store) DeleteAISession(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_turns WHERE session_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_sessions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
