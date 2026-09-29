package tui

import (
	"cmp"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// calendarGlyph opens an event's card, the way the client marks one.
const calendarGlyph = "📅"

// calendarOf reads the event a message carries, and false for anything else —
// including a body this renderer cannot make a card out of, which falls back
// to the text larkim rendered into content.
func calendarOf(x store.Message) (sync.Calendar, bool) {
	if !sync.LocalCalendar(x.MsgType) {
		return sync.Calendar{}, false
	}
	return sync.ParseCalendar(x.ContentRaw)
}

// calendarRows card an event the way the client draws one: what it is called,
// when it runs, and — on an invite — the button that opens it in the client.
//
// A shared event gets no button. It lives on someone else's calendar, which
// the detail page cannot show to a reader who has not subscribed to it; the
// one credential that would open it is share_token, and that has no applink.
func calendarRows(c sync.Calendar, x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	lines := cardLines{g: g, idx: idx, label: "the event", note: "opening the event"}
	for _, l := range wrap(calendarGlyph+" "+flatten(cmp.Or(c.Summary, "Event")), st.inner()) {
		lines.add(stBold.Render(l), "")
	}
	// The span already spells the date out, so nothing relative is drawn
	// beside it: a weekday or a "Today" would say the same thing twice.
	if span := c.Span(time.Local); span != "" {
		lines.add(stDim.Render(span), "")
	}
	if x.MsgType == "calendar" {
		if url := applink.EventLink(c.CalendarID, c.EventID, c.StartMs); url != "" {
			lines.add(stBtn.Render("Open"), url)
		}
	}
	return lines.rows
}

// cardLines builds a card whose lines may each lead somewhere: the event a
// calendar card opens, the meeting a call card joins. label and note name that
// target in the chooser and on the status line.
type cardLines struct {
	g           *leads
	idx         int
	label, note string
	rows        []msgRow
}

// add appends one line. With a url, the whole of what the line draws, past the
// lead, is its click target.
func (c *cardLines) add(s, url string) {
	row := msgRow{lead: c.g.take(), text: s, idx: c.idx}
	if url != "" {
		x0 := row.lead.cols()
		row.zones = []clickZone{{x0: x0, x1: x0 + lipgloss.Width(s),
			urls: []string{url}, label: c.label, note: c.note}}
		// Measured before the link goes on: the escapes it adds draw
		// nothing, and the zone is in columns.
		row.text = hyperlink(url, s)
	}
	c.rows = append(c.rows, row)
}
