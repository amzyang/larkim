package larkmd

import (
	"regexp"
	"strings"
)

// listItem is a line that opens a list item, indented or not.
var listItem = regexp.MustCompile(`^\s*(?:[-*+]|\d{1,9}[.)])\s`)

// CardForm reports whether a draft goes as an interactive card rather than a
// post, and the markdown that card carries. A post keeps a list item in an md
// element, which reads no emoji name and moves an emotion beside it onto a
// line of its own; card markdown draws the list and reads :KEY: inside it. So
// a draft with an emoji name in a list item goes as a card, every name in it
// spelled as its key. A picture or a mention has no card spelling larkim
// writes, so a draft with one stays a post.
func CardForm(md string) (string, bool) {
	if strings.Contains(md, "![") || strings.Contains(md, "<at") {
		return "", false
	}
	lines := strings.Split(md, "\n")
	fence, listed := "", false
	for i, line := range lines {
		switch {
		case fence != "":
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
		case mdFenceLine.MatchString(line):
			fence = mdFenceLine.FindStringSubmatch(line)[1]
		default:
			spelled, found := keyNames(line)
			listed = listed || found && listItem.MatchString(line)
			lines[i] = spelled
		}
	}
	if !listed {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

// keyNames spells each emoji name on a line, outside code spans, as :KEY:,
// and reports whether there was one. A link's label is left alone, and so is
// a name Feishu offers no emoji for.
func keyNames(line string) (string, bool) {
	parts := strings.Split(line, "`")
	found := false
	for i := 0; i < len(parts); i += 2 {
		p := parts[i]
		var b strings.Builder
		last := 0
		for _, m := range emojiName.FindAllStringSubmatchIndex(p, -1) {
			if m[1] < len(p) && p[m[1]] == '(' {
				continue
			}
			e, ok := emotionFor(p[m[2]:m[3]])
			if !ok {
				continue
			}
			b.WriteString(p[last:m[0]])
			b.WriteString(":" + e.Key + ":")
			last, found = m[1], true
		}
		b.WriteString(p[last:])
		parts[i] = b.String()
	}
	return strings.Join(parts, "`"), found
}
