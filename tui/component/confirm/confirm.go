// Package confirm formats a one-line y/n prompt for the status bar and config
// detail rows: a warn body, an underlined affirmative key, and a dim negative.
package confirm

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Suffix is the plain-text ending every armed confirmation notice carries.
const Suffix = "? y/n"

// Styles paints the prompt. Affirm is the key that commits (y); Deny is the
// key that backs out or chooses the softer path (n). Punct styles ? and /.
type Styles struct {
	Body   lipgloss.Style
	Affirm lipgloss.Style
	Deny   lipgloss.Style
	Punct  lipgloss.Style
}

// Keys renders the ? y/n tail with each key in its role.
func (s Styles) Keys() string {
	return s.Punct.Render("? ") +
		s.Affirm.Render("y") +
		s.Punct.Render("/") +
		s.Deny.Render("n")
}

// Format fits text into maxW columns. When text ends with Suffix, the suffix
// is always kept visible and the body is truncated before it.
func Format(text string, maxW int, st Styles) string {
	if maxW <= 0 {
		return ""
	}
	if !strings.HasSuffix(text, Suffix) {
		return st.Body.Render(truncatePlain(text, maxW))
	}
	body := strings.TrimSuffix(text, Suffix)
	suffix := st.Keys()
	suffixW := lipgloss.Width(suffix)
	bodyW := max(0, maxW-suffixW)
	return st.Body.Render(truncatePlain(body, bodyW)) + suffix
}

func truncatePlain(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, n-1, "") + "…"
}
