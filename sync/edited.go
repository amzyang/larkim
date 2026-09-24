package sync

import (
	"regexp"
	"strings"

	"github.com/amzyang/larkim/larkcli"
)

// paragraphRe matches one <p>…</p> block of a text body's HTML.
var paragraphRe = regexp.MustCompile(`(?s)<p>(.*?)</p>`)

// renderedText is the text to store for a rendered message. Feishu keeps some
// text bodies as rich text — every edit, and whatever a few clients send that
// way — so the renderer hands back one <p> per line where the body is
// otherwise plain.
func renderedText(r larkcli.RenderedMessage) string {
	switch r.MsgType {
	case "text":
		return UnwrapParagraphs(r.Content)
	case "merge_forward":
		return unwrapForwarded(r.Content)
	}
	return r.Content
}

// unwrapForwarded flattens the paragraphs of the text messages inside a
// forwarded bundle, which the renderer lays out one indented line each. A
// message of several paragraphs keeps the indentation on every line it opens.
func unwrapForwarded(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		text := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(text)]
		body := UnwrapParagraphs(text)
		lines[i] = indent + strings.ReplaceAll(body, "\n", "\n"+indent)
	}
	return strings.Join(lines, "\n")
}

// UnwrapParagraphs joins the paragraphs of an HTML text body with newlines. A
// body holding anything outside a paragraph is left alone: that shape is not
// the one Feishu produces, so its text is the sender's own. Only the tags are
// dropped, never entities: Feishu leaves & as itself here, so unescaping would
// eat the query string of a link.
func UnwrapParagraphs(s string) string {
	locs := paragraphRe.FindAllStringSubmatchIndex(s, -1)
	lines := make([]string, 0, len(locs))
	end := 0
	for _, l := range locs {
		if l[0] != end {
			return s
		}
		end = l[1]
		lines = append(lines, s[l[2]:l[3]])
	}
	if end != len(s) {
		return s
	}
	return strings.Join(lines, "\n")
}
