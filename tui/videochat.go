package tui

import (
	"cmp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// callGlyph opens a call's card, the way the client marks one.
const callGlyph = "📹"

// videoChatOf reads the call a message invites to, and false for anything
// else — including a body this renderer cannot make a card out of, which
// falls back to the text lark-cli rendered.
func videoChatOf(x store.Message) (sync.VideoChat, bool) {
	if x.MsgType != "video_chat" {
		return sync.VideoChat{}, false
	}
	return sync.ParseVideoChat(x.ContentRaw)
}

// videoChatRows card a call the way the client draws one: the topic, with
// how long it ran (or that it is still running) at the right, the meeting
// number, and, while it runs, the button that joins it.
func videoChatRows(v sync.VideoChat, idx int, st msgStyle, g *leads) []msgRow {
	inner := st.inner()
	lines := cardLines{g: g, idx: idx, label: "the meeting", note: "joining the meeting"}
	mark := stAccent.Render("live")
	if ms, ended := v.Span(); ended {
		mark = stDim.Render(sync.CallLength(ms))
	}
	title := callGlyph + " " + flatten(cmp.Or(v.Topic, "Video meeting"))
	// The mark rides the first line, so a topic too long for one line wraps
	// under it rather than being cut short.
	for i, l := range wrap(title, inner-lipgloss.Width(mark)-1) {
		if i == 0 {
			l = padBetween(stBold.Render(l), mark, inner)
		} else {
			l = stBold.Render(l)
		}
		lines.add(l, "")
	}
	if v.MeetNumber != "" {
		lines.add(stDim.Render("Meeting ID: "+spacedMeetNumber(v.MeetNumber)), "")
	}
	if v.Live() && v.MeetNumber != "" {
		lines.add(stBtn.Render("Join"), applink.MeetingLink(v.MeetNumber))
	}
	return lines.rows
}

// spacedMeetNumber groups a meeting number in threes, which is how the
// client, the calendar entry and the invite mail all spell it.
func spacedMeetNumber(n string) string {
	var groups []string
	for len(n) > 3 {
		groups = append([]string{n[len(n)-3:]}, groups...)
		n = n[:len(n)-3]
	}
	return strings.Join(append([]string{n}, groups...), " ")
}
