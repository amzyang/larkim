// Package keyhint renders shortcut strings for display: authoring form in,
// macOS keyboard symbols out, optional dim description beside each key.
package keyhint

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Binding is one shortcut for display. Keys stays in authoring form; MacKeys
// runs at render time only.
type Binding struct {
	Keys string
	Desc string
}

// Styles paints keys and descriptions. HintBar separators use Desc.
type Styles struct {
	Key  lipgloss.Style
	Desc lipgloss.Style
}

// MacKeys converts authoring shortcuts to macOS keyboard symbols without
// changing vim tokens, mouse prose, or command names.
func MacKeys(s string) string {
	if s == "" {
		return ""
	}
	for _, alt := range []string{" or ", " / "} {
		if strings.Contains(s, alt) {
			parts := strings.Split(s, alt)
			for i, p := range parts {
				parts[i] = MacKeys(p)
			}
			return strings.Join(parts, alt)
		}
	}
	if strings.Contains(s, "/") {
		parts := strings.Split(s, "/")
		for i, p := range parts {
			parts[i] = macKeyToken(strings.TrimSpace(p))
		}
		return strings.Join(parts, "/")
	}
	return macKeyToken(strings.TrimSpace(s))
}

// RenderKey returns MacKeys(s) styled with st.Key.
func RenderKey(s string, st Styles) string {
	return st.Key.Render(MacKeys(s))
}

// RenderDesc returns keys alone or "keys desc" with st.Desc on the description.
func RenderDesc(b Binding, st Styles) string {
	out := RenderKey(b.Keys, st)
	if b.Desc != "" {
		out += " " + st.Desc.Render(b.Desc)
	}
	return out
}

// HintBar joins bindings with a dim middle dot, truncating to width w.
func HintBar(bindings []Binding, w int, st Styles) string {
	if len(bindings) == 0 || w <= 0 {
		return ""
	}
	sep := st.Desc.Render(" · ")
	var parts []string
	for _, b := range bindings {
		parts = append(parts, RenderDesc(b, st))
	}
	s := strings.Join(parts, sep)
	if lipgloss.Width(s) <= w {
		return s
	}
	return truncate(s, w)
}

// Pack greedily fits rendered bindings into availWidth separated by gap. When
// the last binding's Keys is "?", it is pinned at the end (status bar help).
func Pack(bindings []Binding, availWidth int, gap string, st Styles) string {
	if availWidth <= 0 || len(bindings) == 0 {
		return ""
	}
	gapW := lipgloss.Width(gap)
	rendered := make([]string, len(bindings))
	for i, b := range bindings {
		rendered[i] = RenderDesc(b, st)
	}

	if len(bindings) > 0 && bindings[len(bindings)-1].Keys == "?" {
		help := rendered[len(rendered)-1]
		pool := rendered[:len(rendered)-1]
		if availWidth < lipgloss.Width(help) {
			return ""
		}
		chosen := []string{help}
		rem := availWidth - lipgloss.Width(help)
		var front []string
		for _, item := range pool {
			cost := lipgloss.Width(item) + gapW
			if rem >= cost {
				front = append(front, item)
				rem -= cost
			}
		}
		return strings.Join(append(front, chosen...), gap)
	}

	var chosen []string
	rem := availWidth
	for _, item := range rendered {
		cost := lipgloss.Width(item)
		if len(chosen) > 0 {
			cost += gapW
		}
		if rem >= cost {
			chosen = append(chosen, item)
			rem -= cost
		}
	}
	return strings.Join(chosen, gap)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "")
}

func macKeyToken(tok string) string {
	if tok == "" {
		return ""
	}
	low := strings.ToLower(tok)
	switch low {
	case "enter", "return":
		return "↩"
	case "esc", "escape":
		return "⎋"
	case "tab":
		return "⇥"
	case "backspace":
		return "⌫"
	case "delete":
		return "⌦"
	case "pgup", "page up", "pageup":
		return "⇞"
	case "pgdown", "page down", "pagedown":
		return "⇟"
	case "space":
		return "Space"
	}
	if strings.HasPrefix(tok, "^") && len([]rune(tok)) == 2 {
		r := []rune(tok)[1]
		if unicode.IsLetter(r) {
			return "⌃" + strings.ToUpper(string(r))
		}
	}
	if strings.Contains(tok, "+") {
		parts := strings.Split(tok, "+")
		var mods string
		key := parts[len(parts)-1]
		for _, p := range parts[:len(parts)-1] {
			mods += macModifier(p)
		}
		return mods + macKeyLetter(key)
	}
	if len(tok) == 1 && unicode.IsLetter(rune(tok[0])) {
		return tok
	}
	return tok
}

func macModifier(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ctrl", "control":
		return "⌃"
	case "cmd", "command", "super":
		return "⌘"
	case "shift":
		return "⇧"
	case "alt", "option", "opt":
		return "⌥"
	default:
		return macKeyToken(name)
	}
}

func macKeyLetter(key string) string {
	key = strings.TrimSpace(key)
	switch strings.ToLower(key) {
	case "enter", "return":
		return "↩"
	case "esc", "escape":
		return "⎋"
	case "tab":
		return "⇥"
	case "backspace":
		return "⌫"
	case "delete":
		return "⌦"
	}
	if len(key) == 1 && unicode.IsLetter(rune(key[0])) {
		return strings.ToUpper(key)
	}
	return macKeyToken(key)
}
