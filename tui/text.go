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

// personName is how a colleague is named on screen: the account suffix runs
// on from the name ("李明" + "01"), the way the tenant spells the account
// itself, so the chat list and the message list name the same person alike.
func personName(name, suffix string) string { return name + suffix }

// renderInline styles one line of message text against the message's own
// mentions. A link keeps only its label, which is what the Feishu client
// shows; the target stays in the raw content that `y` copies and `o` opens.
func renderInline(s string, ms mentions) string {
	var b strings.Builder
	last := 0
	for _, m := range inlineMD.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(ms.render(s[last:m[0]]))
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
	b.WriteString(ms.render(s[last:]))
	return b.String()
}

// wrap breaks styled text to w columns, returning at least one line.
func wrap(s string, w int) []string {
	return strings.Split(lipgloss.NewStyle().Width(max(4, w)).Render(s), "\n")
}
