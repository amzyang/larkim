package larkmd

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/yuin/goldmark/v2/ast"
	gast "github.com/yuin/goldmark/v2/extension/ast"
)

// Finding is one thing a markdown body loses on the way to Feishu: something
// the sender wrote that will not arrive as what they meant by it.
type Finding struct {
	Rule    string `json:"rule"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Lint reads a body the way the wire will and says what it loses. Every
// finding is advisory: what it names is content the sender chose, so nothing
// here refuses a send.
//
// Only what larkim can prove is reported. What the Feishu client makes of the
// markdown inside an md element is its own business, and a rule guessing at
// it would cry over bodies that arrive fine.
func Lint(src string) []Finding {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	b := []byte(src)
	lines := newLineTable(src)
	cuts := paragraphCuts(src)
	var out []Finding
	at := lines.at
	_ = ast.Walk(Parser.Parse(b), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := n.(type) {
		case *ast.Text:
			// A bare [Name] reaches this walk as three nodes — "[", the name,
			// "]" — and a link's label reaches it as the name alone, so only
			// the parent tells the bracket a sender typed from the one
			// markdown ate.
			label := c.Parent() != nil &&
				(c.Parent().Kind() == ast.KindLink || c.Parent().Kind() == ast.KindImage)
			// The raw source is scanned rather than the decoded value, so an
			// offset found here is an offset in what the sender wrote.
			for _, idx := range c.Value.Indices() {
				out = append(out, scanText(src, idx.Start, idx.Stop, label, at)...)
			}
		case *ast.List:
			out = append(out, scanList(c, cuts, at)...)
		case *gast.Table:
			out = append(out, scanTable(c, at)...)
		case *ast.Link:
			out = append(out, scanLink(c.Destination.Value(b), c.Pos(), at)...)
		case *ast.RawHTML:
			out = append(out, scanHTML(c.Value.Str(b), c.Pos(), at)...)
		case *ast.HTMLBlock:
			// An HTML block keeps no source segments in goldmark v2, so the
			// tag is read off the body at the position it opens on.
			open := max(c.Pos(), 0)
			out = append(out, scanHTML(src[open:min(open+64, len(src))], c.Pos(), at)...)
		}
		return ast.WalkContinue, nil
	})
	return out
}

// shortcode is how a message spells one of Feishu's emoji.
var shortcode = regexp.MustCompile(`:([A-Za-z0-9_]{1,32}):`)

// emojiFinding reports an emoji a post body cannot carry. Feishu draws an
// emoji in a post from an emotion element, and a body sent as markdown is one
// md element with no room for one, so every spelling arrives as the
// characters it is written with — while an <at> tag on the same line becomes
// a real mention. [Name] is the spelling a plain text message does draw, and
// :KEY: draws nowhere: it is how larkim writes an emotion element back
// (sync/post.go), not a spelling Feishu takes. See
// docs/markdown-lint/DIALECT.md.
//
// Only a spelling that names an emoji Feishu has is reported. :tada: names
// none, and whether the sender meant an emoji or wrote prose between colons
// is not decidable — saying Feishu has no such emoji would only imply that
// having one would have helped.
func emojiFinding(e emoji.Emoji, spelling string, line, col int) Finding {
	f := Finding{
		Rule: "emoji_not_rendered", Line: line, Column: col,
		Message: spelling + " arrives as those characters: a post carries no emoji",
		Hint:    "only a plain text message draws this one, spelled [" + e.Name() + "]",
	}
	if g := cmp.Or(e.Insert, e.Glyph); g != "" {
		f.Hint = "write " + g + " instead"
	}
	return f
}

func scanText(src string, start, stop int, label bool, at func(int) (int, int)) []Finding {
	raw := src[start:stop]
	var out []Finding
	for i := range len(raw) {
		if raw[i] != '@' || !AtBoundary(src, start+i) {
			continue
		}
		name := mentionName(raw[i+1:])
		if name == "" {
			continue
		}
		line, col := at(start + i)
		out = append(out, Finding{
			Rule: "mention_unresolved", Line: line, Column: col,
			Message: "@" + name + " notifies nobody: it is sent as the text it reads as",
			Hint:    `write <at user_id="ou_…">` + name + `</at>, or pick the name from the composer's popup`,
		})
	}
	for _, m := range shortcode.FindAllStringSubmatchIndex(raw, -1) {
		key := raw[m[2]:m[3]]
		// 10:30:45 spells a time with the same colons, and the digits after
		// the first are no more an emoji than the ones before it.
		if start+m[0] > 0 && isAlnum(src[start+m[0]-1]) {
			continue
		}
		if e, ok := emoji.ByKey(key); ok {
			line, col := at(start + m[0])
			out = append(out, emojiFinding(e, ":"+key+":", line, col))
		}
	}
	// [Name] is what the composer's own popup writes for an emoji no
	// character carries: a text message resolves that spelling, a post does
	// not. The brackets sit outside this node, so the source carries them.
	if !label && start > 0 && stop < len(src) && src[start-1] == '[' && src[stop] == ']' {
		if e, ok := emoji.ByName(raw); ok {
			line, col := at(start - 1)
			out = append(out, emojiFinding(e, "["+raw+"]", line, col))
		}
	}
	return out
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// scanList reports a list the wire cuts up. postContent sends one md element
// per paragraph, so a blank line inside a list ends it: what follows the gap
// arrives as a separate element with no list around it.
func scanList(n *ast.List, cuts []int, at func(int) (int, int)) []Finding {
	start, stop := max(n.Pos(), 0), blockEnd(n)
	parts := 1
	for _, cut := range cuts {
		if cut > start && cut < stop {
			parts++
		}
	}
	if parts == 1 {
		return nil
	}
	line, col := at(start)
	return []Finding{{
		Rule: "list_split", Line: line, Column: col,
		Message: fmt.Sprintf("a blank line inside this list sends it as %d separate post elements", parts),
		Hint:    "keep the items on consecutive lines to send the list as one",
	}}
}

// paragraphCuts is where the wire cuts a body: the offset of every blank line
// larkcli.Paragraphs parts the md elements at. The cuts are read back off
// that function rather than worked out again, so a rule about them cannot
// describe a split the wire does not make.
func paragraphCuts(src string) []int {
	chunks := larkcli.Paragraphs(src)
	if len(chunks) == 0 {
		return nil
	}
	// Paragraphs drops the blank lines around a body, so the first chunk is
	// where what it kept begins.
	at := strings.Index(src, chunks[0])
	var cuts []int
	for _, c := range chunks {
		if c == "" {
			cuts = append(cuts, at)
		}
		at += len(c) + 1 // the newline the split consumed
	}
	return cuts
}

// blockEnd is where a block's last byte sits. goldmark hands out a start
// position and the segments its leaves were cut from, so the end is the
// furthest of those.
func blockEnd(n ast.Node) int {
	end := max(n.Pos(), 0)
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if b, ok := c.(ast.BlockNode); ok {
			for _, seg := range b.Source() {
				end = max(end, seg.Stop)
			}
		}
		if t, ok := c.(*ast.Text); ok {
			for _, idx := range t.Value.Indices() {
				end = max(end, idx.Stop)
			}
		}
		return ast.WalkContinue, nil
	})
	return end
}

// scanLink reports a destination Feishu will not make a link of. It keeps
// what it can read as an address — a scheme it knows, or something shaped
// like a host, bare notes.md included — and drops the rest, leaving the label
// behind as plain text. What is listed here is what was watched being
// dropped; see docs/markdown-lint/DIALECT.md.
func scanLink(dest string, pos int, at func(int) (int, int)) []Finding {
	if !linkDropped(dest) {
		return nil
	}
	line, col := at(max(pos, 0))
	return []Finding{{
		Rule: "link_dropped", Line: line, Column: col,
		Message: "the link to " + dest + " is dropped; only its words arrive",
		Hint:    "give it a whole address, https:// or lark:// included",
	}}
}

func linkDropped(dest string) bool {
	if dest == "" {
		return false
	}
	switch dest[0] {
	case '/', '.', '#', '$':
		// A path, a relative path, an anchor, a template: none of them names
		// somewhere Feishu can send a reader.
		return true
	}
	return strings.HasPrefix(strings.ToLower(dest), "mailto:")
}

// htmlTag is the name a tag opens with. A closing tag answers nothing: one
// finding for the pair is enough to say the pair will show.
var htmlTag = regexp.MustCompile(`^<([A-Za-z][A-Za-z0-9-]*)`)

// htmlMarkdown is what to write instead, where markdown can say it at all.
var htmlMarkdown = map[string]string{
	"b": "**bold**", "strong": "**bold**",
	"i": "*italic*", "em": "*italic*",
	"s": "~~strike~~", "strike": "~~strike~~", "del": "~~strike~~",
	"code": "`code`",
	"br":   "a new line",
}

// scanHTML reports a tag that arrives as the characters it is spelled with.
// <at> is the one a post parses; every other tag watched so far — <b>, <i>,
// <u>, <s>, <code>, <span>, <br>, <hr>, and the card's own <font> and
// <text_tag> — reaches the reader as itself.
func scanHTML(raw string, pos int, at func(int) (int, int)) []Finding {
	m := htmlTag.FindStringSubmatch(raw)
	if m == nil || strings.EqualFold(m[1], "at") {
		return nil
	}
	name := strings.ToLower(m[1])
	line, col := at(max(pos, 0))
	f := Finding{
		Rule: "html_literal", Line: line, Column: col,
		Message: "<" + name + "> arrives as those characters: a post reads no tag but <at>",
		Hint:    "say it in markdown, or drop the tag",
	}
	if md, ok := htmlMarkdown[name]; ok {
		f.Hint = "write " + md + " instead"
	}
	return []Finding{f}
}

// scanTable reports an alignment Feishu drops. It draws a real grid from a
// pipe table — seven body rows and inline content in the cells all arrive —
// but every column reads left, whatever the delimiter row asked for, and it
// rewrites `:---:` to `---` on the way in. See docs/markdown-lint/DIALECT.md.
func scanTable(n *gast.Table, at func(int) (int, int)) []Finding {
	aligned := false
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		cell, ok := c.(*gast.TableCell)
		if entering && ok && cell.Alignment != gast.AlignNone {
			aligned = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if !aligned {
		return nil
	}
	line, col := at(max(n.Pos(), 0))
	return []Finding{{
		Rule: "table_alignment_ignored", Line: line, Column: col,
		Message: "this table's column alignment is dropped: every column reads left",
		Hint:    "write the delimiter row as --- and let the columns be",
	}}
}
