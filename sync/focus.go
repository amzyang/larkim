package sync

import (
	"context"
	"fmt"
	"time"
)

// hotLookback bounds what one refresh asks for. The listing endpoint reads the
// message store rather than the search index, so a message is listable as soon
// as it exists and the window only has to outlast a poll or two. Keeping it
// fixed keeps the call's cost constant: a window anchored on the chat cursor
// would grow with how long the chat had been quiet, which is backwards.
const hotLookback = time.Minute

// hotRenderBatch is one lark-cli rendering call. The reader sees a text body
// before its rendering lands, so falling behind here costs polish, not words.
const hotRenderBatch = 50

// PullOlder fetches one page of history from behind a chat's floor and moves
// the floor to where that page ends, so repeated calls walk the chat back to
// its start. It returns how many messages landed.
//
// Backfill only ever covered the last backfill_days and marked itself done, so
// without this a chat's stored history has a hard bottom that raising the
// setting afterwards cannot lift. A reader who scrolls past that bottom is the
// only signal worth spending a call on, which is why this is pulled rather
// than swept.
func (s *Syncer) PullOlder(ctx context.Context, chatID string) (int, error) {
	chat, err := s.Store.GetChat(ctx, chatID)
	if err != nil {
		return 0, err
	}
	if chat.HistoryFloorMs == 0 {
		return 0, nil // the whole chat is already stored
	}
	// The endpoint bounds by whole seconds and includes the bound, so a page
	// always re-reads the message the last one ended on. Paying for that one
	// row is the safe side of a rounding this code does not control: asking
	// for strictly less would drop whatever shares that second with it.
	floor := time.UnixMilli(chat.HistoryFloorMs)
	msgs, more, err := s.Client.OlderMessagesRaw(ctx, chatID, floor)
	if err != nil {
		return 0, fmt.Errorf("pull older %s: %w", chatID, err)
	}
	now := s.now()
	// The page names the threads whose replies belong with it. Those replies
	// live in a container of their own, unbounded here because a thread is
	// small and its replies may run past the floor the roots sit behind.
	var threads []string
	next := chat.HistoryFloorMs
	for _, m := range msgs {
		if m.ThreadID != "" {
			threads = append(threads, m.ThreadID)
		}
		next = min(next, int64(m.CreateTime))
	}
	replies, err := s.listThreads(ctx, UniqueStrings(threads), time.Time{}, floor)
	if err != nil {
		return 0, fmt.Errorf("pull older threads %s: %w", chatID, err)
	}
	// Not pullChat: that moves the chat cursor, which names the newest
	// message pulled. Walking backwards has nothing to say about the newest.
	n, _, err := s.upsertRaw(ctx, append(msgs, replies...), now)
	if err != nil {
		return n, err
	}
	// Whether anything is left is the server's own answer rather than a guess
	// from the page's length, so a page the API cut short for its own reasons
	// does not read as the start of the chat.
	if !more {
		next = 0
	}
	if err := s.Store.SetChatHistoryFloor(ctx, chatID, next); err != nil {
		return n, err
	}
	s.changed(n)
	r, err := s.renderPending(ctx, chatID, hotRenderBatch, now)
	s.changed(r)
	return n, err
}

// RefreshChat re-lists the newest slice of the chat somebody is reading,
// straight from the message store. Every other discovery path goes through
// messages/search, whose index runs about seven seconds behind; the listing
// does not, and that difference is the whole reason the open chat is pulled on
// its own. threadID, set while a thread pane is open, brings replies rooted
// before the window with it: pullChat only follows threads it saw a root for.
//
// It is deliberately best-effort. The cross-chat search still covers this chat
// with its own overlap, so a refusal here loses nothing but time.
func (s *Syncer) RefreshChat(ctx context.Context, chatID, threadID string) (int, error) {
	if chatID == "" {
		return 0, nil
	}
	now := s.now()
	since := now.Add(-hotLookback)
	n, _, err := s.pullChat(ctx, chatID, since, time.Time{}, now)
	if err != nil {
		return 0, fmt.Errorf("refresh chat %s: %w", chatID, err)
	}
	if threadID != "" {
		replies, err := s.Client.ListMessagesRaw(ctx, "thread", threadID, since, time.Time{})
		if err != nil {
			return n, fmt.Errorf("refresh thread %s: %w", threadID, err)
		}
		m, _, err := s.upsertRaw(ctx, replies, now)
		if err != nil {
			return n, err
		}
		n += m
	}
	// A body is readable before its rendering, so the words go up now and the
	// mentions, cards and pictures settle a call later.
	s.changed(n)
	r, err := s.renderPending(ctx, chatID, hotRenderBatch, now)
	s.changed(r)
	return n, err
}
