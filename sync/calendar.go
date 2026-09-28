package sync

import (
	json "encoding/json/v2"
	"strconv"
	"strings"
	"time"
)

// Calendar is what a calendar body says about an event. The three msg_types
// that carry one — the invite the calendar bot posts, the card a colleague
// shares, and the RSVP card — all ship the same fields.
//
// share_token is deliberately not among them. It is the credential that joins
// an event, and the only one: calendar/v4/calendars/join_event takes nothing
// else, and there is no path in by event id. Everything held here reaches
// messages.content, the search index and the screen, so the token stays out.
type Calendar struct {
	Summary    string
	StartMs    int64
	EndMs      int64
	CalendarID string
	EventID    string
}

// LocalCalendar reports whether msgType names a calendar body, which larkim
// renders from the body itself rather than asking lark-cli for a rendering.
func LocalCalendar(msgType string) bool {
	switch msgType {
	case "calendar", "share_calendar_event", "general_calendar":
		return true
	}
	return false
}

// ParseCalendar reads a calendar body. It reports false for one naming
// neither an event nor a time: that is a shape this renderer has never seen,
// not an event it can describe.
func ParseCalendar(contentRaw string) (Calendar, bool) {
	var body struct {
		Summary        string `json:"summary"`
		StartTime      string `json:"start_time"`
		EndTime        string `json:"end_time"`
		OpenCalendarID string `json:"open_calendar_id"`
		OpenEventID    string `json:"open_event_id"`
	}
	if json.Unmarshal([]byte(contentRaw), &body) != nil {
		return Calendar{}, false
	}
	c := Calendar{
		// An event is titled in a box that takes newlines, and the title is
		// read on one line everywhere larkim puts it.
		Summary:    strings.Join(strings.Fields(body.Summary), " "),
		StartMs:    epochMs(body.StartTime),
		EndMs:      epochMs(body.EndTime),
		CalendarID: body.OpenCalendarID,
		EventID:    body.OpenEventID,
	}
	return c, c.Summary != "" || c.StartMs != 0
}

// epochMs reads a calendar timestamp. Feishu answers milliseconds, but the
// converter this renderer takes over from reads seconds as well, and a body
// read one way here and the other way there would be off by a factor of a
// thousand. The split is by magnitude: a millisecond value that small is 1970.
func epochMs(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	if n < 1e12 {
		n *= 1000
	}
	return n
}

// The stamps a span is spelled with. An event is scheduled to the minute, so
// the seconds a timestamp could carry are noise on every one of them.
const (
	calendarStamp = "2006-01-02 15:04"
	calendarClock = "15:04"
)

// Span is when the event runs. The closing half drops its date when the event
// ends on the day it started, which is nearly every event.
func (c Calendar) Span(loc *time.Location) string {
	if c.StartMs == 0 {
		return ""
	}
	start := time.UnixMilli(c.StartMs).In(loc)
	if c.EndMs <= c.StartMs {
		return start.Format(calendarStamp)
	}
	end := time.UnixMilli(c.EndMs).In(loc)
	layout := calendarStamp
	if sameDay(start, end) {
		layout = calendarClock
	}
	return start.Format(calendarStamp) + " ~ " + end.Format(layout)
}

// sameDay reports whether two times fall on the same calendar day.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// Text is the one line stored as the message's content, so the chat list, the
// search index and a copy all say which event this was. It keeps the label
// the chat list falls back to at the front, the way a call's rendering does.
func (c Calendar) Text(msgType string, loc *time.Location) string {
	var parts []string
	if c.Summary != "" {
		parts = append(parts, c.Summary)
	}
	if span := c.Span(loc); span != "" {
		parts = append(parts, span)
	}
	label := calendarPlaceholder(msgType)
	if len(parts) == 0 {
		return label
	}
	return label + " " + strings.Join(parts, " · ")
}

// calendarPlaceholder names a calendar body by its msg_type, in the words the
// chat list already falls back to when it has nothing but the type.
func calendarPlaceholder(msgType string) string {
	if msgType == "share_calendar_event" {
		return "[Shared Event]"
	}
	return "[Event]"
}

// calendarText renders a calendar message from the body on disk.
func calendarText(msgType, contentRaw string) string {
	c, ok := ParseCalendar(contentRaw)
	if !ok {
		return calendarPlaceholder(msgType)
	}
	return c.Text(msgType, time.Local)
}
