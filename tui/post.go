package tui

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// postEmpty reports a body with nothing in it, which is how a locale-wrapped
// one is told from the bare shape it parses into first.
func postEmpty(b sync.PostBody) bool { return b.Title == "" && len(b.Paragraphs()) == 0 }

// postBodyOf reads a body in either form the wire uses: the bare
// {title, content} the API returns, and the locale-wrapped {zh_cn: {...}}
// lark-cli sends, which is how a post larkim sent itself reads back.
func postBodyOf(contentRaw string) (sync.PostBody, bool) {
	var b sync.PostBody
	if json.Unmarshal([]byte(contentRaw), &b) == nil && !postEmpty(b) {
		return b, true
	}
	var byLocale map[string]sync.PostBody
	if json.Unmarshal([]byte(contentRaw), &byLocale) != nil {
		return sync.PostBody{}, false
	}
	for _, loc := range slices.Sorted(maps.Keys(byLocale)) {
		if b := byLocale[loc]; !postEmpty(b) {
			return b, true
		}
	}
	return sync.PostBody{}, false
}

// postRows draw a rich-text body as the elements it was written from. The
// structure is the part no rendering gives back: a paragraph is a line and an
// empty one is a blank line, a code block carries the language it was tagged
// with, a picture stands where it was placed, and the words between them are
// words rather than markup.
func postRows(b sync.PostBody, x store.Message, idx int, st msgStyle, g *leads, ms mentions) []msgRow {
	d := postDoc{x: x, idx: idx, st: st, g: g, ms: ms}
	var rows []msgRow
	if b.Title != "" {
		rows = append(rows, d.segRows(d.words(b.Title, []string{"bold"}))...)
	}
	paras := b.Paragraphs()
	for i := 0; i < len(paras); {
		if md, n := postList(paras[i:]); n > 0 {
			rows = append(rows, mdRows(md, x, idx, st, g, ms)...)
			i += n
			continue
		}
		rows = append(rows, d.paragraph(paras[i])...)
		i++
	}
	return rows
}

// postListMarker matches the bullet or number a paragraph opens a list item
// with. Feishu's rich text has no list element, so a list reaches larkim only
// as the characters somebody typed in front of each item; the space after the
// marker is what separates a list from a word a hyphen runs into.
var postListMarker = regexp.MustCompile(`^( *)(?:[-*+]|\d{1,9}[.)]) +\S`)

// postList reads the run of list items paragraphs open with, answering the
// markdown they spell and how many of them it took. The run is handed to the
// markdown path whole so that one parse decides the nesting, the numbering and
// which marker each item wears.
//
// This is the one place a post's words are read as markup: somebody who wrote
// a marker meant a list, and the paragraphs around it stay literal.
func postList(paras [][]sync.PostElem) (string, int) {
	var lines []string
	for _, para := range paras {
		line, ok := postMDLine(para)
		if !ok {
			break
		}
		m := postListMarker.FindStringSubmatch(line)
		if m == nil {
			break
		}
		// Four columns in, a parser reads an indented code block rather than
		// a list, so a run cannot open that far right.
		if len(lines) == 0 && len(m[1]) > 3 {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), len(lines)
}

// postMDLine spells one paragraph as the markdown that reads back as the
// elements it holds. It answers false for a paragraph carrying something no
// list item does — a code block, a rule, a media file — which leaves that
// paragraph on the element path whatever it opens with.
func postMDLine(para []sync.PostElem) (string, bool) {
	var b strings.Builder
	for _, el := range para {
		switch el.Tag {
		case "code_block", "hr", "media":
			return "", false
		case "a":
			b.WriteString(postMDLink(el))
		case "at":
			fmt.Fprintf(&b, `<at user_id="%s">%s</at>`, el.UserID, el.UserName)
		case "emotion":
			b.WriteString(":" + el.EmojiType + ":")
		case "img":
			b.WriteString("![Image](" + el.ImageKey + ")")
		case "md":
			b.WriteString(el.Text)
		default:
			b.WriteString(sync.StyleMarkdown(el.Text, el.Style))
		}
	}
	return b.String(), true
}

// postMDLink spells an `a` element. A label that is the address itself is
// written out rather than spelled as a link, which is what sends it down the
// path that names a Feishu document by its title.
func postMDLink(el sync.PostElem) string {
	switch {
	case el.Href == "":
		return el.Text
	case el.Text == "" || el.Text == el.Href:
		return el.Href
	}
	return "[" + el.Text + "](" + el.Href + ")"
}

// postDoc is what every paragraph of one body is drawn against: the message
// its pictures are read from, and the styling the pane hands down.
type postDoc struct {
	x   store.Message
	idx int
	st  msgStyle
	g   *leads
	ms  mentions
}

// paragraph draws one paragraph. Words gather into the pieces a row is packed
// from; a picture, a code block or an embedded markdown document takes rows of
// its own, so the words written beside it are laid down first.
func (d postDoc) paragraph(para []sync.PostElem) []msgRow {
	var rows []msgRow
	var segs []rowSeg
	flush := func() {
		if len(segs) > 0 {
			rows = append(rows, d.segRows(segs)...)
			segs = nil
		}
	}
	for i, el := range para {
		switch el.Tag {
		case "img":
			flush()
			rows = append(rows, pictureRows(el.ImageKey, d.x, d.idx, d.st, d.g)...)
		case "code_block":
			flush()
			code := strings.Split(strings.TrimRight(el.Text, "\n"), "\n")
			rows = append(rows, textRows(codeRows(code, el.Language, d.st.inner(), d.st.dark), d.idx, d.g)...)
		case "md":
			// The one element whose text is markdown by the client's own
			// doing, so it is the one that goes to the markdown path. Its
			// text is still verbatim as far as emoji go: Feishu draws an
			// emoji in a post from an emotion element, never from a name
			// somebody typed into an md element.
			flush()
			rows = append(rows, mdRows(el.Text, d.x, d.idx, d.st, d.g, d.ms.spelling(spellNone))...)
		case "hr":
			flush()
			rows = append(rows, textRows([]string{stDim.Render(strings.Repeat("─", d.st.inner()))}, d.idx, d.g)...)
		case "at":
			segs = append(segs, rowSeg{text: d.ms.draw(d.ms.tagRun(el.UserID, el.UserName))})
			// The client sets a mention off as a chip; here the space is the
			// author's, so words running straight on need one lent to them.
			if i+1 < len(para) && glues(para[i+1].Text) {
				segs = append(segs, rowSeg{text: " "})
			}
		case "a":
			segs = append(segs, d.link(el)...)
		case "emotion":
			segs = append(segs, d.emotion(el.EmojiType)...)
		case "media":
			segs = append(segs, rowSeg{text: stDim.Render(msgTypeLabel("media"))})
		default:
			segs = append(segs, d.words(el.Text, el.Style)...)
		}
	}
	if len(rows) == 0 && len(segs) == 0 {
		// A paragraph with nothing in it is a blank line somebody left, which
		// the client draws as one.
		return []msgRow{{lead: d.g.take(), idx: d.idx}}
	}
	flush()
	return rows
}

// words render one element's own text: the addresses written out in it cut
// away, and the rest drawn in the emphasis the element carries. Nothing here
// reads markup — a post spells its links and its emphasis as elements, so an
// asterisk in the words is an asterisk — and nothing here reads an emoji
// either: the element's text is what the client draws, character for
// character, so "[赞]" in it is three characters and not a face.
func (d postDoc) words(s string, style []string) []rowSeg {
	if s == "" {
		return nil
	}
	ms := d.ms.styled(postStyle(d.ms.base, style)).spelling(spellNone)
	cuts := autoLinkCuts(s, nil, d.st.docLabel)
	return cutSegs(s, cuts, ms.render)
}

// emotion draws the one element an emoji in a post comes from. The glyph is
// preferred over the picture for the same reason the rest of larkim prefers
// it: it is text, so it survives a copy and costs the terminal nothing. A key
// this build has no entry for, or one no character carries where no picture
// can be drawn, is left as the shortcode it was read as — the spelling the
// reader can still look up.
func (d postDoc) emotion(key string) []rowSeg {
	e, known := emoji.ByKey(key)
	spelled := []rowSeg{{text: d.ms.base.Render(":" + key + ":")}}
	switch {
	case !known:
		return spelled
	case e.Glyph != "":
		return []rowSeg{{text: d.ms.base.Render(e.Glyph)}}
	}
	if p := d.st.emojiInline(e.Key); p.cols > 0 {
		return []rowSeg{{pic: p}}
	}
	return spelled
}

// link draws an `a` element. A label that is the address itself is drawn the
// way a written-out address is, so a Feishu document link becomes the document
// rather than its token.
func (d postDoc) link(el sync.PostElem) []rowSeg {
	if el.Text == "" || el.Text == el.Href {
		return d.words(el.Href, el.Style)
	}
	if el.Href == "" {
		return d.words(el.Text, el.Style)
	}
	label := flatten(el.Text)
	return []rowSeg{{
		text:  postStyle(stLink, el.Style).Render(el.Text),
		urls:  []string{el.Href},
		label: label,
		note:  "opening " + label,
	}}
}

func (d postDoc) segRows(segs []rowSeg) []msgRow {
	return segRows(segs, "", d.idx, d.st, d.g)
}

// postStyle applies the emphasis a rich-text element carries. Feishu keeps it
// as names beside the words rather than as markup around them, which is what
// lets the words stay literal.
func postStyle(base lipgloss.Style, names []string) lipgloss.Style {
	for _, n := range names {
		switch n {
		case "bold":
			base = base.Bold(true)
		case "italic":
			base = base.Italic(true)
		case "underline":
			base = base.Underline(true)
		case "lineThrough":
			base = base.Strikethrough(true)
		}
	}
	return base
}

// postText reads what a rich-text body says, paragraph per line. It is the
// one-line-per-paragraph form the chat list and a pending body are named by;
// the drawn body is postRows.
func postText(contentRaw string) string {
	b, ok := postBodyOf(contentRaw)
	if !ok {
		return ""
	}
	lines := make([]string, 0, len(b.Paragraphs())+1)
	if b.Title != "" {
		lines = append(lines, b.Title)
	}
	for _, para := range b.Paragraphs() {
		var line strings.Builder
		for _, el := range para {
			switch el.Tag {
			case "img":
				line.WriteString(msgTypeLabel("image"))
			case "media":
				line.WriteString(msgTypeLabel("media"))
			case "hr":
			case "at":
				line.WriteString("@" + el.UserName)
			case "emotion":
				line.WriteString(":" + el.EmojiType + ":")
			default:
				line.WriteString(el.Text)
			}
		}
		lines = append(lines, line.String())
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
