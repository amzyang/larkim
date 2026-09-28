package tui

import (
	"encoding/json"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// pendingText stands in for a message with no rendering yet. A body arrives
// one sync step before its rendering and, in a backfilled range, can wait
// minutes behind the render queue, so the raw OpenAPI JSON would be a common
// sight. Text carries its own words, and the mention list that arrived with it
// says whose names the placeholders stand for; a post carries its own words
// too; an event carries the same fields larkim will render it from; anything
// else is named by its type until the rendering lands and replaces this.
func pendingText(msgType, contentRaw, mentionsJSON string) string {
	switch msgType {
	case "text":
		var v struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(contentRaw), &v) == nil && v.Text != "" {
			return sync.ResolveMentions(sync.UnwrapParagraphs(v.Text), mentionsJSON)
		}
	case "post":
		if text := postText(contentRaw); text != "" {
			return text
		}
	case "calendar", "share_calendar_event", "general_calendar":
		if c, ok := sync.ParseCalendar(contentRaw); ok {
			return c.Text(msgType, time.Local)
		}
	}
	return msgTypeLabel(msgType)
}

// pendingSummary is pendingText for a chat's last message, on one line the
// way a rendered summary is.
func pendingSummary(c store.Chat) string {
	return flatten(expandEmoji(pendingText(c.LastMsgType, c.LastContentRaw, c.LastMentionsJSON)))
}
