package sync

import (
	"encoding/json"
	"strconv"
	"strings"
)

// VideoChat is what a video_chat body says about a call. Feishu writes the
// message when the call starts and rewrites it with an end_time when the
// call closes, so one body serves both the invite and the record.
type VideoChat struct {
	Topic      string
	MeetNumber string // digits only, the form a join link takes
	StartMs    int64
	EndMs      int64
}

// ParseVideoChat reads a video_chat body. It reports false for one naming
// neither a topic nor a meeting: that is a shape this renderer has never
// seen, not a call it can describe.
func ParseVideoChat(contentRaw string) (VideoChat, bool) {
	var body struct {
		Topic      string `json:"topic"`
		MeetNumber string `json:"meet_number"`
		StartTime  string `json:"start_time"`
		EndTime    string `json:"end_time"`
	}
	if json.Unmarshal([]byte(contentRaw), &body) != nil {
		return VideoChat{}, false
	}
	v := VideoChat{
		Topic:      strings.TrimSpace(body.Topic),
		MeetNumber: meetNumber(body.MeetNumber),
		StartMs:    unixMs(body.StartTime),
		EndMs:      unixMs(body.EndTime),
	}
	return v, v.Topic != "" || v.MeetNumber != ""
}

// meetNumber closes a meeting number to digits: the value is handed to the
// desktop client as a link, and anything else would send it somewhere else.
func meetNumber(s string) string {
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

func unixMs(s string) int64 {
	ms, _ := strconv.ParseInt(s, 10, 64)
	return ms
}

// Live reports whether the call is still running, which is what makes it
// worth joining. end_time arrives with the update that closes the call.
func (v VideoChat) Live() bool { return v.EndMs <= v.StartMs }

// Span is how long the call ran, and false while it still runs.
func (v VideoChat) Span() (int64, bool) {
	if v.Live() {
		return 0, false
	}
	return v.EndMs - v.StartMs, true
}

// Text is the one line stored as the message's content: lark-cli's own
// placeholder, with everything the body carries behind it, so the chat list,
// the search index and a copy all say which meeting this was.
func (v VideoChat) Text() string {
	var parts []string
	if v.Topic != "" {
		parts = append(parts, v.Topic)
	}
	if v.MeetNumber != "" {
		parts = append(parts, v.MeetNumber)
	}
	if ms, ok := v.Span(); ok {
		parts = append(parts, CallLength(ms))
	}
	if len(parts) == 0 {
		return videoChatPlaceholder
	}
	return videoChatPlaceholder + " " + strings.Join(parts, " · ")
}

// videoChatPlaceholder is what lark-cli renders a video_chat message to, and
// what larkim keeps at the front of its own rendering.
const videoChatPlaceholder = "[Video call]"

// videoChatText renders a video_chat message from the body on disk.
func videoChatText(contentRaw string) string {
	v, ok := ParseVideoChat(contentRaw)
	if !ok {
		return videoChatPlaceholder
	}
	return v.Text()
}
