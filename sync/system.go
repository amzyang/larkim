package sync

import (
	"encoding/json"
	"fmt"
	"regexp"
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

// localText renders a message larkim renders itself, from the bodies it
// already stores.
func localText(m store.PendingLocalMessage) string {
	if m.MsgType == "video_chat" {
		return videoChatText(m.ContentRaw)
	}
	return systemText(m)
}

// systemText renders a system message from the bodies larkim already stores,
// so this msg_type needs no render call. A blank template carries no text
// anywhere in the API — the client fills it from state of its own — and every
// blank one seen so far closes a call, which the video_chat message the call
// left behind can date. A p2p call leaves none, so there only the fact that a
// call ended can be told, and that much is inferred rather than read.
func systemText(m store.PendingLocalMessage) string {
	tmpl, body, ok := systemBody(m.ContentRaw)
	if !ok {
		return ""
	}
	if text := fillSlots(tmpl, body); text != "" {
		return text
	}
	if ms, ok := callSpan(m.CallRaw, m.CreateMs); ok {
		return "Meeting ended: " + CallLength(ms)
	}
	return "Call ended"
}

// systemBody splits a body into its template and the values that fill it,
// reporting whether larkim can read it at all. A body carrying no template is
// a shape this renderer has never seen, not a blank marker.
func systemBody(contentRaw string) (string, map[string]any, bool) {
	var body map[string]any
	if err := json.Unmarshal([]byte(contentRaw), &body); err != nil {
		return "", nil, false
	}
	tmpl, ok := body["template"].(string)
	return tmpl, body, ok
}

// fillSlots fills the template's slots from the rest of the body.
func fillSlots(tmpl string, body map[string]any) string {
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
	v, ok := ParseVideoChat(callRaw)
	if !ok {
		return 0, false
	}
	ms, ended := v.Span()
	if !ended || markerMs < v.EndMs || markerMs-v.EndMs > callEndWindowMs {
		return 0, false
	}
	return ms, true
}

// CallLength spells a duration out over the two units that carry it, with no
// zero tail: 32s, 24m28s, 1h52m.
func CallLength(ms int64) string {
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
