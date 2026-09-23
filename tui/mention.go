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

// mentionKind is how loudly one @ run is drawn. Only a mention that reaches
// the reader earns a colour: their own takes the filled badge the client
// paints, @All the accent below it, and everyone else's reads as body text.
type mentionKind int

const (
	mentionPlain mentionKind = iota
	mentionMe
	mentionAll
)

// mentionRun is one @ run: key is how it stands in the rendered text, text is
// what is drawn in its place.
type mentionRun struct {
	key, text string
	kind      mentionKind
}

// mentions styles the @ runs of one message. Runs are carried for people the
// reader is not, so that a longer name is matched before a shorter one it
// contains — "@张三丰" is one mention, not "@张三" with a 丰 after it.
type mentions struct {
	runs []mentionRun
	self string
	base lipgloss.Style
}

// mentionsIn reads the `[{id,name}]` a message's rendering carries. The text
// around the runs is drawn plain; on() dims it for a line that is dim as a
// whole.
func mentionsIn(mentionsJSON, self string) mentions {
	m := mentions{
		runs: []mentionRun{{key: allKey, text: allName, kind: mentionAll}},
		self: self,
		base: lipgloss.NewStyle(),
	}
	var items []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(mentionsJSON), &items) != nil {
		return m
	}
	for _, it := range items {
		if it.Name == "" {
			continue
		}
		run := mentionRun{key: "@" + it.Name, text: "@" + it.Name}
		if self != "" && it.ID == self {
			run.kind = mentionMe
		}
		m.runs = append(m.runs, run)
	}
	slices.SortStableFunc(m.runs, func(a, b mentionRun) int { return len(b.key) - len(a.key) })
	return m
}

// on draws the text around the mentions in base.
func (m mentions) on(base lipgloss.Style) mentions {
	m.base = base
	return m
}

func (m mentions) style(k mentionKind) lipgloss.Style {
	switch k {
	case mentionMe:
		return stMentionMe
	case mentionAll:
		return stAccent
	}
	return m.base
}

// render draws a stretch of message text that carries no markup of its own:
// the @ runs in their own colour, everything else in the base style with its
// emoji expanded.
func (m mentions) render(s string) string {
	var b strings.Builder
	plain := func(chunk string) {
		if chunk != "" {
			b.WriteString(m.base.Render(expandEmoji(chunk)))
		}
	}
	last := 0
	for i := 0; i < len(s); {
		run, n, ok := m.at(s[i:])
		if !ok {
			i++
			continue
		}
		plain(s[last:i])
		b.WriteString(m.style(run.kind).Render(run.text))
		i += n
		last = i
		// The client sets a mention off as a chip; here it is the space the
		// author wrote, so a body that runs straight on needs one lent to it.
		if glues(s[i:]) {
			b.WriteString(m.base.Render(" "))
		}
	}
	plain(s[last:])
	return b.String()
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
	if name == "" {
		name = id
	}
	run := mentionRun{text: "@" + name}
	if m.self != "" && id == m.self {
		run.kind = mentionMe
	}
	return run
}
