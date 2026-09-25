package store

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
)

// Contact is one row of contacts.
type Contact struct {
	OpenID          string `json:"open_id"`
	Name            string `json:"name"`
	Email           string `json:"email,omitempty"`
	EnterpriseEmail string `json:"enterprise_email,omitempty"`
	Department      string `json:"department,omitempty"`
	IsCrossTenant   bool   `json:"is_cross_tenant,omitempty"`
	IsBot           bool   `json:"is_bot"`
	P2PChatID       string `json:"p2p_chat_id,omitempty"`
	AvatarURL       string `json:"avatar_url,omitempty"`
	AvatarPath      string `json:"avatar_path,omitempty"`
	DetailCheckedAt int64  `json:"detail_checked_at,omitempty"`
	UpdatedAt       int64  `json:"updated_at"`
	RawJSON         string `json:"-"`
}

// AvatarFile is the contact's picture relative to the data dir. Empty when
// there is none to draw, including the sentinels for "no avatar" and
// "download gave up".
func (c Contact) AvatarFile() string { return avatarFile(c.AvatarPath) }

const contactColumns = `open_id, name, email, enterprise_email, department, is_cross_tenant, is_bot, p2p_chat_id, avatar_url, avatar_path, detail_checked_at, updated_at, raw_json`

// contactUpsertColumns is contactColumns without the ones only the detail
// backfill writes.
const contactUpsertColumns = `open_id, name, email, is_bot, p2p_chat_id, avatar_url, avatar_path, updated_at, raw_json`

func scanContact(sc scanner) (Contact, error) {
	var c Contact
	err := sc.Scan(&c.OpenID, &c.Name, &c.Email, &c.EnterpriseEmail, &c.Department, &c.IsCrossTenant,
		&c.IsBot, &c.P2PChatID, &c.AvatarURL, &c.AvatarPath, &c.DetailCheckedAt, &c.UpdatedAt, &c.RawJSON)
	return c, err
}

// AccountSuffix is the trailing number of the tenant account name
// ("liming01@example.com" → "01"), which the tenant assigns only to
// disambiguate same-named colleagues. Empty when the name carries no suffix.
func AccountSuffix(addr string) string {
	local, _, _ := strings.Cut(addr, "@")
	end := len(local)
	for end > 0 && local[end-1] >= '0' && local[end-1] <= '9' {
		end--
	}
	if end == 0 || end == len(local) {
		return "" // all digits, or none: not a disambiguating suffix
	}
	return local[end:]
}

// AccountSuffix reads the contact's own account name, preferring the
// enterprise address.
func (c Contact) AccountSuffix() string {
	if s := AccountSuffix(c.EnterpriseEmail); s != "" {
		return s
	}
	return AccountSuffix(c.Email)
}

// UpsertContacts inserts or refreshes contacts; empty incoming fields keep the stored value.
func (s *Store) UpsertContacts(ctx context.Context, contacts []Contact, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO contacts (`+contactUpsertColumns+`) VALUES (?,?,?,?,?,?,?,?,?)
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

// ListContacts returns contacts by name. Narrowing is the caller's: a search
// is matched fuzzily in Go, which SQL cannot do.
func (s *Store) ListContacts(ctx context.Context, limit int) ([]Contact, error) {
	q := `SELECT ` + contactColumns + ` FROM contacts ORDER BY name`
	if limit <= 0 {
		// A search spells every name it is given, so a cap here is a person
		// the reader cannot find rather than a page they cannot see.
		return queryAll(ctx, s.db, scanContact, q)
	}
	return queryAll(ctx, s.db, scanContact, q+` LIMIT ?`, limit)
}

// GetContact loads one contact.
func (s *Store) GetContact(ctx context.Context, openID string) (Contact, error) {
	c, err := scanContact(s.db.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM contacts WHERE open_id = ?`, openID))
	if err == sql.ErrNoRows {
		return c, ErrNotFound
	}
	return c, err
}

// ContactDetail is the identity half of a contact, from `contact +search-user`.
type ContactDetail struct {
	OpenID          string
	Name            string
	Email           string
	EnterpriseEmail string
	Department      string
	IsCrossTenant   bool
}

// ContactsNeedingDetail returns user contacts whose identity fields were never
// resolved, p2p partners before other contacts and recent senders before
// dormant ones.
func (s *Store) ContactsNeedingDetail(ctx context.Context, limit int) ([]Contact, error) {
	return queryAll(ctx, s.db, scanContact, `SELECT `+contactColumns+` FROM contacts c
 WHERE c.is_bot = 0 AND c.detail_checked_at = 0
 ORDER BY (c.p2p_chat_id <> '') DESC, (SELECT max(create_ms) FROM messages m WHERE m.sender_id = c.open_id) DESC LIMIT ?`, limit)
}

// SetContactDetails records the identity fields the search resolved and marks
// every id of the batch checked, so ids the server omits (left the tenant, no
// longer visible) are not asked for on every tick.
func (s *Store) SetContactDetails(ctx context.Context, ids []string, details []ContactDetail, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// email has another writer (UpsertContacts, from chat members and
	// resolve), so an answer that omits it must not erase it; the other
	// identity columns belong to this lookup alone and an empty answer is
	// their truth.
	set, err := tx.PrepareContext(ctx, `UPDATE contacts SET
   name = CASE WHEN ? <> '' THEN ? ELSE name END,
   email = CASE WHEN ? <> '' THEN ? ELSE email END,
   enterprise_email = ?, department = ?, is_cross_tenant = ?,
   detail_checked_at = ?, updated_at = ? WHERE open_id = ?`)
	if err != nil {
		return err
	}
	defer set.Close()
	for _, d := range details {
		if _, err := set.ExecContext(ctx, d.Name, d.Name, d.Email, d.Email, d.EnterpriseEmail, d.Department, d.IsCrossTenant, now, now, d.OpenID); err != nil {
			return err
		}
	}
	mark, err := tx.PrepareContext(ctx, `UPDATE contacts SET detail_checked_at = ? WHERE open_id = ? AND detail_checked_at = 0`)
	if err != nil {
		return err
	}
	defer mark.Close()
	for _, id := range ids {
		if _, err := mark.ExecContext(ctx, now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ContactsByIDs loads contacts by open id, keyed by open id; ids with no row
// are absent from the map.
func (s *Store) ContactsByIDs(ctx context.Context, ids []string) (map[string]Contact, error) {
	out := make(map[string]Contact, len(ids))
	for chunk := range slices.Chunk(ids, 500) {
		rows, err := queryAll(ctx, s.db, scanContact, `SELECT `+contactColumns+` FROM contacts WHERE open_id IN `+inClause(len(chunk)), anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, c := range rows {
			out[c.OpenID] = c
		}
	}
	return out, nil
}

// ChatMembers is a chat's roster, joined to whatever the contacts table knows
// about each member and ordered by name so the same chat always lists the same
// way. A member the contacts table has never seen still comes back, named by
// its open id, because leaving it out would say the chat is smaller than it is.
//
// Member lists refresh daily, so this answers what the last refresh saw.
func (s *Store) ChatMembers(ctx context.Context, chatID string) ([]Contact, error) {
	return queryAll(ctx, s.db, scanContact,
		`SELECT m.member_id,
		        COALESCE(c.name, ''), COALESCE(c.email, ''), COALESCE(c.enterprise_email, ''),
		        COALESCE(c.department, ''), COALESCE(c.is_cross_tenant, 0), COALESCE(c.is_bot, 0),
		        COALESCE(c.p2p_chat_id, ''), COALESCE(c.avatar_url, ''), COALESCE(c.avatar_path, ''),
		        COALESCE(c.detail_checked_at, 0), COALESCE(c.updated_at, 0), COALESCE(c.raw_json, '')
		 FROM chat_members m LEFT JOIN contacts c ON c.open_id = m.member_id
		 WHERE m.chat_id = ?
		 ORDER BY CASE WHEN COALESCE(c.name, '') = '' THEN 1 ELSE 0 END, c.name, m.member_id`, chatID)
}

// ChatRoster is who @ can name in a chat. A group answers with the member list
// ChatMembers keeps; a chat of two has none kept for it, so the pair is read
// off the chat itself, which is where the peer has been named all along.
//
// The peer comes first because it is who a mention in a chat of two means.
func (s *Store) ChatRoster(ctx context.Context, chatID, self string) ([]Contact, error) {
	c, err := s.GetChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if c.ChatMode != "p2p" {
		return s.ChatMembers(ctx, chatID)
	}
	known, err := s.ContactsByIDs(ctx, []string{c.P2PTargetID, self})
	if err != nil {
		return nil, err
	}
	// Most peers of a p2p chat, bots above all, are never fetched as contacts.
	// The chat's own name is the peer's name — it is what the chat list draws
	// them by — and the chat says which kind of peer it is.
	peer := known[c.P2PTargetID]
	peer.OpenID = c.P2PTargetID
	peer.Name = cmp.Or(peer.Name, c.Name)
	peer.IsBot = peer.IsBot || c.P2PTargetType == "bot"
	out := []Contact{peer}
	if self != "" && self != c.P2PTargetID {
		me := known[self]
		me.OpenID = self
		out = append(out, me)
	}
	return out, nil
}
