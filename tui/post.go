package tui

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

// postElem is one element of a rich-text body. Every tag keeps the fields it
// arrived with rather than a spelling of them: a rendering flattens the whole
// body to markdown, where an asterisk somebody typed and one the flattening
// added are the same character.
type postElem struct {
	Tag       string   `json:"tag"`
	Text      string   `json:"text"`
	Style     []string `json:"style"`
	Href      string   `json:"href"`
	UserID    string   `json:"user_id"`
	UserName  string   `json:"user_name"`
	EmojiType string   `json:"emoji_type"`
	ImageKey  string   `json:"image_key"`
	FileKey   string   `json:"file_key"`
	Language  string   `json:"language"`
}

// postBody is a rich-text body: a title, and the paragraphs below it. The
// client writes content_v2 and keeps content beside it as the older spelling
// of the same body, so the two are never merged — whichever is read is read
// whole.
type postBody struct {
	Title     string       `json:"title"`
	ContentV2 [][]postElem `json:"content_v2"`
	Content   [][]postElem `json:"content"`
}

func (b postBody) paragraphs() [][]postElem {
	if len(b.ContentV2) > 0 {
		return b.ContentV2
	}
	return b.Content
}

func (b postBody) empty() bool { return b.Title == "" && len(b.paragraphs()) == 0 }

// postBodyOf reads a body in either form the wire uses: the bare
// {title, content} the API returns, and the locale-wrapped {zh_cn: {...}}
// lark-cli sends, which is how a post larkim sent itself reads back.
func postBodyOf(contentRaw string) (postBody, bool) {
	var b postBody
	if json.Unmarshal([]byte(contentRaw), &b) == nil && !b.empty() {
		return b, true
	}
	var byLocale map[string]postBody
	if json.Unmarshal([]byte(contentRaw), &byLocale) != nil {
		return postBody{}, false
	}
	for _, loc := range slices.Sorted(maps.Keys(byLocale)) {
		if b := byLocale[loc]; !b.empty() {
			return b, true
		}
	}
	return postBody{}, false
}

// postRows draw a rich-text body as the elements it was written from. The
// structure is the part no rendering gives back: a paragraph is a line and an
// empty one is a blank line, a code block carries the language it was tagged
// with, a picture stands where it was placed, and the words between them are
// words rather than markup.
func postRows(b postBody, x store.Message, idx int, st msgStyle, g *leads, ms mentions) []msgRow {
	d := postDoc{x: x, idx: idx, st: st, g: g, ms: ms}
	var rows []msgRow
	if b.Title != "" {
		rows = append(rows, d.segRows(d.words(b.Title, []string{"bold"}))...)
	}
	paras := b.paragraphs()
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
func postList(paras [][]postElem) (string, int) {
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
func postMDLine(para []postElem) (string, bool) {
	var b strings.Builder
	for _, el := range para {
		switch el.Tag {
		case "code_block", "hr", "media":
			return "", false
		case "a":
			b.WriteString(postMDLink(el))
		case "at":
			b.WriteString(fmt.Sprintf(`<at user_id="%s">%s</at>`, el.UserID, el.UserName))
		case "emotion":
			b.WriteString(":" + el.EmojiType + ":")
		case "img":
			b.WriteString("![Image](" + el.ImageKey + ")")
		case "md":
			b.WriteString(el.Text)
		default:
			b.WriteString(postMDStyle(el.Text, el.Style))
		}
	}
	return b.String(), true
}

// postMDLink spells an `a` element. A label that is the address itself is
// written out rather than spelled as a link, which is what sends it down the
// path that names a Feishu document by its title.
func postMDLink(el postElem) string {
	switch {
	case el.Href == "":
		return el.Text
	case el.Text == "" || el.Text == el.Href:
		return el.Href
	}
	return "[" + el.Text + "](" + el.Href + ")"
}

// postMDStyle spells an element's emphasis as the markup the markdown path
// reads it back from. It is postStyle written out instead of painted.
func postMDStyle(text string, styles []string) string {
	if text == "" || len(styles) == 0 {
		return text
	}
	has := func(name string) bool { return slices.Contains(styles, name) }
	if has("bold") {
		text = "**" + text + "**"
	}
	if has("italic") {
		text = "*" + text + "*"
	}
	if has("underline") {
		text = "<u>" + text + "</u>"
	}
	if has("lineThrough") {
		text = "~~" + text + "~~"
	}
	return text
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
func (d postDoc) paragraph(para []postElem) []msgRow {
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
			rows = append(rows, d.lines(codeRows(code, el.Language, d.st.inner(), d.st.dark))...)
		case "md":
			// The one element whose text is markdown by the client's own
			// doing, so it is the one that goes to the markdown path.
			flush()
			rows = append(rows, mdRows(el.Text, d.x, d.idx, d.st, d.g, d.ms)...)
		case "hr":
			flush()
			rows = append(rows, d.lines([]string{stDim.Render(strings.Repeat("─", d.st.inner()))})...)
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
			// The shortcode spelling is the one the emoji table is keyed by.
			segs = append(segs, d.words(":"+el.EmojiType+":", nil)...)
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

// words render one element's own text: the addresses written out in it and the
// emoji the client draws as pictures cut away, and the rest drawn in the
// emphasis the element carries. Nothing here reads markup — a post spells its
// links and its emphasis as elements, so an asterisk in the words is an
// asterisk.
func (d postDoc) words(s string, style []string) []rowSeg {
	if s == "" {
		return nil
	}
	ms := d.ms.styled(postStyle(d.ms.base, style))
	cuts := autoLinkCuts(s, nil, d.st.docLabel)
	cuts = append(cuts, emojiCuts(s, cuts, d.st.emojiInline)...)
	return cutSegs(s, cuts, ms.render)
}

// link draws an `a` element. A label that is the address itself is drawn the
// way a written-out address is, so a Feishu document link becomes the document
// rather than its token.
func (d postDoc) link(el postElem) []rowSeg {
	if el.Text == "" || el.Text == el.Href {
		return d.words(el.Href, el.Style)
	}
	if el.Href == "" {
		return d.words(el.Text, el.Style)
	}
	label := flatten(el.Text)
	return []rowSeg{{
		text:  postStyle(stLink, el.Style).Render(expandEmoji(el.Text)),
		urls:  []string{el.Href},
		label: label,
		note:  "opening " + label,
	}}
}

func (d postDoc) segRows(segs []rowSeg) []msgRow {
	return segRows(segs, "", d.idx, d.st, d.g)
}

func (d postDoc) lines(ls []string) []msgRow {
	out := make([]msgRow, 0, len(ls))
	for _, l := range ls {
		out = append(out, msgRow{lead: d.g.take(), text: l, idx: d.idx})
	}
	return out
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
	lines := make([]string, 0, len(b.paragraphs())+1)
	if b.Title != "" {
		lines = append(lines, b.Title)
	}
	for _, para := range b.paragraphs() {
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
