package store

import (
	"cmp"
	"context"
	"net/url"
	"regexp"
	"strings"
)

// DocRef is a Feishu document as its URL spells it. The type is the word in
// the path rather than what the document turns out to be: a /wiki/ link is
// unwrapped server-side, and this is the spelling the next message carrying
// that link will be looked up by.
type DocRef struct {
	Type  string
	Token string
}

// Key indexes a document in the map a pane renders from.
func (r DocRef) Key() string { return r.Type + "/" + r.Token }

// DocLabel is what a resolved link shows in place of its URL: the document's
// title, or the fact that it is out of reach.
type DocLabel struct {
	Title string
	// Type is what the document turned out to be, which is what picks the
	// glyph; a wiki node is whatever it wraps.
	Type   string
	Denied bool
}

// docPathTypes maps a URL path prefix onto the document type the metadata
// API names it by. Longer prefixes come first so /drive/folder/ is not read
// as a document called "folder", and two of the spellings differ from the
// API's word for the same thing: /sheets/ is a sheet, /base/ is a bitable.
var docPathTypes = []struct{ prefix, docType string }{
	{"/drive/folder/", "folder"},
	{"/drive/file/", "file"},
	{"/drive/shr/", "folder"},
	{"/chat/drive/", "folder"},
	{"/docx/", "docx"},
	{"/doc/", "doc"},
	{"/sheets/", "sheet"},
	{"/base/", "bitable"},
	{"/bitable/", "bitable"},
	{"/wiki/", "wiki"},
	{"/file/", "file"},
	{"/mindnote/", "mindnote"},
	{"/slides/", "slides"},
}

// docURL matches a Feishu URL written out in a body. The trailing class is
// the one bare links are found by elsewhere: a full stop or a closing bracket
// after a URL belongs to the sentence, not the link.
var docURL = regexp.MustCompile(`https?://[\w.-]*feishu\.cn/[^\s<>"'` + "`" + `\[\]()]*[^\s<>"'` + "`" + `\[\]().,;:!?，。；：！？、]`)

// ParseDocURL reads the document a Feishu URL addresses. Only the path
// decides: a pasted link almost always carries ?from= and a #fragment naming
// a block inside the document, and both would otherwise make the same
// document look like a different one every time someone shares it.
func ParseDocURL(raw string) (DocRef, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.HasSuffix(u.Hostname(), "feishu.cn") {
		return DocRef{}, false
	}
	for _, m := range docPathTypes {
		rest, ok := strings.CutPrefix(u.Path, m.prefix)
		if !ok {
			continue
		}
		token, _, _ := strings.Cut(strings.TrimLeft(rest, "/"), "/")
		if token == "" {
			return DocRef{}, false
		}
		return DocRef{Type: m.docType, Token: token}, true
	}
	return DocRef{}, false
}

// FindDocRefs reads the documents a body links to. The same document named
// twice is returned twice; registering it is idempotent.
func FindDocRefs(text string) []DocRef {
	var refs []DocRef
	for _, raw := range docURL.FindAllString(text, -1) {
		if ref, ok := ParseDocURL(raw); ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

// AddPendingDocLinks registers documents worth a title. A document already
// known keeps the state it has, so a link shared around a dozen chats is
// resolved once.
func (s *Store) AddPendingDocLinks(ctx context.Context, refs []DocRef) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO doc_titles (doc_type, token) VALUES (?, ?)
 ON CONFLICT(doc_type, token) DO NOTHING`, r.Type, r.Token); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DocLinksDue returns documents worth asking about now: never resolved, or
// resolved long enough ago that the title may have been changed since. A
// document the identity cannot read is left out for good — the three ways
// the API refuses one are all permanent.
func (s *Store) DocLinksDue(ctx context.Context, now int64, limit int) ([]DocRef, error) {
	return queryAll(ctx, s.db, func(sc scanner) (DocRef, error) {
		var r DocRef
		err := sc.Scan(&r.Type, &r.Token)
		return r, err
	}, `SELECT doc_type, token FROM doc_titles
 WHERE status <> 'denied' AND next_attempt_at <= ? ORDER BY next_attempt_at, token LIMIT ?`, now, limit)
}

// MarkDocTitle records a resolved title and when it is worth reading again.
func (s *Store) MarkDocTitle(ctx context.Context, ref DocRef, title, resolvedType string, refreshAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE doc_titles
 SET status = 'done', title = ?, resolved_type = ?, next_attempt_at = ?
 WHERE doc_type = ? AND token = ?`, title, resolvedType, refreshAt, ref.Type, ref.Token)
	return err
}

// MarkDocDenied records a document this identity cannot read. Unsupported
// type, no permission and no such document all land here: the reader is told
// the same thing by each, and none of them is worth asking again.
func (s *Store) MarkDocDenied(ctx context.Context, ref DocRef) error {
	_, err := s.db.ExecContext(ctx, `UPDATE doc_titles
 SET status = 'denied', next_attempt_at = 0 WHERE doc_type = ? AND token = ?`, ref.Type, ref.Token)
	return err
}

// DocLabels reads every document a pane could need to name. A personal
// archive holds these in the thousands at most, so one read beats working
// out which links the rows on screen happen to spell.
func (s *Store) DocLabels(ctx context.Context) (map[string]DocLabel, error) {
	type row struct {
		ref          DocRef
		title        string
		resolvedType string
		status       string
	}
	rows, err := queryAll(ctx, s.db, func(sc scanner) (row, error) {
		var r row
		err := sc.Scan(&r.ref.Type, &r.ref.Token, &r.title, &r.resolvedType, &r.status)
		return r, err
	}, `SELECT doc_type, token, title, resolved_type, status FROM doc_titles
 WHERE status IN ('done','denied')`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]DocLabel, len(rows))
	for _, r := range rows {
		// A wiki node is named by whatever it wraps; a document the identity
		// cannot read was never unwrapped, so it stays what the URL called it.
		out[r.ref.Key()] = DocLabel{
			Title:  r.title,
			Type:   cmp.Or(r.resolvedType, r.ref.Type),
			Denied: r.status == "denied",
		}
	}
	return out, nil
}

// DocScanRow is a rendered body for the document-link back-scan.
type DocScanRow struct {
	ID      int64
	Content string
}

// MessagesAfterIDForDocScan returns rendered live messages ingested after
// rowID, oldest first, so links in messages stored before titles were read
// can be registered. Unlike the resource scan this cannot filter by type: a
// document link is almost always someone pasting a URL into a plain text
// message.
func (s *Store) MessagesAfterIDForDocScan(ctx context.Context, rowID int64, limit int) ([]DocScanRow, error) {
	return queryAll(ctx, s.db, func(sc scanner) (DocScanRow, error) {
		var r DocScanRow
		err := sc.Scan(&r.ID, &r.Content)
		return r, err
	}, `SELECT id, content FROM messages
 WHERE id > ? AND deleted = 0 AND rendered_at > 0 ORDER BY id LIMIT ?`, rowID, limit)
}
