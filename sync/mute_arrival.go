package sync

import (
	"context"
	"time"

	"github.com/amzyang/larkim/larkcli"
)

// refreshMuteForChats asks Feishu for do-not-disturb on chatIDs and stores the
// answers. Callers batch ids themselves; one round trip covers the whole slice.
func (s *Syncer) refreshMuteForChats(ctx context.Context, chatIDs []string, now time.Time) (int, error) {
	chatIDs = UniqueStrings(chatIDs)
	if len(chatIDs) == 0 {
		return 0, nil
	}
	muted, unknown, err := s.Client.MuteStatus(ctx, chatIDs)
	if err != nil {
		return 0, err
	}
	if err := s.Store.SetMuteStatus(ctx, muted, unknown, now.UnixMilli()); err != nil {
		return 0, err
	}
	return len(muted), nil
}

// muteOnMessageArrival refreshes mute for chats that just gained a first-seen
// message or whose badge count rose from zero, so discovery does not wait for
// the periodic chats refresh.
func (s *Syncer) muteOnMessageArrival(ctx context.Context, msgs []larkcli.RawMessage, unknown []string, chats map[string]struct{}, beforeBadge map[string]int64, now time.Time) {
	unk := make(map[string]struct{}, len(unknown))
	for _, id := range unknown {
		unk[id] = struct{}{}
	}
	freshChat := make(map[string]bool)
	for _, m := range msgs {
		if _, ok := unk[m.MessageID]; ok {
			freshChat[m.ChatID] = true
		}
	}
	var ids []string
	for id := range chats {
		after, err := s.Store.ChatBadgeCount(ctx, id)
		if err != nil {
			s.log().WarnContext(ctx, "mute on arrival: badge count", "chat", id, "err", err)
			continue
		}
		if freshChat[id] || (beforeBadge[id] == 0 && after > 0) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	if _, err := s.refreshMuteForChats(ctx, ids, now); err != nil {
		s.log().WarnContext(ctx, "mute on arrival", "err", err, "chats", ids)
	}
}
