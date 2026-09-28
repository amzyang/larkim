package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cst is the zone every span in these tests is read in, so none of them
// depends on the machine's own.
var cst = time.FixedZone("CST", 8*60*60)

func TestParseCalendar_ReadsTheFieldsEveryCalendarBodyCarries(t *testing.T) {
	c, ok := ParseCalendar(`{"summary":"平台组周会","start_time":"1788143400000","end_time":"1788148800000",` +
		`"open_calendar_id":"cal_team","open_event_id":"evt_a_0"}`)
	require.True(t, ok)
	require.Equal(t, Calendar{
		Summary:    "平台组周会",
		StartMs:    1788143400000,
		EndMs:      1788148800000,
		CalendarID: "cal_team",
		EventID:    "evt_a_0",
	}, c)
}

func TestParseCalendar_TakesSecondsAndMilliseconds(t *testing.T) {
	// lark-cli's converter reads both, and a body read one way here and the
	// other way there would be off by a factor of a thousand.
	ms, ok := ParseCalendar(`{"summary":"平台组周会","start_time":"1788143400000"}`)
	require.True(t, ok)
	sec, ok := ParseCalendar(`{"summary":"平台组周会","start_time":"1788143400"}`)
	require.True(t, ok)
	require.Equal(t, ms.StartMs, sec.StartMs)
}

func TestParseCalendar_FoldsAMultiLineSummaryOntoOneLine(t *testing.T) {
	c, ok := ParseCalendar(`{"summary":"改会议室 \n项目对齐","start_time":"1788143400000"}`)
	require.True(t, ok)
	require.Equal(t, "改会议室 项目对齐", c.Summary)
}

func TestParseCalendar_RefusesABodyNamingNeitherEventNorTime(t *testing.T) {
	for _, raw := range []string{"", "{}", "not json", `{"summary":"  "}`, `{"start_time":"0"}`} {
		_, ok := ParseCalendar(raw)
		require.False(t, ok, "%q", raw)
	}
}

func TestCalendarSpan_DropsTheDateOnTheClosingHalfWithinOneDay(t *testing.T) {
	c := Calendar{StartMs: 1788143400000, EndMs: 1788148800000}
	require.Equal(t, "2026-08-31 10:30 ~ 12:00", c.Span(cst))
}

func TestCalendarSpan_KeepsBothDatesAcrossMidnight(t *testing.T) {
	c := Calendar{StartMs: 1788143400000, EndMs: 1788143400000 + 20*60*60*1000}
	require.Equal(t, "2026-08-31 10:30 ~ 2026-09-01 06:30", c.Span(cst))
}

func TestCalendarSpan_IsJustTheStartWithoutAnEnd(t *testing.T) {
	require.Equal(t, "2026-08-31 10:30", Calendar{StartMs: 1788143400000}.Span(cst))
	require.Empty(t, Calendar{EndMs: 1788148800000}.Span(cst))
}

func TestCalendarText_NamesTheEventTheWayTheChatListDoes(t *testing.T) {
	c := Calendar{Summary: "平台组周会", StartMs: 1788143400000, EndMs: 1788148800000}
	require.Equal(t, "[Event] 平台组周会 · 2026-08-31 10:30 ~ 12:00", c.Text("calendar", cst))
	require.Equal(t, "[Shared Event] 平台组周会 · 2026-08-31 10:30 ~ 12:00", c.Text("share_calendar_event", cst))
	require.Equal(t, "[Event] 平台组周会 · 2026-08-31 10:30 ~ 12:00", c.Text("general_calendar", cst))
}

func TestCalendarText_IsTheBareLabelWithNothingToSay(t *testing.T) {
	require.Equal(t, "[Shared Event]", Calendar{}.Text("share_calendar_event", cst))
	require.Equal(t, "[Event]", CalendarText("calendar", "not json"))
}

func TestCalendarText_LeavesTheShareTokenOut(t *testing.T) {
	// share_token is the only way into an event, so it must not reach
	// content, the search index or the screen.
	const raw = `{"summary":"项目对齐","start_time":"1788143400000","end_time":"1788145200000",` +
		`"open_calendar_id":"cal_peer","open_event_id":"evt_b_0","share_token":"cse_secret"}`
	assert.NotContains(t, CalendarText("share_calendar_event", raw), "cse_secret")
	c, ok := ParseCalendar(raw)
	require.True(t, ok)
	assert.NotContains(t, c.Text("share_calendar_event", cst), "cse_secret")
}

func TestLocalCalendar_CoversTheThreeTypesRenderedInProcess(t *testing.T) {
	for _, mt := range []string{"calendar", "share_calendar_event", "general_calendar"} {
		require.True(t, LocalCalendar(mt), mt)
	}
	for _, mt := range []string{"text", "post", "video_chat", "system", ""} {
		require.False(t, LocalCalendar(mt), mt)
	}
}
