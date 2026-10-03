package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const (
	eventRaw = `{"summary":"平台组周会","start_time":"1788143400000","end_time":"1788148800000",` +
		`"open_calendar_id":"cal_team","open_event_id":"evt-a_0"}`
	eventStartMs = 1788143400000
)

// eventSpan is what the card's second row reads, built from the renderer the
// card itself uses so the assertion holds in whatever zone the test runs in.
func eventSpan() string {
	return sync.Calendar{StartMs: eventStartMs, EndMs: 1788148800000}.Span(time.Local)
}

// calendarRaw is an event drawn from its own body, with no rendering behind
// it: that is the state every calendar message is in when it lands.
func calendarRaw(msgType, raw string) []store.Message {
	return []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a",
		MsgType: msgType, ContentRaw: raw, CreateMs: msgAt(23, 9, 0)}}
}

// calendarLines is what a body draws, one string per row, past the day rule
// and the sender line.
func calendarLines(t *testing.T, msgType, raw string) []string {
	t.Helper()
	rows := renderRows(calendarRaw(msgType, raw), baseStyle())
	out := make([]string, 0, len(rows))
	for _, r := range rows[2:] {
		out = append(out, strings.TrimRight(ansi.Strip(segText(r)), " "))
	}
	return out
}

func TestCalendarRows_DrawTheEventWithoutARendering(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"📅 平台组周会", eventSpan(), " Open"},
		calendarLines(t, "calendar", eventRaw))
}

func TestCalendarRows_OfferTheOpenButtonOnAnInvite(t *testing.T) {
	t.Parallel()
	zones := rowZones(renderRows(calendarRaw("calendar", eventRaw), baseStyle()))
	require.Len(t, zones, 1)
	require.Equal(t, []string{applink.EventLink("cal_team", "evt-a_0", eventStartMs)}, zones[0].urls)
}

func TestCalendarRows_WithholdTheButtonWhenTheBodyNamesNoEvent(t *testing.T) {
	t.Parallel()
	// The client needs both ids to find an event; without them the button
	// would land on nothing.
	require.Equal(t, []string{"📅 平台组周会", eventSpan()},
		calendarLines(t, "calendar", `{"summary":"平台组周会","start_time":"1788143400000","end_time":"1788148800000"}`))
}

func TestCalendarRows_LeaveASharedEventWithoutAButton(t *testing.T) {
	t.Parallel()
	// A shared event sits on somebody else's calendar, which the detail page
	// cannot show a reader who has not subscribed to it.
	shared := strings.Replace(eventRaw, `"open_calendar_id":"cal_team"`,
		`"open_calendar_id":"cal_peer","share_token":"cse_secret"`, 1)
	require.Equal(t, []string{"📅 平台组周会", eventSpan()},
		calendarLines(t, "share_calendar_event", shared))
	require.Empty(t, rowZones(renderRows(calendarRaw("share_calendar_event", shared), baseStyle())))
}

func TestCalendarRows_KeepTheShareTokenOffTheScreen(t *testing.T) {
	t.Parallel()
	shared := strings.Replace(eventRaw, `"open_event_id"`, `"share_token":"cse_secret","open_event_id"`, 1)
	rows := renderRows(calendarRaw("share_calendar_event", shared), baseStyle())
	require.NotContains(t, rowText(rows), "cse_secret")
}

func TestCalendarRows_FallBackToTheRenderingWhenTheBodyIsUnreadable(t *testing.T) {
	t.Parallel()
	msgs := calendarRaw("calendar", "not json")
	msgs[0].Content, msgs[0].RenderedAt = "[Event] 平台组周会", 1
	rows := renderRows(msgs, baseStyle())
	require.Contains(t, rowText(rows), "[Event] 平台组周会")
}

func TestCalendarRows_NameAnEventWhoseBodyCarriesNoTitle(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"📅 Event", eventSpan()},
		calendarLines(t, "general_calendar", `{"start_time":"1788143400000","end_time":"1788148800000"}`))
}
