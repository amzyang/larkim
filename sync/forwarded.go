package sync

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// maxForwardAttempts bounds the retries of a bundle whose expansion failed
// for a reason worth repeating — a timeout, a rate limit. A refusal never
// gets here: it is settled on the first answer.
const maxForwardAttempts = 5

// forwardRetryDelay is the wait before the next attempt at a bundle that has
// already failed `attempts` times; 0 means give up.
func forwardRetryDelay(attempts int) time.Duration {
	if attempts >= maxForwardAttempts {
		return 0
	}
	return ReadCheckDelay(attempts)
}

// expandForwards drains a bounded slice of the bundle queue. One bundle is
// one call — the endpoint takes a single root id — so this rides the
// background lane: fanning out on the beat lane would starve the chat the
// reader has open, which is what that lane exists for.
func (s *Syncer) expandForwards(ctx context.Context, now time.Time) (int, error) {
	due, err := s.Store.ForwardRootsDue(ctx, now.UnixMilli(), s.Opt.ForwardsPerTick)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	done := 0
	for _, root := range due {
		ok, err := s.expandForward(ctx, root, now)
		if err != nil {
			return done, err
		}
		if ok {
			done++
		}
	}
	return done, nil
}

// expandForward asks for one bundle's children and stores them. A refusal
// from Feishu is recorded on the queue row rather than returned, so one
// unreadable forward does not stop the sweep; the summary line reads
// last_error to say the bundle cannot be opened.
func (s *Syncer) expandForward(ctx context.Context, root store.ForwardRoot, now time.Time) (bool, error) {
	items, err := s.Client.ForwardedMessages(ctx, root.RootMessageID)
	if err != nil {
		return false, s.recordForwardFailure(ctx, root, err, now)
	}
	kids := toForwarded(root.RootMessageID, items)
	if err := s.Store.SaveForwarded(ctx, root.RootMessageID, kids, now.UnixMilli()); err != nil {
		return false, err
	}
	if s.Opt.DataDir == "" {
		return true, nil
	}
	// The pictures inside a bundle download under the bundle's own id: the
	// resource endpoint refuses a child message's id, which is also why
	// ExtractRendered registers them there.
	var refs []store.ResourceRef
	for _, k := range kids {
		refs = append(refs, ExtractResources(root.RootMessageID, k.MsgType, k.ContentRaw)...)
	}
	return true, s.Store.AddPendingResources(ctx, refs)
}

// recordForwardFailure settles or reschedules a bundle. A forward is frozen,
// so a permanent refusal is an answer: asking again cannot change it.
func (s *Syncer) recordForwardFailure(ctx context.Context, root store.ForwardRoot, cause error, now time.Time) error {
	reason := cause.Error()
	le, ok := errors.AsType[*larkcli.Error](cause)
	delay := forwardRetryDelay(root.Attempts + 1)
	if (ok && le.IsPermanent()) || delay == 0 {
		s.log().InfoContext(ctx, "forward will not expand", "message_id", root.RootMessageID, "error", reason)
		return s.Store.MarkForwardRefused(ctx, root.RootMessageID, reason, now.UnixMilli())
	}
	return s.Store.MarkForwardFailed(ctx, root.RootMessageID, reason, now.Add(delay).UnixMilli())
}

// toForwarded maps one bundle's flat items onto its child rows.
func toForwarded(rootMessageID string, items []larkcli.RawForwarded) []store.Forwarded {
	kids := make([]store.Forwarded, 0, len(items))
	for _, it := range items {
		// The bundle is one of its own items, told apart by id: a missing
		// message_position decodes as 0, which a real position 0 cannot be
		// told from.
		if it.MessageID == rootMessageID && it.UpperMessageID == "" {
			continue
		}
		mentions, _ := json.Marshal(it.Mentions)
		kids = append(kids, store.Forwarded{
			RootMessageID: rootMessageID, UpperMessageID: it.UpperMessageID, MessageID: it.MessageID,
			ChatID: it.ChatID, MsgType: it.MsgType,
			SenderID: senderIDOf(it.RawMessage), SenderName: it.Sender.SenderName,
			CreateMs: int64(it.CreateTime), ContentRaw: it.Body.Content,
			MentionsJSON: string(mentions), RawJSON: string(it.Raw),
		})
	}
	// Siblings are numbered by when they were said, which is the order the
	// bundle reads in. The sort is stable, so two messages sharing a
	// millisecond keep the order the API listed them in.
	slices.SortStableFunc(kids, func(a, b store.Forwarded) int {
		return cmp.Or(cmp.Compare(a.UpperMessageID, b.UpperMessageID), cmp.Compare(a.CreateMs, b.CreateMs))
	})
	seq, upper := 0, ""
	for i := range kids {
		if kids[i].UpperMessageID != upper {
			seq, upper = 0, kids[i].UpperMessageID
		}
		kids[i].Seq = seq
		seq++
	}
	return kids
}
