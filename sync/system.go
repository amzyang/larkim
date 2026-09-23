package sync

import (
	"encoding/json"
	"regexp"
	"strings"
)

// systemSlotRe matches a slot in a system message template, including the
// bare {} used for positional ones.
var systemSlotRe = regexp.MustCompile(`\{(\w*)}`)

// unresolvedSlot stands in for a slot the message carries no value for. The
// API body ships template, from_user, to_chatters and divider_text only, so
// slots such as {old_group_name} or {count} can never be filled.
const unresolvedSlot = "…"

// systemText renders a system message from the API body larkim already
// stores, so this msg_type needs no render call. A template with nothing to
// say renders empty, which is what the chat list falls back on.
func systemText(contentRaw string) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(contentRaw), &body); err != nil {
		return ""
	}
	tmpl, _ := body["template"].(string)
	return strings.TrimSpace(systemSlotRe.ReplaceAllStringFunc(tmpl, func(slot string) string {
		if v := systemSlot(body[slot[1:len(slot)-1]]); v != "" {
			return v
		}
		return unresolvedSlot
	}))
}

// systemSlot reads one slot out of the body: the people arrays join their
// names, divider_text nests its text, everything else is a plain string.
func systemSlot(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []any:
		var names []string
		for _, u := range v {
			if s, ok := u.(string); ok && s != "" {
				names = append(names, s)
			}
		}
		return strings.Join(names, ", ")
	case map[string]any:
		text, _ := v["text"].(string)
		return text
	}
	return ""
}
