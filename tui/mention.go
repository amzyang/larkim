package tui

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

const (
	// allKey is the token an @-everyone leaves in a rendered body: it carries
	// no mentions entry, so nothing else resolves it to a name.
	allKey = "@_all"
	// allName is what the client spells that token as.
	allName = "@All"
)

// atTag matches the `<at user_id="…">name</at>` run a rendered post carries. A
// text message's mentions arrive already spelled as "@name", a post's do not.
var atTag = regexp.MustCompile(`^<at user_id="([^"]*)"[^>]*>([^<]*)</at>`)

// mentionKind is how loudly one @ run is drawn, which follows who it reaches:
// the reader's own takes the filled badge the client paints, a name the room
// can answer takes the accent, and one it cannot is drawn away.
type mentionKind int

const (
	mentionPlain mentionKind = iota
	mentionMe
	mentionAll
	mentionAway
)

// mentionRun is one @ run: key is how it stands in the rendered text, text is
// what is drawn in its place, and id is who it names — @All names nobody.
type mentionRun struct {
	id, key, text string
	// mkey is the key the message spells this mention by — @_user_1 and the
	// like — which is how a card body points at it.
	mkey string
	kind mentionKind
}

// mentions styles the @ runs of one message. Runs are carried for people the
// reader is not, so that a longer name is matched before a shorter one it
// contains — "@张三丰" is one mention, not "@张三" with a 丰 after it.
type mentions struct {
	runs []mentionRun
	self string
	// peer is the person across from the reader, set only in a chat of two.
	peer string
	base lipgloss.Style
	// other is how a mention of somebody else is drawn. A message body gives it
	// the accent, the @ runs being the only colour the body carries; a chat row
	// hands it the row's own dim instead, on() below.
	other lipgloss.Style
}

// mentionsIn reads the `[{id,name}]` a message's rendering carries. The text
// around the runs is drawn plain; on() dims it for a line that is dim as a
// whole.
func mentionsIn(mentionsJSON, self string) mentions {
	m := mentions{
		runs:  []mentionRun{{key: allKey, text: allName, kind: mentionAll}},
		self:  self,
		base:  lipgloss.NewStyle(),
		other: stAccent,
	}
	var items []struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(mentionsJSON), &items) != nil {
		return m
	}
	for _, it := range items {
		if it.Name == "" {
			continue
		}
		m.runs = append(m.runs, mentionRun{id: it.ID, key: "@" + it.Name, mkey: it.Key,
			text: "@" + it.Name, kind: m.kindOf(it.ID)})
	}
	slices.SortStableFunc(m.runs, func(a, b mentionRun) int { return len(b.key) - len(a.key) })
	return m
}

// on draws the text around the mentions in base, and any @ that is not the
// reader's own along with it: the line is one colour, so only a mention that
// reaches them is worth breaking out of it.
func (m mentions) on(base lipgloss.Style) mentions {
	m.base, m.other = base, base
	return m
}

// facing narrows the mentions to a chat of two. Nobody but the reader and the
// person across from them is in the room, so a third name is a link to their
// card rather than a call for attention, and is drawn away.
func (m mentions) facing(peer string) mentions {
	if peer == "" {
		return m
	}
	m.peer = peer
	m.runs = slices.Clone(m.runs)
	for i, r := range m.runs {
		if r.id != "" {
			m.runs[i].kind = m.kindOf(r.id)
		}
	}
	return m
}

// kindOf places one mention by who it reaches.
func (m mentions) kindOf(id string) mentionKind {
	switch {
	case m.self != "" && id == m.self:
		return mentionMe
	case m.peer != "" && id != m.peer:
		return mentionAway
	}
	return mentionPlain
}

// draw paints one @ run. The reader's own is a filled badge closed by the caps
// a reaction chip wears, so the two read as one shape; the rest are colour on
// the line they sit in.
func (m mentions) draw(r mentionRun) string {
	switch r.kind {
	case mentionMe:
		return stMentionMeEdge.Render(chipLeft) + stMentionMe.Render(r.text) + stMentionMeEdge.Render(chipRight)
	case mentionAll:
		return stAccent.Render(r.text)
	case mentionAway:
		return stDim.Render(r.text)
	}
	return m.other.Render(r.text)
}

// walk splits s at the @ runs it holds, handing each stretch of ordinary text
// to plain and each mention to run, in the order they were written.
func (m mentions) walk(s string, plain func(string), run func(mentionRun)) {
	last := 0
	for i := 0; i < len(s); {
		r, n, ok := m.at(s[i:])
		if !ok {
			i++
			continue
		}
		plain(s[last:i])
		run(r)
		i += n
		last = i
		// The client sets a mention off as a chip; here it is the space the
		// author wrote, so a body that runs straight on needs one lent to it.
		if glues(s[i:]) {
			plain(" ")
		}
	}
	plain(s[last:])
}

// render draws a stretch of message text that carries no markup of its own:
// the @ runs in their own colour, everything else in the base style with its
// emoji expanded.
func (m mentions) render(s string) string {
	var b strings.Builder
	m.walk(s, func(chunk string) {
		if chunk != "" {
			b.WriteString(m.plain(chunk))
		}
	}, func(r mentionRun) { b.WriteString(m.draw(r)) })
	return b.String()
}

// plain styles a stretch that holds no @ of its own.
func (m mentions) plain(s string) string { return m.base.Render(expandEmoji(s)) }

// segs is render in pieces, so that an official emoji no character carries can
// stand in the line as the picture the client draws. It answers nil when the
// text spells none, which leaves the line on the string path render gives it.
func (m mentions) segs(s string, pic func(key string) picture) []rowSeg {
	var out []rowSeg
	drawn := false
	m.walk(s, func(chunk string) {
		if chunk == "" {
			return
		}
		if segs := emojiSegs(chunk, pic, m.plain); segs != nil {
			out, drawn = append(out, segs...), true
			return
		}
		out = append(out, rowSeg{text: m.plain(chunk)})
	}, func(r mentionRun) { out = append(out, rowSeg{text: m.draw(r)}) })
	if !drawn {
		return nil
	}
	return out
}

// glues reports whether what follows a mention would read as part of it.
// Punctuation and whitespace already separate; a word does not.
func glues(rest string) bool {
	r, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// at returns the run standing at the head of s and the bytes it spans: a post's
// `<at>` tag, or a name token, longest key first.
func (m mentions) at(s string) (mentionRun, int, bool) {
	if strings.HasPrefix(s, "<at ") {
		g := atTag.FindStringSubmatch(s)
		if g == nil {
			return mentionRun{}, 0, false
		}
		return m.tagRun(g[1], g[2]), len(g[0]), true
	}
	if len(s) == 0 || s[0] != '@' {
		return mentionRun{}, 0, false
	}
	for _, r := range m.runs {
		if strings.HasPrefix(s, r.key) {
			return r, len(r.key), true
		}
	}
	return mentionRun{}, 0, false
}

// tagRun resolves one `<at>` tag: an @-everyone by its id, anyone else by the
// name the tag carries, falling back to the id when it carries none.
func (m mentions) tagRun(id, name string) mentionRun {
	if id == "all" || id == allKey {
		return mentionRun{text: allName, kind: mentionAll}
	}
	// A card names a mention by the key its attachment table pairs the
	// sending app's id with, which is what ties it to the open id this reader
	// knows the person by.
	for _, r := range m.runs {
		if r.mkey != "" && r.mkey == id {
			return r
		}
	}
	if name == "" {
		name = id
	}
	return mentionRun{id: id, text: "@" + name, kind: m.kindOf(id)}
}
