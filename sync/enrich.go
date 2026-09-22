package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// Fetcher downloads a URL; avatars are public CDN files that need no token.
type Fetcher func(ctx context.Context, url string) (body []byte, contentType string, err error)

// HTTPFetch is the production Fetcher.
func HTTPFetch(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return body, resp.Header.Get("Content-Type"), err
}

const (
	// KeyRepairAt is when the current repair pass started; chats repaired
	// before it are due again.
	KeyRepairAt = "repair_at"

	repairHorizon       = 7 * 24 * time.Hour
	membersRefreshEvery = 24 * time.Hour
)

// repairSlice re-lists a few active chats per pass so edits and recalls of
// the last week are picked up; a pass starts every RepairEvery.
func (s *Syncer) repairSlice(ctx context.Context, now time.Time) (int, error) {
	if s.Opt.RepairEvery <= 0 {
		return 0, nil
	}
	started := s.stateTime(ctx, KeyRepairAt)
	if Due(started, s.Opt.RepairEvery, now) {
		if err := s.setStateTime(ctx, KeyRepairAt, now); err != nil {
			return 0, err
		}
		started = now
	}
	chats, err := s.Store.ChatsForRepair(ctx, now.Add(-repairHorizon).UnixMilli(), started.UnixMilli(), s.Opt.RepairPerTick)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range chats {
		n, err := s.pullChat(ctx, c.ChatID, now.Add(-repairHorizon), time.Time{}, now)
		if err != nil {
			if s.recordChatError(ctx, c.ChatID, err, now) {
				continue
			}
			return total, err
		}
		total += n
		if err := s.Store.SetChatRepaired(ctx, c.ChatID, now.UnixMilli()); err != nil {
			return total, err
		}
	}
	return total, nil
}

// membersSlice refreshes member lists for a few chats whose list is stale,
// and records the members as contacts.
func (s *Syncer) membersSlice(ctx context.Context, now time.Time) (int, error) {
	chats, err := s.Store.ChatsNeedingMembers(ctx, now.Add(-membersRefreshEvery).UnixMilli(), s.Opt.MembersPerTick)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range chats {
		members, err := s.Client.ChatMembers(ctx, c.ChatID)
		if err != nil {
			if s.recordChatError(ctx, c.ChatID, err, now) {
				continue
			}
			return total, err
		}
		contacts := make([]store.Contact, 0, len(members))
		for _, m := range members {
			contacts = append(contacts, store.Contact{OpenID: m.MemberID, Name: m.Name})
		}
		if err := s.Store.UpsertContacts(ctx, contacts, now.UnixMilli()); err != nil {
			return total, err
		}
		if err := s.Store.SetChatMembers(ctx, c.ChatID, contacts, now.UnixMilli()); err != nil {
			return total, err
		}
		total += len(contacts)
	}
	return total, nil
}

// senderContacts records message senders as contacts so names resolve
// without a members pass.
func senderContacts(msgs []larkcli.RawMessage) []store.Contact {
	seen := map[string]bool{}
	var out []store.Contact
	for _, m := range msgs {
		id := m.Sender.OpenBotID
		if id == "" {
			id = m.Sender.ID
		}
		if id == "" || !strings.HasPrefix(id, "ou_") || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, store.Contact{OpenID: id, Name: m.Sender.SenderName, IsBot: m.Sender.SenderType == "app"})
	}
	return out
}

// avatarsSlice resolves avatar URLs for contacts and downloads a few chat and
// contact avatars per tick into resources/avatars/.
func (s *Syncer) avatarsSlice(ctx context.Context, now time.Time) (int, error) {
	if s.Fetch == nil || s.Opt.DataDir == "" {
		return 0, nil
	}
	need, err := s.Store.ContactsNeedingAvatar(ctx, s.Opt.AvatarsPerTick)
	if err != nil {
		return 0, err
	}
	for _, c := range need {
		d, err := s.Client.UserDetail(ctx, c.OpenID)
		if err != nil {
			var le *larkcli.Error
			if errors.As(err, &le) && le.IsPermanent() {
				if err := s.Store.SetContactAvatar(ctx, c.OpenID, store.AvatarNone, now.UnixMilli()); err != nil {
					return 0, err
				}
				continue
			}
			return 0, err
		}
		url := d.AvatarURL
		if url == "" {
			url = store.AvatarNone
		}
		if err := s.Store.SetContactAvatar(ctx, c.OpenID, url, now.UnixMilli()); err != nil {
			return 0, err
		}
		if d.Name != "" {
			_ = s.Store.UpsertContacts(ctx, []store.Contact{{OpenID: c.OpenID, Name: d.Name}}, now.UnixMilli())
		}
	}
	chats, contacts, err := s.Store.AvatarsToDownload(ctx, s.Opt.AvatarsPerTick)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, c := range chats {
		rel := s.downloadAvatar(ctx, "chats", c.ChatID, c.AvatarURL)
		if err := s.Store.SetChatAvatarPath(ctx, c.ChatID, rel); err != nil {
			return done, err
		}
		if rel != store.AvatarFailed {
			done++
		}
	}
	for _, c := range contacts {
		rel := s.downloadAvatar(ctx, "users", c.OpenID, c.AvatarURL)
		if err := s.Store.SetContactAvatarPath(ctx, c.OpenID, rel); err != nil {
			return done, err
		}
		if rel != store.AvatarFailed {
			done++
		}
	}
	return done, nil
}

// downloadAvatar stores an avatar under resources/avatars/<kind>/<id>.<ext>
// and returns the path relative to the data dir, or AvatarFailed.
func (s *Syncer) downloadAvatar(ctx context.Context, kind, id, url string) string {
	body, ctype, err := s.Fetch(ctx, url)
	if err != nil || len(body) == 0 {
		s.log().Warn("avatar download failed", "id", id, "err", err)
		return store.AvatarFailed
	}
	ext := ".img"
	if exts, _ := mime.ExtensionsByType(strings.Split(ctype, ";")[0]); len(exts) > 0 {
		ext = exts[len(exts)-1]
		if ext == ".jpe" {
			ext = ".jpg"
		}
	}
	rel := filepath.Join("resources", "avatars", kind, id+ext)
	abs := filepath.Join(s.Opt.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return store.AvatarFailed
	}
	if err := os.WriteFile(abs, body, 0o600); err != nil {
		s.log().Warn("avatar write failed", "path", abs, "err", err)
		return store.AvatarFailed
	}
	return rel
}
