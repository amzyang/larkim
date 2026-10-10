package store

import (
	"context"
)

// Triage is the verdict on one arrival and how far the work it set off has
// got. The process holding daemon.lock is its only writer.
type Triage struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
	Level     string `json:"level"`
	Reason    string `json:"reason"`
	// JevP is Jev's attention probability, nil when a rule decided.
	JevP       *float64 `json:"jev_p,omitempty"`
	JudgedMs   int64    `json:"judged_ms"`
	DraftedMs  int64    `json:"drafted_ms,omitzero"`
	DraftTries int      `json:"draft_tries,omitzero"`
	NotifiedMs int64    `json:"notified_ms,omitzero"`
}

// TriageEntry is a verdict with what a reader needs to recognise the message.
type TriageEntry struct {
	Triage
	ChatName   string `json:"chat_name"`
	SenderName string `json:"sender_name"`
	CreateMs   int64  `json:"create_ms"`
	Content    string `json:"content"`
	// ContentRaw and RenderedAt let a caller read a card or a message not
	// rendered yet the way the rest of larkim does.
	ContentRaw string `json:"-"`
	RenderedAt int64  `json:"-"`
}

// TriageQuery narrows ListTriage.
type TriageQuery struct {
	ChatID string
	Level  string
	Limit  int
}

// Untriaged is what is waiting to be judged: rendered, live, unsilenced
// messages created at or after sinceMs that have no verdict yet, oldest first.
// The window is what keeps a backfill from being judged as news, and the
// anti-join is what makes a restart resume rather than repeat.
func (s *Store) Untriaged(ctx context.Context, sinceMs int64, limit int) ([]Message, error) {
	return queryAll(ctx, s.db, scanMessage, `SELECT `+messageColumns+` `+messageFrom+`
 WHERE m.rendered_at > 0 AND m.deleted = 0 AND m.silenced = 0 AND m.create_ms >= ?
   AND NOT EXISTS (SELECT 1 FROM triage t WHERE t.message_id = m.message_id)
 ORDER BY m.create_ms, m.message_position, m.id LIMIT ?`, sinceMs, limit)
}

// PutTriage records a verdict. A message is judged once: a second verdict for
// the same id is dropped, so two passes racing over one arrival agree.
func (s *Store) PutTriage(ctx context.Context, t Triage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO triage(message_id, chat_id, level, reason, jev_p, judged_ms)
 VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(message_id) DO NOTHING`,
		t.MessageID, t.ChatID, t.Level, t.Reason, t.JevP, t.JudgedMs)
	return err
}

const triageColumns = `t.message_id, t.chat_id, t.level, t.reason, t.jev_p, t.judged_ms, t.drafted_ms, t.draft_tries, t.notified_ms`

// triageDest is where triageColumns scan into. JevP is scanned as a pointer,
// which database/sql leaves nil for NULL.
func triageDest(t *Triage) []any {
	return []any{&t.MessageID, &t.ChatID, &t.Level, &t.Reason, &t.JevP, &t.JudgedMs, &t.DraftedMs, &t.DraftTries, &t.NotifiedMs}
}

func scanTriage(sc scanner) (Triage, error) {
	var t Triage
	err := sc.Scan(triageDest(&t)...)
	return t, err
}

// TriageToNotify is every P0 verdict whose banner is still owed, oldest first.
func (s *Store) TriageToNotify(ctx context.Context) ([]Triage, error) {
	return queryAll(ctx, s.db, scanTriage, `SELECT `+triageColumns+` FROM triage t
 WHERE t.level = 'P0' AND t.notified_ms = 0 ORDER BY t.judged_ms, t.message_id`)
}

// MarkTriageNotified stamps the banner as done, raised or suppressed alike.
func (s *Store) MarkTriageNotified(ctx context.Context, messageID string, nowMs int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE triage SET notified_ms = ? WHERE message_id = ?`, nowMs, messageID)
	return err
}

// TriageToDraft is every P0 verdict still owed a draft that has failed fewer
// than maxTries times, oldest first.
func (s *Store) TriageToDraft(ctx context.Context, maxTries int) ([]Triage, error) {
	return queryAll(ctx, s.db, scanTriage, `SELECT `+triageColumns+` FROM triage t
 WHERE t.level = 'P0' AND t.drafted_ms = 0 AND t.draft_tries < ? ORDER BY t.judged_ms, t.message_id`, maxTries)
}

// MarkTriageDrafted stamps the draft as done, whatever it produced.
func (s *Store) MarkTriageDrafted(ctx context.Context, messageID string, nowMs int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE triage SET drafted_ms = ? WHERE message_id = ?`, nowMs, messageID)
	return err
}

// FailTriageDraft counts one failed draft against the message.
func (s *Store) FailTriageDraft(ctx context.Context, messageID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE triage SET draft_tries = draft_tries + 1 WHERE message_id = ?`, messageID)
	return err
}

// ListTriage lists verdicts newest first with the message they judged.
func (s *Store) ListTriage(ctx context.Context, q TriageQuery) ([]TriageEntry, error) {
	query := `SELECT ` + triageColumns + `, COALESCE(c.name, ''), COALESCE(m.sender_name, ''),
 COALESCE(m.create_ms, 0), COALESCE(m.content, ''), COALESCE(m.content_raw, ''), COALESCE(m.rendered_at, 0)
 FROM triage t LEFT JOIN messages m ON m.message_id = t.message_id LEFT JOIN chats c ON c.chat_id = t.chat_id
 WHERE (? = '' OR t.chat_id = ?) AND (? = '' OR t.level = ?)
 ORDER BY t.judged_ms DESC, t.message_id DESC LIMIT ?`
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	return queryAll(ctx, s.db, func(sc scanner) (TriageEntry, error) {
		var e TriageEntry
		err := sc.Scan(append(triageDest(&e.Triage),
			&e.ChatName, &e.SenderName, &e.CreateMs, &e.Content, &e.ContentRaw, &e.RenderedAt)...)
		return e, err
	}, query, q.ChatID, q.ChatID, q.Level, q.Level, limit)
}

// RepliedSince reports whether self has sent a live message in the chat after
// sinceMs: the reader already answered, so a banner would only repeat it.
func (s *Store) RepliedSince(ctx context.Context, chatID, self string, sinceMs int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages
 WHERE chat_id = ? AND sender_id = ? AND create_ms > ? AND deleted = 0)`, chatID, self, sinceMs).Scan(&ok)
	return ok, err
}

// Reminder is a banner a message asked for at a later time.
type Reminder struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
	FireMs    int64  `json:"fire_ms"`
	Title     string `json:"title"`
	FiredMs   int64  `json:"fired_ms,omitzero"`
}

// PutReminder plans a reminder. A replan of the same message replaces the
// plan and owes the banner again.
func (s *Store) PutReminder(ctx context.Context, r Reminder) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO reminders(message_id, chat_id, fire_ms, title) VALUES(?, ?, ?, ?)
 ON CONFLICT(message_id) DO UPDATE SET chat_id = excluded.chat_id, fire_ms = excluded.fire_ms,
   title = excluded.title, fired_ms = 0`, r.MessageID, r.ChatID, r.FireMs, r.Title)
	return err
}

// DueReminders is every owed reminder due at or before nowMs, earliest first.
func (s *Store) DueReminders(ctx context.Context, nowMs int64) ([]Reminder, error) {
	return queryAll(ctx, s.db, func(sc scanner) (Reminder, error) {
		var r Reminder
		err := sc.Scan(&r.MessageID, &r.ChatID, &r.FireMs, &r.Title, &r.FiredMs)
		return r, err
	}, `SELECT message_id, chat_id, fire_ms, title, fired_ms FROM reminders
 WHERE fired_ms = 0 AND fire_ms <= ? ORDER BY fire_ms, message_id`, nowMs)
}

// MarkReminderFired stamps a reminder as done, raised or dropped as too late.
func (s *Store) MarkReminderFired(ctx context.Context, messageID string, nowMs int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE reminders SET fired_ms = ? WHERE message_id = ?`, nowMs, messageID)
	return err
}
