package store

import (
	"context"
	"database/sql"
)

// Contact is one row of contacts.
type Contact struct {
	OpenID     string `json:"open_id"`
	Name       string `json:"name"`
	Email      string `json:"email,omitempty"`
	IsBot      bool   `json:"is_bot"`
	P2PChatID  string `json:"p2p_chat_id,omitempty"`
	AvatarURL  string `json:"avatar_url,omitempty"`
	AvatarPath string `json:"avatar_path,omitempty"`
	UpdatedAt  int64  `json:"updated_at"`
	RawJSON    string `json:"-"`
}

const contactColumns = `open_id, name, email, is_bot, p2p_chat_id, avatar_url, avatar_path, updated_at, raw_json`

func scanContact(sc scanner) (Contact, error) {
	var c Contact
	err := sc.Scan(&c.OpenID, &c.Name, &c.Email, &c.IsBot, &c.P2PChatID, &c.AvatarURL, &c.AvatarPath, &c.UpdatedAt, &c.RawJSON)
	return c, err
}

// UpsertContacts inserts or refreshes contacts; empty incoming fields keep the stored value.
func (s *Store) UpsertContacts(ctx context.Context, contacts []Contact, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO contacts (`+contactColumns+`) VALUES (?,?,?,?,?,?,?,?,?)
 ON CONFLICT(open_id) DO UPDATE SET
   name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE contacts.name END,
   email = CASE WHEN excluded.email <> '' THEN excluded.email ELSE contacts.email END,
   is_bot = excluded.is_bot,
   p2p_chat_id = CASE WHEN excluded.p2p_chat_id <> '' THEN excluded.p2p_chat_id ELSE contacts.p2p_chat_id END,
   avatar_url = CASE WHEN excluded.avatar_url <> '' THEN excluded.avatar_url ELSE contacts.avatar_url END,
   updated_at = excluded.updated_at,
   raw_json = CASE WHEN excluded.raw_json <> '' THEN excluded.raw_json ELSE contacts.raw_json END`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range contacts {
		if _, err := stmt.ExecContext(ctx, c.OpenID, c.Name, c.Email, c.IsBot, c.P2PChatID, c.AvatarURL, c.AvatarPath, now, c.RawJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FindContacts returns contacts whose open_id, email or name equals ref (case-insensitive for email/name).
func (s *Store) FindContacts(ctx context.Context, ref string) ([]Contact, error) {
	return queryAll(ctx, s.db, scanContact, `SELECT `+contactColumns+` FROM contacts WHERE open_id = ? OR lower(email) = lower(?) OR name = ? ORDER BY name`, ref, ref, ref)
}

// ListContacts returns contacts matching an optional substring of name or email.
func (s *Store) ListContacts(ctx context.Context, search string, limit int) ([]Contact, error) {
	q := `SELECT ` + contactColumns + ` FROM contacts`
	var args []any
	if search != "" {
		q += ` WHERE instr(lower(name), lower(?)) > 0 OR instr(lower(email), lower(?)) > 0`
		args = append(args, search, search)
	}
	if limit <= 0 {
		limit = 1000
	}
	q += ` ORDER BY name LIMIT ?`
	args = append(args, limit)
	return queryAll(ctx, s.db, scanContact, q, args...)
}

// GetContact loads one contact.
func (s *Store) GetContact(ctx context.Context, openID string) (Contact, error) {
	c, err := scanContact(s.db.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM contacts WHERE open_id = ?`, openID))
	if err == sql.ErrNoRows {
		return c, ErrNotFound
	}
	return c, err
}
