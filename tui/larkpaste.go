package tui

import (
	"bytes"
	"cmp"
	"os"
	"slices"
	"strings"

	"github.com/amzyang/larkim/sync"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// larkPaste converts Lark-client clipboard HTML into the composer's markdown.
// It returns the markdown and true when the HTML is Lark-origin (carries
// data-lark-html-role="root"); for anything else it returns ("", false) and
// the caller falls through to the generic converter.
//
// The Lark desktop client writes rich-text-paragraph divs, rich-text-image
// figures, rich-text-anchor links, and at-mention spans with data attributes
// that name the resources (data-origin-file, data-image-key, data-at-id).
// The generic html-to-markdown converter cannot read those, so images come
// out as useless native-resource:// URLs and mentions as bare text.
func larkPaste(data []byte) (string, bool) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", false
	}
	root := findNode(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && nodeAttr(n, "data-lark-html-role") == "root"
	})
	if root == nil {
		return "", false
	}
	var b strings.Builder
	larkWalk(&b, root, false)
	return strings.TrimSpace(b.String()), true
}

// larkWalk renders one node and its children into the builder. prevPara
// tracks whether a paragraph separator has already been written, so blank
// lines are not doubled.
func larkWalk(b *strings.Builder, n *html.Node, prevPara bool) bool {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		return false
	}
	if n.Type != html.ElementNode {
		return larkChildren(b, n, prevPara)
	}

	cls := strings.Fields(nodeAttr(n, "class"))
	switch {
	case slices.Contains(cls, "rich-text-paragraph"):
		return larkParagraph(b, n, prevPara)

	case slices.Contains(cls, "rich-text-image"):
		return larkImage(b, n, prevPara)

	case slices.Contains(cls, "rich-text-code-block"):
		return larkCodeBlock(b, n, prevPara)

	case slices.Contains(cls, "rich-text-ordered-list"):
		larkListItem(b, n, true)
		return false

	case slices.Contains(cls, "rich-text-unordered-list"):
		larkListItem(b, n, false)
		return false

	case slices.Contains(cls, "rich-text-quote"), n.DataAtom == atom.Blockquote:
		return larkQuote(b, n, prevPara)

	// The client also writes plain HTML blocks, which the rich-text classes
	// above do not cover; each keeps a line of its own, the way those do.
	case n.DataAtom == atom.P:
		return larkParagraph(b, n, prevPara)

	case n.DataAtom == atom.Pre:
		return larkCodeBlock(b, n, prevPara)

	case n.DataAtom == atom.Ol, n.DataAtom == atom.Ul:
		for c := range n.ChildNodes() {
			if c.DataAtom == atom.Li {
				larkListItem(b, c, n.DataAtom == atom.Ol)
			}
		}
		return false

	case slices.Contains(headings, n.DataAtom):
		larkBreak(b, prevPara)
		b.WriteString(strings.Repeat("#", slices.Index(headings, n.DataAtom)+1) + " ")
		larkChildren(b, n, false)
		return false

	case n.DataAtom == atom.A:
		larkAnchor(b, n)
		return false

	case n.DataAtom == atom.B || n.DataAtom == atom.Strong:
		larkInline(b, n, "**")
		return false

	case n.DataAtom == atom.I || n.DataAtom == atom.Em:
		larkInline(b, n, "*")
		return false

	case n.DataAtom == atom.S || n.DataAtom == atom.Del:
		larkInline(b, n, "~~")
		return false

	case n.DataAtom == atom.U:
		larkInline(b, n, "")
		return false

	case n.DataAtom == atom.Code:
		larkCode(b, n)
		return false

	case slices.Contains(cls, "rich-text-at"):
		larkMention(b, n)
		return false

	default:
		// Pass through unknown elements, rendering their children.
		return larkChildren(b, n, prevPara)
	}
}

// headings are h1 to h6, in the order of the markdown level each one is.
var headings = []atom.Atom{atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6}

// larkChildren walks n's children in order, carrying prevPara across them.
func larkChildren(b *strings.Builder, n *html.Node, prevPara bool) bool {
	for c := range n.ChildNodes() {
		prevPara = larkWalk(b, c, prevPara)
	}
	return prevPara
}

// larkBreak starts a block on a line of its own, unless the paragraph before
// already ended the line.
func larkBreak(b *strings.Builder, prevPara bool) {
	if b.Len() > 0 && !prevPara {
		b.WriteByte('\n')
	}
}

// larkParagraph emits the text of one paragraph. A newline separates it from
// the previous block; an empty paragraph adds a second one (a blank line).
func larkParagraph(b *strings.Builder, n *html.Node, prevPara bool) bool {
	larkBreak(b, prevPara)
	start := b.Len()
	for c := range n.ChildNodes() {
		larkWalk(b, c, false)
	}
	// An empty paragraph is a blank line between two others.
	if b.Len() == start {
		b.WriteByte('\n')
		return true
	}
	return false
}

// larkImage emits a markdown image reference for a figure element.
func larkImage(b *strings.Builder, n *html.Node, prevPara bool) bool {
	img := findNode(n, func(n *html.Node) bool { return n.Type == html.ElementNode && n.DataAtom == atom.Img })
	if img == nil {
		return prevPara
	}
	larkBreak(b, prevPara)

	// Prefer data-origin-file (local cached path) if the file exists.
	// Fall back to data-image-key (already on Feishu servers).
	origin := nodeAttr(img, "data-origin-file")
	key := nodeAttr(img, "data-image-key")
	// Strip the _MIDDLE_WEBP / _NOOP_WEBP suffix lark adds to the key in the
	// clipboard; the raw key is what Feishu holds and what compose recognises.
	key = stripImageKeySuffix(key)

	switch {
	case origin != "" && fileExists(origin):
		b.WriteString(imageRef(origin))
	case key != "":
		b.WriteString(imageRef(key))
	default:
		// Nothing useful; skip the image entirely.
		return prevPara
	}
	return false
}

// larkCodeBlock emits a fenced code block.
func larkCodeBlock(b *strings.Builder, n *html.Node, prevPara bool) bool {
	larkBreak(b, prevPara)
	lang := nodeAttr(n, "data-language")
	if code := findNode(n, func(c *html.Node) bool { return c.DataAtom == atom.Code }); code != nil {
		lang = cmp.Or(lang, nodeAttr(code, "data-lark-language"))
	}
	b.WriteString("```")
	b.WriteString(lang)
	b.WriteByte('\n')
	// Collect text from all lines inside the code block.
	larkCodeLines(b, n)
	b.WriteString("\n```")
	return false
}

// larkCodeLines collects code text, respecting <br> as line breaks.
func larkCodeLines(b *strings.Builder, n *html.Node) {
	for c := range n.ChildNodes() {
		switch {
		case c.Type == html.TextNode:
			b.WriteString(c.Data)
		case c.DataAtom == atom.Br:
			b.WriteByte('\n')
		default:
			larkCodeLines(b, c)
		}
	}
}

// larkListItem emits one list item, numbered or bulleted.
func larkListItem(b *strings.Builder, n *html.Node, ordered bool) {
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	if ordered {
		// Lark may put data-list-index; we just use "1." since markdown renumbers.
		b.WriteString("1. ")
	} else {
		b.WriteString("- ")
	}
	for c := range n.ChildNodes() {
		larkWalk(b, c, false)
	}
}

// larkQuote emits a blockquote. The Lark client wraps blockquote content in
// rich-text-paragraph divs; collectText flattens those into inline text so the
// result is a single "> …" line rather than "> \ntext".
func larkQuote(b *strings.Builder, n *html.Node, prevPara bool) bool {
	larkBreak(b, prevPara)
	b.WriteString("> ")
	b.WriteString(strings.TrimSpace(collectText(n)))
	return false
}

// larkAnchor emits a markdown link. When the display text equals the URL
// (the common case for a bare link), only the URL is emitted.
func larkAnchor(b *strings.Builder, n *html.Node) {
	href := nodeAttr(n, "href")
	text := collectText(n)
	if text == "" {
		text = href
	}
	// Bare URL: the display text is (nearly) the URL itself.
	if urlsEquivalent(text, href) {
		b.WriteString(href)
		return
	}
	b.WriteString("[")
	b.WriteString(sync.EscapeMDLinkText(text))
	b.WriteString("](")
	b.WriteString(href)
	b.WriteString(")")
}

// larkInline wraps children in a markdown delimiter (**, *, ~~, etc).
func larkInline(b *strings.Builder, n *html.Node, delim string) {
	b.WriteString(delim)
	for c := range n.ChildNodes() {
		larkWalk(b, c, false)
	}
	b.WriteString(delim)
}

// larkCode wraps text in backtick fences.
func larkCode(b *strings.Builder, n *html.Node) {
	text := collectText(n)
	if strings.Contains(text, "`") {
		b.WriteString("`` ")
		b.WriteString(text)
		b.WriteString(" ``")
	} else {
		b.WriteByte('`')
		b.WriteString(text)
		b.WriteByte('`')
	}
}

// larkMention emits an @mention. The Lark client writes <span class="rich-text-at"
// data-at-id="ou_…">@Name</span>. The composer's own mention syntax is @Name,
// so we emit exactly that and let resolveMentions do the rest on send.
func larkMention(b *strings.Builder, n *html.Node) {
	text := collectText(n)
	// The clipboard text already starts with @; emit as-is if so.
	if strings.HasPrefix(text, "@") {
		b.WriteString(text)
	} else {
		b.WriteByte('@')
		b.WriteString(text)
	}
}

// --- helpers ---

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// findNode is the first node beneath n, in document order, that is accepts.
func findNode(n *html.Node, is func(*html.Node) bool) *html.Node {
	for d := range n.Descendants() {
		if is(d) {
			return d
		}
	}
	return nil
}

func collectText(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
		}
	}
	return b.String()
}

// stripImageKeySuffix removes the quality/format suffix the clipboard adds.
// e.g. "img_v3_xxx_MIDDLE_WEBP" → "img_v3_xxx".
func stripImageKeySuffix(key string) string {
	for _, suffix := range []string{"_MIDDLE_WEBP", "_NOOP_WEBP", "_ORIGIN_WEBP", "_MIDDLE_PNG", "_NOOP_PNG"} {
		if s, ok := strings.CutSuffix(key, suffix); ok {
			return s
		}
	}
	return key
}

// urlsEquivalent reports whether two URLs are the same up to a trailing slash.
func urlsEquivalent(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// fileExists reports whether path names a regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
