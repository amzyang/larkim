package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// readCheckDelays spaces remote read-status checks: a message is usually read
// within minutes or not for hours.
var readCheckDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 6 * time.Hour}

// ReadCheckDelay returns the wait before the n-th check (n >= 1).
func ReadCheckDelay(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > len(readCheckDelays) {
		n = len(readCheckDelays)
	}
	return readCheckDelays[n-1]
}

// readStatusHorizon bounds how far back read status is polled.
const readStatusHorizon = 7 * 24 * time.Hour

const maxResourceAttempts = 5

// ResourceRetryDelay returns the wait before retrying a failed download after
// `attempts` failures; 0 means give up.
func ResourceRetryDelay(attempts int) time.Duration {
	if attempts >= maxResourceAttempts {
		return 0
	}
	return ReadCheckDelay(attempts)
}

// ExtractResources lists the downloadable attachment keys referenced by a
// message body. Stickers are not downloadable and merge-forwards carry their
// resources on the inner messages.
func ExtractResources(messageID, msgType, contentRaw string) []store.Resource {
	var out []store.Resource
	add := func(key, typ string) {
		if key == "" {
			return
		}
		for _, r := range out {
			if r.FileKey == key {
				return
			}
		}
		out = append(out, store.Resource{MessageID: messageID, FileKey: key, Type: typ})
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(contentRaw), &body); err != nil {
		return nil
	}
	switch msgType {
	case "image":
		add(str(body["image_key"]), "image")
	case "file", "audio", "media", "video":
		add(str(body["file_key"]), "file")
	case "post":
		walkPost(body, add)
	}
	return out
}

// walkPost visits every element of a rich-text body (either the plain
// {title, content} form or the locale-wrapped {zh_cn: {...}} form).
func walkPost(v any, add func(key, typ string)) {
	switch x := v.(type) {
	case map[string]any:
		if tag, _ := x["tag"].(string); tag != "" {
			switch tag {
			case "img":
				add(str(x["image_key"]), "image")
			case "media":
				add(str(x["file_key"]), "file")
			}
		}
		for _, child := range x {
			walkPost(child, add)
		}
	case []any:
		for _, child := range x {
			walkPost(child, add)
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// KeyResourceScanID is the newest message row whose attachments have been
// registered by the back-scan.
const KeyResourceScanID = "resource_scan_id"

const resourceScanBatch = 500

// registerExistingResources walks messages stored before attachment tracking
// existed (or by older versions) and registers their attachment keys, a
// bounded slice per tick. Steady state costs one empty query.
func (s *Syncer) registerExistingResources(ctx context.Context) error {
	v, _, _ := s.Store.GetState(ctx, KeyResourceScanID)
	after, _ := strconv.ParseInt(v, 10, 64)
	rows, err := s.Store.MessagesAfterIDForScan(ctx, after, resourceScanBatch)
	if err != nil {
		return err
	}
	var rs []store.Resource
	for _, r := range rows {
		rs = append(rs, ExtractResources(r.MessageID, r.MsgType, r.ContentRaw)...)
		after = max(after, r.ID)
	}
	if len(rows) < resourceScanBatch {
		// Caught up: jump to the newest row so later ticks skip non-media rows too.
		if maxID, err := s.Store.MaxMessageRowID(ctx); err == nil {
			after = max(after, maxID)
		}
	}
	if err := s.Store.AddPendingResources(ctx, rs); err != nil {
		return err
	}
	return s.Store.SetState(ctx, KeyResourceScanID, strconv.FormatInt(after, 10))
}

// downloadPending fetches attachments for messages with due resources.
func (s *Syncer) downloadPending(ctx context.Context, now time.Time) (int, error) {
	if s.Opt.DataDir == "" {
		return 0, nil
	}
	if err := s.registerExistingResources(ctx); err != nil {
		return 0, err
	}
	ids, err := s.Store.ResourceMessagesDue(ctx, now.UnixMilli(), s.Opt.DownloadPerTick*50)
	if err != nil {
		return 0, err
	}
	done := 0
	for batch := range slices.Chunk(ids, 50) {
		rendered, err := s.Client.MGetRendered(ctx, batch, true)
		if err != nil {
			return done, err
		}
		got := map[string]map[string]larkcli.Resource{}
		for _, r := range rendered {
			if err := s.storeRendered(ctx, r, now); err != nil {
				return done, err
			}
			byKey := map[string]larkcli.Resource{}
			for _, res := range r.Resources {
				byKey[res.Key] = res
			}
			got[r.MessageID] = byKey
		}
		for _, id := range batch {
			pending, err := s.Store.ResourcesFor(ctx, id)
			if err != nil {
				return done, err
			}
			for _, p := range pending {
				if p.Status == "done" || p.Status == "skipped" {
					continue
				}
				res, ok := got[id][p.FileKey]
				if !ok {
					if err := s.failResource(ctx, p, "not returned by lark-cli", now); err != nil {
						return done, err
					}
					continue
				}
				stored, err := s.storeResource(ctx, p, res, now)
				if err != nil {
					return done, err
				}
				if stored {
					done++
				}
			}
		}
	}
	return done, nil
}

// storeResource records a downloaded file; stored is false when the file was
// missing or discarded for size.
func (s *Syncer) storeResource(ctx context.Context, p store.Resource, res larkcli.Resource, now time.Time) (bool, error) {
	abs := res.LocalPath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.Opt.DataDir, "resources", abs)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return false, s.failResource(ctx, p, "downloaded file missing: "+err.Error(), now)
	}
	if s.Opt.MaxBytes > 0 && st.Size() > s.Opt.MaxBytes {
		_ = os.Remove(abs)
		return false, s.Store.MarkResourceSkipped(ctx, p.MessageID, p.FileKey, st.Size(), fmt.Sprintf("larger than %d bytes", s.Opt.MaxBytes))
	}
	rel, err := filepath.Rel(s.Opt.DataDir, abs)
	if err != nil {
		rel = abs
	}
	return true, s.Store.MarkResourceDone(ctx, p.MessageID, p.FileKey, rel, st.Size())
}

func (s *Syncer) failResource(ctx context.Context, p store.Resource, reason string, now time.Time) error {
	delay := ResourceRetryDelay(p.Attempts + 1)
	next := int64(0)
	if delay > 0 {
		next = now.Add(delay).UnixMilli()
	}
	return s.Store.MarkResourceFailed(ctx, p.MessageID, p.FileKey, reason, next)
}

// pollReadStatus asks Feishu whether the user has read recent messages from
// others, on a widening schedule per message.
func (s *Syncer) pollReadStatus(ctx context.Context, now time.Time) (int, error) {
	self, ok, err := s.Store.GetState(ctx, KeySelfOpenID)
	if err != nil || !ok || self == "" {
		return 0, err
	}
	ids, err := s.Store.ReadStatusCandidates(ctx, self, now.Add(-readStatusHorizon).UnixMilli(), now.UnixMilli(), s.Opt.ReadStatusPerTick*50)
	if err != nil {
		return 0, err
	}
	checked := 0
	for batch := range slices.Chunk(ids, 50) {
		items, invalid, err := s.Client.ReadStatus(ctx, batch)
		if err != nil {
			return checked, err
		}
		for _, it := range items {
			n, err := s.Store.ReadCheckCount(ctx, it.MessageID)
			if err != nil {
				return checked, err
			}
			next := int64(0)
			if !it.IsRead {
				next = now.Add(ReadCheckDelay(n + 1)).UnixMilli()
			}
			isRead := it.IsRead
			if err := s.Store.SetReadStatus(ctx, it.MessageID, &isRead, now.UnixMilli(), next); err != nil {
				return checked, err
			}
			checked++
		}
		for _, id := range invalid {
			// Not visible or unsupported: check again much later, keep unknown.
			if err := s.Store.SetReadStatus(ctx, id, nil, now.UnixMilli(), now.Add(readCheckDelays[len(readCheckDelays)-1]).UnixMilli()); err != nil {
				return checked, err
			}
		}
	}
	return checked, nil
}
