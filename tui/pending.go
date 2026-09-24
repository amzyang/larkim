package tui

import (
	"encoding/json"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// pendingText stands in for a message lark-cli has not rendered yet. A body
// arrives one sync step before its rendering and, in a backfilled range, can
// wait minutes behind the render queue, so the raw OpenAPI JSON would be a
// common sight. Text carries its own body, which reads fine with the mention
// placeholders still in it once the paragraphs a rich-text body arrives in are
// flattened; anything else is named by its type until the rendering lands and
// replaces this.
func pendingText(msgType, contentRaw string) string {
	if msgType == "text" {
		var v struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(contentRaw), &v) == nil && v.Text != "" {
			return sync.UnwrapParagraphs(v.Text)
		}
	}
	return msgTypeLabel(msgType)
}

// pendingSummary is pendingText for a chat's last message, on one line the
// way a rendered summary is.
func pendingSummary(c store.Chat) string {
	return flatten(expandEmoji(pendingText(c.LastMsgType, c.LastContentRaw)))
}
