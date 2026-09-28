package sync

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// textLocal renders a plain text message. Everything it says is in its own
// body but the names: Feishu spells a mention as an @_user_n placeholder and
// leaves the name in the message's mention list.
func textLocal(m store.PendingLocalMessage) string {
	var body struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(m.ContentRaw), &body) != nil {
		return m.ContentRaw
	}
	return ResolveMentions(UnwrapParagraphs(body.Text), m.MentionsJSON)
}

// mention is one entry of a message's mention list, as mentions_json holds it.
type mention struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// ResolveMentions puts the names back where a body's @_user_n placeholders
// stand. Longest key first, because @_user_1 is a prefix of @_user_10 and a
// message with ten of them is not rare enough to get wrong.
func ResolveMentions(text, mentionsJSON string) string {
	var items []mention
	if json.Unmarshal([]byte(mentionsJSON), &items) != nil {
		return text
	}
	slices.SortStableFunc(items, func(a, b mention) int { return len(b.Key) - len(a.Key) })
	for _, it := range items {
		if it.Key == "" || it.Name == "" {
			continue
		}
		text = strings.ReplaceAll(text, it.Key, "@"+it.Name)
	}
	return text
}

// mentionsJSON is the `[{id,key,name}]` docs/SCHEMA.md documents. The API
// sends an id_type and a tenant_key besides, which nothing reads and which
// would only pad a column whose ids are matched as text.
func mentionsJSON(ms []larkcli.RawMention) string {
	if len(ms) == 0 {
		return ""
	}
	out := make([]mention, len(ms))
	for i, m := range ms {
		out[i].ID, out[i].Key, out[i].Name = m.ID, m.Key, m.Name
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}
