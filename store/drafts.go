package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Draft is one composer's unsent state. It is consumer-owned: the daemon never
// writes this table.
type Draft struct {
	ChatID string `json:"chat_id"`
	// FrameID is the right-column frame whose box holds this draft — a thread
	// id, or the root of a reply tree — and empty for the chat's own box.
	FrameID   string `json:"frame_id,omitempty"`
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to,omitempty"`
	InThread  bool   `json:"in_thread,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// Empty reports whether the draft holds nothing worth keeping, and is the only
// place that decides it. A quote alone is not worth keeping: reopening the chat
// on an empty composer that claims to be answering something is a state the
// reader never chose. Neither is whitespace, which the chat list would draw a
// marker for and the reader would find nothing behind.
func (d Draft) Empty() bool { return strings.TrimSpace(d.Text) == "" }

// LoadDraft returns the draft for one box, or the zero value when there is
// none. A missing draft is the normal case, not an error.
func (s *Store) LoadDraft(ctx context.Context, chatID, frameID string) (Draft, error) {
	d := Draft{ChatID: chatID, FrameID: frameID}
	err := s.db.QueryRowContext(ctx,
		`SELECT text, reply_to, in_thread, updated_at FROM drafts WHERE chat_id = ? AND frame_id = ?`,
		chatID, frameID).Scan(&d.Text, &d.ReplyTo, &d.InThread, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{ChatID: chatID, FrameID: frameID}, nil
	}
	return d, err
}

// SaveDraft writes one box's draft, replacing whatever was there. An empty
// draft deletes the row instead, so a cleared composer leaves no trace for the
// chat list to draw. Two TUIs on one chat: last write wins, by design.
func (s *Store) SaveDraft(ctx context.Context, d Draft, nowMs int64) error {
	if d.Empty() {
		return s.DeleteDraft(ctx, d.ChatID, d.FrameID)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO drafts(chat_id, frame_id, text, reply_to, in_thread, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(chat_id, frame_id) DO UPDATE SET
		   text = excluded.text, reply_to = excluded.reply_to,
		   in_thread = excluded.in_thread, updated_at = excluded.updated_at`,
		d.ChatID, d.FrameID, d.Text, d.ReplyTo, d.InThread, nowMs)
	return err
}

// DeleteDraft drops one box's draft, which is what a successful send does.
func (s *Store) DeleteDraft(ctx context.Context, chatID, frameID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE chat_id = ? AND frame_id = ?`, chatID, frameID)
	return err
}

// Drafts returns every stored chat draft keyed by chat, which is what the chat
// list draws its draft marker from: one query per refresh rather than one per
// row. A frame's draft belongs to its own row — see FrameDrafts.
func (s *Store) Drafts(ctx context.Context) (map[string]Draft, error) {
	return s.draftsBy(ctx, `frame_id = ''`, func(d Draft) string { return d.ChatID })
}

// FrameDrafts returns every stored frame draft keyed by frame, for the thread
// rows the chat list carries beside the chats.
func (s *Store) FrameDrafts(ctx context.Context) (map[string]Draft, error) {
	return s.draftsBy(ctx, `frame_id <> ''`, func(d Draft) string { return d.FrameID })
}

func (s *Store) draftsBy(ctx context.Context, where string, key func(Draft) string) (map[string]Draft, error) {
	rows, err := queryAll(ctx, s.db, func(sc scanner) (Draft, error) {
		var d Draft
		err := sc.Scan(&d.ChatID, &d.FrameID, &d.Text, &d.ReplyTo, &d.InThread, &d.UpdatedAt)
		return d, err
	}, `SELECT chat_id, frame_id, text, reply_to, in_thread, updated_at FROM drafts WHERE `+where)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Draft, len(rows))
	for _, d := range rows {
		out[key(d)] = d
	}
	return out, nil
}
