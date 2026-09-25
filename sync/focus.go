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
