package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// ExtractResources lists the attachment keys referenced by a message body.
// Merge-forwards carry their resources on the inner messages. A sticker's key
// is listed like any other, though nothing downloads it: copyStickers takes
// that picture out of the Lark client's own storage.
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
	case "file", "audio":
		add(str(body["file_key"]), "file")
	case "media", "video":
		add(str(body["file_key"]), "file")
		// The frame the client draws a video as. lark-cli's batch download
		// extracts a media body's file_key alone, so this one is fetched by a
		// call of its own.
		add(str(body["image_key"]), "cover")
	case "sticker":
		add(str(body["file_key"]), "sticker")
	case "post":
		walkPost(body, add)
	case "interactive":
		walkCardAttachment(body["json_attachment"], add)
	}
	return out
}

// walkCardAttachment collects the image keys a card keeps in its attachment
// table. The card body only names an imageID that points here, so the key
// exists nowhere else; the table's shape is the same whatever card_schema the
// body speaks, which is why the body is left unread. Feishu sends the table
// either inline or as an embedded JSON string. The ids are walked in order so
// a card's images register in a stable one.
func walkCardAttachment(v any, add func(key, typ string)) {
	att, ok := v.(map[string]any)
	if !ok {
		s, _ := v.(string)
		if json.Unmarshal([]byte(s), &att) != nil {
			return
		}
	}
	imgs, _ := att["images"].(map[string]any)
	for _, id := range slices.Sorted(maps.Keys(imgs)) {
		img, _ := imgs[id].(map[string]any)
		add(str(img["origin_key"]), "image")
	}
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
					var err error
					if res, err = s.fetchAside(ctx, p); err != nil {
						if err := s.failResource(ctx, p, err.Error(), now); err != nil {
							return done, err
						}
						continue
					}
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

// fetchAside downloads an attachment the batch left out. Only a video's cover
// is expected here — lark-cli's worklist holds every other key — so anything
// else is reported as the gap it is rather than costing a call.
func (s *Syncer) fetchAside(ctx context.Context, p store.Resource) (larkcli.Resource, error) {
	if p.Type != "cover" {
		return larkcli.Resource{}, errors.New("not returned by lark-cli")
	}
	return s.Client.DownloadResource(ctx, p.MessageID, p.FileKey, "image")
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
	if s.oversize(st.Size()) {
		_ = os.Remove(abs)
		return false, s.skipResource(ctx, p, st.Size())
	}
	rel, err := filepath.Rel(s.Opt.DataDir, abs)
	if err != nil {
		rel = abs
	}
	return true, s.Store.MarkResourceDone(ctx, p.MessageID, p.FileKey, rel, st.Size())
}

// oversize reports whether an attachment is past resources.max_bytes.
func (s *Syncer) oversize(size int64) bool {
	return s.Opt.MaxBytes > 0 && size > s.Opt.MaxBytes
}

// skipResource records an attachment deliberately not kept for its size.
func (s *Syncer) skipResource(ctx context.Context, p store.Resource, size int64) error {
	return s.Store.MarkResourceSkipped(ctx, p.MessageID, p.FileKey, size, fmt.Sprintf("larger than %d bytes", s.Opt.MaxBytes))
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
// others, on a widening schedule per message, and drops the unread flag of
// messages that have aged out of the horizon it polls.
func (s *Syncer) pollReadStatus(ctx context.Context, now time.Time) (int, error) {
	if _, err := s.Store.ExpireReadStatus(ctx, now.Add(-readStatusHorizon).UnixMilli()); err != nil {
		return 0, err
	}
	return s.checkReadStatus(ctx, now, store.ReadCheckQuery{DueAt: now.UnixMilli(), Limit: s.Opt.ReadStatusPerTick * 50})
}

// RefreshReadStatus re-asks Feishu about one chat's unread messages right
// away, ignoring their backoff, so a chat read in the desktop client stops
// showing a stale badge as soon as it is opened here.
func (s *Syncer) RefreshReadStatus(ctx context.Context, chatID string) (int, error) {
	return s.checkReadStatus(ctx, s.now(), store.ReadCheckQuery{ChatID: chatID, Limit: 50})
}

// reactionWindow is how many of a chat's newest messages have their reactions
// re-asked when it is opened. It is exactly one batch_query, and it is the
// part of the chat people react to: an older message's summary stays as the
// rendering left it.
const reactionWindow = larkcli.MaxMessageIDsPerReactionCall

// RefreshReactions re-asks Feishu who reacted to the newest messages of one
// chat. Nothing else keeps a reaction summary current: Feishu does not move a
// message's update_time when somebody reacts, so the rendering pass, which is
// what wrote the summary in the first place, never comes back for it.
func (s *Syncer) RefreshReactions(ctx context.Context, chatID string) (int, error) {
	msgs, err := s.Store.ListMessages(ctx, store.MessageQuery{ChatID: chatID, Desc: true, Limit: reactionWindow})
	if err != nil || len(msgs) == 0 {
		return 0, err
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.MessageID)
	}
	blocks, err := s.Client.ReactionCounts(ctx, ids)
	if err != nil {
		return 0, err
	}
	for id, block := range blocks {
		if err := s.Store.UpdateReactions(ctx, id, rawString(block)); err != nil {
			return 0, err
		}
	}
	return len(blocks), nil
}

// reactionsSlice keeps the chat list's reactions current. The list draws them
// for p2p chats alone, so only those are asked about, and only their newest
// message: that is the one the summary line carries.
//
// A chat is worth one slot in the batch for as long as it is among the
// liveliest, which is the window a reaction is likely to land in. The rest
// keep whatever their last rendering or visit left, and the TUI refreshes a
// chat's whole window when it is opened.
func (s *Syncer) reactionsSlice(ctx context.Context) (int, error) {
	chats, err := s.Store.ListChats(ctx, store.ChatQuery{Mode: "p2p", Limit: reactionWindow})
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(chats))
	for _, c := range chats {
		if c.LastMessageID != "" && !c.LastDeleted {
			ids = append(ids, c.LastMessageID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	blocks, err := s.Client.ReactionCounts(ctx, ids)
	if err != nil {
		return 0, err
	}
	for id, block := range blocks {
		if err := s.Store.UpdateReactions(ctx, id, rawString(block)); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// React puts one emoji on a message, or takes the reader's own back, and
// brings that message's summary up to date in the same breath so the pane
// shows what Feishu now holds rather than what it held a moment ago.
//
// Taking one back costs an extra call: the delete needs a reaction id, and
// Feishu hands that out nowhere but reactions.list. Feishu only lets an
// identity delete what it added, so the id has to be the reader's own.
func (s *Syncer) React(ctx context.Context, messageID, emojiType string, on bool) error {
	if on {
		if _, err := s.Client.AddReaction(ctx, messageID, emojiType); err != nil {
			return err
		}
		return s.refreshReaction(ctx, messageID)
	}
	self, _, err := s.Store.GetState(ctx, KeySelfOpenID)
	if err != nil {
		return err
	}
	items, err := s.Client.ListReactions(ctx, messageID, emojiType)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.OperatorID != self {
			continue
		}
		if err := s.Client.DeleteReaction(ctx, messageID, it.ReactionID); err != nil {
			return err
		}
		return s.refreshReaction(ctx, messageID)
	}
	// Nothing of the reader's to take back: somebody else's reaction, or one
	// already gone. Either way the stored summary is behind, so refresh it.
	return s.refreshReaction(ctx, messageID)
}

// refreshReaction re-reads one message's reactions.
func (s *Syncer) refreshReaction(ctx context.Context, messageID string) error {
	blocks, err := s.Client.ReactionCounts(ctx, []string{messageID})
	if err != nil {
		return err
	}
	return s.Store.UpdateReactions(ctx, messageID, rawString(blocks[messageID]))
}

// checkReadStatus asks Feishu about the messages q selects, records each
// answer and schedules the next check of the ones still unread. It fills in
// the parts of q that every caller shares: the user's own open_id, whose
// messages carry no read flag, and the horizon beyond which polling stops.
func (s *Syncer) checkReadStatus(ctx context.Context, now time.Time, q store.ReadCheckQuery) (int, error) {
	self, _, err := s.Store.GetState(ctx, KeySelfOpenID)
	if err != nil || self == "" {
		return 0, err
	}
	q.Self, q.SinceMs = self, now.Add(-readStatusHorizon).UnixMilli()
	ids, err := s.Store.ReadStatusCandidates(ctx, q)
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
