package sync

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/amzyang/larkim/store"
)

// systemSlotRe matches a slot in a system message template, including the
// bare {} used for positional ones.
var systemSlotRe = regexp.MustCompile(`\{(\w*)}`)

// unresolvedSlot stands in for a slot the message carries no value for. The
// API body ships template, from_user, to_chatters and divider_text only, so
// slots such as {old_group_name} or {count} can never be filled.
const unresolvedSlot = "…"

// callEndWindowMs bounds how long after a call's end_time Feishu may stamp
// the marker that closes it. Measured at 85ms to 1.2s across a year of chats,
// so the window only has to be wide enough to never split the pair.
const callEndWindowMs = 5_000

// systemText renders a system message from the bodies larkim already stores,
// so this msg_type needs no render call. Feishu closes a call with a template
// that is a single space: the text is nowhere in the API, and the client
// reads the length off the video_chat message the call left behind. A p2p
// call leaves none, so there only the fact that it ended can be told.
func systemText(m store.PendingSystemMessage) string {
	if text := templateText(m.ContentRaw); text != "" {
		return text
	}
	if ms, ok := callSpan(m.CallRaw, m.CreateMs); ok {
		return "Meeting ended: " + callLength(ms)
	}
	return "Call ended"
}

// templateText fills the template's slots from the rest of the body.
func templateText(contentRaw string) string {
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

// callSpan reads how long the call ran for off a video_chat body, provided
// that call ended just before the marker closing it was stamped.
func callSpan(callRaw string, markerMs int64) (int64, bool) {
	var c struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}
	if json.Unmarshal([]byte(callRaw), &c) != nil {
		return 0, false
	}
	start, end := unixMs(c.StartTime), unixMs(c.EndTime)
	if end <= start || markerMs < end || markerMs-end > callEndWindowMs {
		return 0, false
	}
	return end - start, true
}

func unixMs(s string) int64 {
	ms, _ := strconv.ParseInt(s, 10, 64)
	return ms
}

// callLength spells a duration out over the two units that carry it, with no
// zero tail: 32s, 24m28s, 1h52m.
func callLength(ms int64) string {
	s := ms / 1000
	switch h, m, sec := s/3600, s%3600/60, s%60; {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	case m > 0 && sec > 0:
		return fmt.Sprintf("%dm%ds", m, sec)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
