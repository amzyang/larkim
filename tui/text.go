package tui

import (
	"regexp"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// inlineMD matches the markup lark-cli renders a message body into: links,
// bold and italic runs, strikethrough, inline code, and the underline
// Feishu's rich text carries over. Italics require a non-space immediately
// inside the asterisks so ordinary prose ("3 * 4 * 5") is left alone, and the
// wider asterisk runs come first so a tripled one is not read as an italic
// wrapping an asterisk, nor a doubled one as an empty italic.
var inlineMD = regexp.MustCompile(`\[([^\]\n]*)\]\(([^)\s]*)\)` +
	`|\*\*\*([^*\n]+)\*\*\*` +
	`|\*\*([^*\n]+)\*\*` +
	`|<u>([^<]*)</u>` +
	`|~~([^~\n]+)~~` +
	"|`([^`\n]+)`" +
	`|\*(\S[^*\n]*)\*`)

var (
	stLink  = lipgloss.NewStyle().Foreground(colAccent).Underline(true)
	stUnder = lipgloss.NewStyle().Underline(true)
	stCode  = lipgloss.NewStyle().Foreground(colAccent)
)

// hitPositions reports the runes of s that a search term stands on, in the
// form markName takes. Terms are matched the way the store matched them to
// find this message at all — every whitespace-separated word, case-insensitive
// substring — so what is marked is what was searched for. Overlapping terms
// coalesce, marking once.
func hitPositions(s string, terms []string) []int {
	if len(terms) == 0 {
		return nil
	}
	// Positions are rune indices, so the lowered copy is walked as runes too;
	// a byte offset would land mid-character on any Chinese word.
	lower := []rune(strings.ToLower(s))
	var pos []int
	for _, t := range terms {
		term := []rune(strings.ToLower(t))
		if len(term) == 0 {
			continue
		}
		for i := 0; i+len(term) <= len(lower); i++ {
			if slices.Equal(lower[i:i+len(term)], term) {
				for j := range len(term) {
					pos = append(pos, i+j)
				}
			}
		}
	}
	return pos
}

// personName is how a colleague is named on screen: the account suffix runs
// on from the name ("李明" + "01"), the way the tenant spells the account
// itself, so the chat list and the message list name the same person alike.
func personName(name, suffix string) string { return name + suffix }

// renderInline styles one line of message text against the message's own
// mentions. A link keeps only its label, which is what the Feishu client
// shows; the target stays in the raw content that `y` copies and `o` opens.
//
// An emphasis run is drawn by reading what it wraps in the style it adds,
// rather than by rendering its content as it stands: a rich-text element
// carries every style it was given at once and the markup spelling it back
// nests, so a layer that stopped at the markup below it would draw that
// markup instead of the words.
func renderInline(s string, ms mentions) string {
	var b strings.Builder
	last := 0
	// in draws what one emphasis run wraps, with the style it adds standing
	// over whatever the runs around it already gave the words.
	in := func(inner string, add func(lipgloss.Style) lipgloss.Style) string {
		return renderInline(inner, ms.styled(add(ms.base)))
	}
	for _, m := range inlineMD.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(ms.render(s[last:m[0]]))
		switch {
		case m[2] >= 0:
			label := s[m[2]:m[3]]
			if strings.TrimSpace(label) == "" {
				label = s[m[4]:m[5]]
			}
			b.WriteString(stLink.Render(expandEmoji(label, ms.spell)))
		case m[6] >= 0:
			b.WriteString(in(s[m[6]:m[7]], func(st lipgloss.Style) lipgloss.Style {
				return st.Bold(true).Italic(true)
			}))
		case m[8] >= 0:
			b.WriteString(in(s[m[8]:m[9]], func(st lipgloss.Style) lipgloss.Style { return st.Bold(true) }))
		case m[10] >= 0:
			b.WriteString(in(s[m[10]:m[11]], func(st lipgloss.Style) lipgloss.Style { return st.Underline(true) }))
		case m[12] >= 0:
			b.WriteString(in(s[m[12]:m[13]], func(st lipgloss.Style) lipgloss.Style { return st.Strikethrough(true) }))
		case m[14] >= 0:
			// Code is the one run that is literal by definition: whatever
			// asterisks are inside it are the code's own.
			b.WriteString(stCode.Render(s[m[14]:m[15]]))
		case m[16] >= 0:
			b.WriteString(in(s[m[16]:m[17]], func(st lipgloss.Style) lipgloss.Style { return st.Italic(true) }))
		}
		last = m[1]
	}
	b.WriteString(ms.render(s[last:]))
	return b.String()
}

// wrap breaks styled text to w columns, returning at least one line, each
// padded to w. It breaks where takeText does, so a body reads the same
// whichever path draws it.
func wrap(s string, w int) []string {
	w = max(4, w)
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")
	var lines []string
	for src := range strings.SplitSeq(s, "\n") {
		first := true
		for {
			if !first {
				src = dropLeadingSpaces(src)
			}
			head, rest := takeText(src, w)
			if head == "" && src != "" {
				// Nowhere to break inside the row: a URL longer than the row.
				head = closeStyle(cut(src, w))
				rest = cutLeft(src, ansi.StringWidth(head))
			}
			lines = append(lines, head+strings.Repeat(" ", max(0, w-ansi.StringWidth(head))))
			if first = false; ansi.StringWidth(rest) == 0 {
				break
			}
			src = rest
		}
	}
	return lines
}
