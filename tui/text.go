package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// inlineMD matches the markup lark-cli renders a message body into: links,
// bold runs and the underline Feishu's rich text carries over.
var inlineMD = regexp.MustCompile(`\[([^\]\n]*)\]\(([^)\s]*)\)|\*\*([^*\n]+)\*\*|<u>([^<]*)</u>`)

var (
	stLink  = lipgloss.NewStyle().Foreground(colAccent).Underline(true)
	stUnder = lipgloss.NewStyle().Underline(true)
)

// renderInline styles one line of message text. A link keeps only its label,
// which is what the Feishu client shows; the target stays in the raw content
// that `y` copies and `o` opens.
func renderInline(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range inlineMD.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(expandEmoji(s[last:m[0]]))
		switch {
		case m[2] >= 0:
			label := s[m[2]:m[3]]
			if strings.TrimSpace(label) == "" {
				label = s[m[4]:m[5]]
			}
			b.WriteString(stLink.Render(expandEmoji(label)))
		case m[6] >= 0:
			b.WriteString(stBold.Render(expandEmoji(s[m[6]:m[7]])))
		case m[8] >= 0:
			b.WriteString(stUnder.Render(expandEmoji(s[m[8]:m[9]])))
		}
		last = m[1]
	}
	b.WriteString(expandEmoji(s[last:]))
	return b.String()
}

// wrap breaks styled text to w columns, returning at least one line.
func wrap(s string, w int) []string {
	return strings.Split(lipgloss.NewStyle().Width(max(4, w)).Render(s), "\n")
}
