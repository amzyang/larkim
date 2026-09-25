package sync

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// KeyDocScanID is the newest message row whose links have been registered.
const KeyDocScanID = "doc_scan_id"

const docScanBatch = 500

// docRefreshEvery is how long a title is trusted. Documents get renamed, and
// a name shown in place of a URL is worse than the URL once it is wrong.
const docRefreshEvery = 7 * 24 * time.Hour

// docRetryEvery is how long a document the endpoint answered about without
// naming waits. Each token comes back either titled or refused, so one that
// comes back as neither leaves nothing to record and its row stays due;
// asking again in a second would only spend the drive quota on the same
// silence.
const docRetryEvery = time.Hour

// registerExistingDocLinks walks messages stored before links were read,
// a bounded slice per tick. Steady state costs one empty query.
func (s *Syncer) registerExistingDocLinks(ctx context.Context) error {
	v, _, _ := s.Store.GetState(ctx, KeyDocScanID)
	after, _ := strconv.ParseInt(v, 10, 64)
	rows, err := s.Store.MessagesAfterIDForDocScan(ctx, after, docScanBatch)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var refs []store.DocRef
	for _, r := range rows {
		refs = append(refs, store.FindDocRefs(r.Content)...)
		after = max(after, r.ID)
	}
	if err := s.Store.AddPendingDocLinks(ctx, refs); err != nil {
		return err
	}
	return s.Store.SetState(ctx, KeyDocScanID, strconv.FormatInt(after, 10))
}

// resolveDocLinks names the documents linked to from messages. A document
// this identity cannot read is settled rather than retried: the endpoint
// refuses one for an unsupported type, a missing document or a missing
// permission, and none of the three changes by asking again.
func (s *Syncer) resolveDocLinks(ctx context.Context, now time.Time) (int, error) {
	if err := s.registerExistingDocLinks(ctx); err != nil {
		return 0, err
	}
	due, err := s.Store.DocLinksDue(ctx, now.UnixMilli(), s.Opt.DocLinksPerTick*larkcli.MaxDocTokensPerBatch)
	if err != nil {
		return 0, err
	}
	refreshAt := now.Add(docRefreshEvery).UnixMilli()
	retryAt := now.Add(docRetryEvery).UnixMilli()
	done := 0
	for batch := range slices.Chunk(due, larkcli.MaxDocTokensPerBatch) {
		titles, err := s.Client.DocTitles(ctx, docRefs(batch))
		if err != nil {
			return done, err
		}
		answered := make(map[store.DocRef]bool, len(batch))
		for _, t := range titles.Found {
			ref := store.DocRef{Type: t.Ref.Type, Token: t.Ref.Token}
			if err := s.Store.MarkDocTitle(ctx, ref, t.Title, t.Type, refreshAt); err != nil {
				return done, err
			}
			answered[ref] = true
			done++
		}
		for _, r := range titles.Denied {
			ref := store.DocRef{Type: r.Type, Token: r.Token}
			if err := s.Store.MarkDocDenied(ctx, ref); err != nil {
				return done, err
			}
			answered[ref] = true
			done++
		}
		var silent []store.DocRef
		for _, r := range batch {
			if !answered[r] {
				silent = append(silent, r)
			}
		}
		if err := s.Store.DeferDocLinks(ctx, silent, retryAt); err != nil {
			return done, err
		}
	}
	return done, nil
}

func docRefs(refs []store.DocRef) []larkcli.DocRef {
	out := make([]larkcli.DocRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, larkcli.DocRef{Type: r.Type, Token: r.Token})
	}
	return out
}
