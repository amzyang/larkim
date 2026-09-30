package larkmd

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	gast "github.com/yuin/goldmark/v2/extension/ast"
	"github.com/yuin/goldmark/v2/parser"
)

// PostContent is the rich-text body Feishu stores for a markdown draft. A
// paragraph goes as an md element, and a blank line between two as an empty
// text element: Feishu expands an md element into paragraphs of its own and
// drops the blank lines inside it, and an empty paragraph sent as an empty
// array is stripped on the way in, so a line carrying an empty text element is
// the only spelling of a gap that survives the round trip.
//
// A line naming one of Feishu's emoji goes the way the client's own editor
// sends it instead: as text, a, at and emotion elements in one paragraph.
// Markdown has no spelling of an emoji, so an md element carries "[Done]" as
// those characters, and Feishu puts an md element on a line of its own, so an
// emotion cannot sit beside one either.
func PostContent(markdown string) string {
	paras := [][]elem{}
	for _, c := range chunks(markdown) {
		if c.gap {
			paras = append(paras, []elem{{Tag: "text", Text: new("")}})
			continue
		}
		split, _ := splitChunk(c.text)
		paras = append(paras, split...)
	}
	body, _ := json.Marshal(map[string]map[string][][]elem{"zh_cn": {"content": paras}})
	return string(body)
}

// elem is one post element as it goes on the wire. Text is a pointer because a
// text element carries it even when it is empty — that is what a gap is — and
// an at or an emotion element carries none.
type elem struct {
	Tag       string   `json:"tag"`
	Text      *string  `json:"text,omitempty"`
	Style     []string `json:"style,omitempty"`
	Href      string   `json:"href,omitempty"`
	UserID    string   `json:"user_id,omitempty"`
	EmojiType string   `json:"emoji_type,omitempty"`
}

// chunk is one paragraph of a body as the wire parts it, or the blank line
// between two. start is the offset it opens at, which is how a finding about
// the wire is placed back in what the sender wrote.
type chunk struct {
	text  string
	start int
	gap   bool
}

// mdFenceLine opens or closes a fenced code block. The composer has a fence of
// its own to classify drafts by; this one is not shared with it because the
// two answer different questions and neither is worth a package.
var mdFenceLine = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// chunks cuts a body at the blank lines between its blocks. A blank line
// inside a fence is code, so the fence stays whole, and the blank lines around
// the body are dropped: nobody typed a gap there.
func chunks(markdown string) []chunk {
	var out []chunk
	var fence string
	open, end, at := -1, 0, 0
	flush := func() {
		if open >= 0 {
			out = append(out, chunk{text: markdown[open:end], start: open})
			open = -1
		}
	}
	for line := range strings.SplitSeq(markdown, "\n") {
		lineStart := at
		at += len(line) + 1
		switch {
		case fence != "":
			// A closing fence is at least as long as the one that opened the
			// block, which is what the prefix test comes to.
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
		case mdFenceLine.MatchString(line):
			fence = mdFenceLine.FindStringSubmatch(line)[1]
		case strings.TrimSpace(line) == "":
			flush()
			out = append(out, chunk{start: lineStart, gap: true})
			continue
		}
		if open < 0 {
			open = lineStart
		}
		end = lineStart + len(line)
	}
	flush()
	first, last := 0, len(out)
	for first < last && out[first].gap {
		first++
	}
	for last > first && out[last-1].gap {
		last--
	}
	return out[first:last]
}

// splitChunk is the paragraphs one chunk goes as. A line of words naming an
// emoji becomes a paragraph of its own elements, and the lines around it stay
// together as the md element they were. native is which of the chunk's lines
// went as elements, counted from zero.
func splitChunk(text string) (paras [][]elem, native []int) {
	whole := [][]elem{{{Tag: "md", Text: new(text)}}}
	if !strings.Contains(text, "[") {
		return whole, nil
	}
	lines := strings.Split(text, "\n")
	prose := proseLines(text, len(lines))
	var held []string
	flush := func() {
		if len(held) > 0 {
			paras = append(paras, []elem{{Tag: "md", Text: new(strings.Join(held, "\n"))}})
			held = nil
		}
	}
	for i, line := range lines {
		if prose[i] {
			if els, ok := lineElems(strings.TrimSpace(line)); ok {
				flush()
				paras = append(paras, els)
				native = append(native, i)
				continue
			}
		}
		held = append(held, line)
	}
	if native == nil {
		return whole, nil
	}
	flush()
	return paras, native
}

// proseLines marks the lines of a chunk that are a paragraph's words from the
// first column: no list or quote marker in front of them, and no heading,
// table or code around them. Only such a line can leave its block without
// taking markup with it. A lazy line after a quote is one, which is how
// "> abc" followed by "[Done]" sends the emoji rather than the characters.
func proseLines(text string, n int) []bool {
	lines := newLineTable(text)
	out := make([]bool, n)
	_ = ast.Walk(Parser.Parse([]byte(text)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		p, ok := node.(*ast.Paragraph)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		for _, seg := range p.Source() {
			line, col := lines.at(seg.Start)
			if strings.TrimSpace(text[seg.Start-col+1:seg.Start]) == "" {
				out[line-1] = true
			}
		}
		return ast.WalkSkipChildren, nil
	})
	return out
}

// lineParser reads one line the way Feishu expands an md element: the
// strikethrough and the written-out addresses it turns into a style and a link
// are read as those, so the elements a line is sent as are the ones its
// markdown would have become.
var lineParser = parser.New(parser.WithExtensions(extension.NewStrikethroughParser(), extension.NewLinkifyParser()))

// lineElems spells one line in the elements the client's editor writes. It
// answers false for a line that is more than words — a heading, a list item,
// code, a picture, a tag other than <at> — or that names no emoji: such a line
// goes as the md element it already is.
func lineElems(line string) ([]elem, bool) {
	src := []byte(line)
	p, ok := lineParser.Parse(src).FirstChild().(*ast.Paragraph)
	if !ok || p.NextSibling() != nil {
		return nil, false
	}
	w := lineWalk{src: src, ok: true}
	w.inline(p)
	if !w.ok || w.inAt {
		return nil, false
	}
	return withEmotions(w.out)
}

// lineWalk gathers the elements of one line. ok falls at the first node the
// post format has no element for.
type lineWalk struct {
	src   []byte
	style []string
	href  string
	inAt  bool
	out   []elem
	ok    bool
}

func (w *lineWalk) inline(n ast.Node) {
	for c := n.FirstChild(); c != nil && w.ok; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			// Feishu fills a mention's name in from its id, so the name
			// written between the tags is not sent.
			if !w.inAt {
				w.words(c.Value.Value(w.src))
			}
		case *ast.Emphasis:
			w.styled("italic", c)
		case *ast.Strong:
			w.styled("bold", c)
		case *gast.Strikethrough:
			w.styled("lineThrough", c)
		case *ast.Link:
			switch dest := c.Destination.Value(w.src); {
			case webAddress(dest):
				w.linked(dest, c)
			case linkDropped(dest):
				// Feishu drops such a link and keeps its words.
				w.inline(c)
			default:
				w.ok = false
			}
		case *ast.AutoLink:
			label := c.Label.Value(w.src)
			if !webAddress(label) {
				w.ok = false
				break
			}
			w.href = c.Destination.Value(w.src)
			w.words(label)
			w.href = ""
		case *ast.RawHTML:
			w.tag(c.Value.Str(w.src))
		default:
			w.ok = false
		}
	}
}

// webAddress reports a destination an a element this path writes has been
// watched arriving as a link. The rest an md element keeps — lark://, a bare
// host — stay in the md element, where what Feishu makes of them is on record
// (docs/markdown-lint/DIALECT.md).
func webAddress(dest string) bool {
	d := strings.ToLower(dest)
	return strings.HasPrefix(d, "https://") || strings.HasPrefix(d, "http://")
}

func (w *lineWalk) styled(name string, n ast.Node) {
	w.style = append(w.style, name)
	w.inline(n)
	w.style = w.style[:len(w.style)-1]
}

func (w *lineWalk) linked(href string, n ast.Node) {
	w.href = href
	w.inline(n)
	w.href = ""
}

// words adds a stretch of text, running it on to the element before when the
// two read the same: goldmark hands a bracket over as a node of its own, and
// an emoji name is only found once its brackets and its name are one string.
func (w *lineWalk) words(s string) {
	if s == "" {
		return
	}
	tag := "text"
	if w.href != "" {
		tag = "a"
	}
	if i := len(w.out) - 1; i >= 0 && w.out[i].Tag == tag && w.out[i].Href == w.href && slices.Equal(w.out[i].Style, w.style) {
		*w.out[i].Text += s
		return
	}
	w.out = append(w.out, elem{Tag: tag, Text: new(s), Style: slices.Clone(w.style), Href: w.href})
}

// atOpen is the tag a resolved mention opens with, the one tag a post reads.
var atOpen = regexp.MustCompile(`^<at user_id="([^"]+)">$`)

func (w *lineWalk) tag(raw string) {
	switch m := atOpen.FindStringSubmatch(raw); {
	case m != nil && !w.inAt:
		w.out = append(w.out, elem{Tag: "at", UserID: m[1], Style: slices.Clone(w.style)})
		w.inAt = true
	case raw == "</at>" && w.inAt:
		w.inAt = false
	default:
		w.ok = false
	}
}

// emojiName is a bracketed name, the spelling a text message carries an emoji
// in and the one the composer writes for an emoji no character carries.
var emojiName = regexp.MustCompile(`\[([^\[\]\n]+)\]`)

// withEmotions cuts every emoji name out of the text elements and puts the
// emotion it names in its place. A link's label is its words, so it is left
// alone. found is false when nothing was cut, which leaves the line in the md
// element it came from.
func withEmotions(els []elem) (out []elem, found bool) {
	for _, el := range els {
		if el.Tag != "text" {
			out = append(out, el)
			continue
		}
		s, at := *el.Text, 0
		for _, m := range emojiName.FindAllStringSubmatchIndex(s, -1) {
			e, ok := emotionFor(s[m[2]:m[3]])
			if !ok {
				continue
			}
			if m[0] > at {
				out = append(out, elem{Tag: "text", Text: new(s[at:m[0]]), Style: el.Style})
			}
			out = append(out, elem{Tag: "emotion", EmojiType: e.Key})
			at, found = m[1], true
		}
		if at < len(s) {
			out = append(out, elem{Tag: "text", Text: new(s[at:]), Style: el.Style})
		}
	}
	return out, found
}

// emotionFor resolves a bracketed name to the emoji a post carries it as,
// either language's name reaching the same one, as it does in a text message.
// An emoji the client has withdrawn is left as its name: sent as an emotion it
// reaches the other side as "[Sensitive emoji]".
func emotionFor(name string) (emoji.Emoji, bool) {
	e, ok := emoji.ByName(name)
	return e, ok && e.Offerable() && !e.Delisted
}
