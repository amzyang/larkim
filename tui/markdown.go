package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/amzyang/larkim/store"
	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	gast "github.com/yuin/goldmark/v2/extension/ast"
	"github.com/yuin/goldmark/v2/parser"
)

// mdParser reads the blocks a post body is built from. Only the table
// extension is added: everything else larkim draws is CommonMark, and a parser
// is safe to share, so one is built for the process rather than per message.
var mdParser = parser.New(parser.WithExtensions(extension.NewTableParser()))

// mdIndent is the width one level of nesting costs a list or a quote.
const mdIndent = 2

// mdRows draws markdown as the document it is: heading levels, list markers,
// quote gutters, rules and tables, instead of the markup that spells them.
// Post bodies and card bodies come here — a text message does not, being
// literal: someone typing "3 * 4 * 5" in one means the asterisks. x is the
// message the body belongs to, which is where its pictures are read from.
func mdRows(body string, x store.Message, idx int, st msgStyle, g *leads, ms mentions) []msgRow {
	src := []byte(strings.ReplaceAll(body, "\r", ""))
	d := mdDoc{src: src, x: x, idx: idx, st: st, g: g, ms: ms}
	return d.blocks(mdParser.Parse(src), 0, 0)
}

// mdDoc is what every block of one body is drawn against: the source it was
// parsed from, the message its pictures are read from, and the styling the
// pane hands down. Only the nesting depth changes as the walk descends, so it
// stays a parameter.
type mdDoc struct {
	src []byte
	x   store.Message
	idx int
	st  msgStyle
	g   *leads
	ms  mentions
}

// blocks walks one level of the document. depth is how far the blocks are
// nested inside lists and quotes, which is what a bullet's shape is drawn
// from; indent is the columns that nesting has already charged them, which a
// list sets from the marker it draws rather than from the depth, so a wide
// marker moves the text rather than pushing the row past the pane.
func (d mdDoc) blocks(parent ast.Node, depth, indent int) []msgRow {
	var rows []msgRow
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		rows = append(rows, d.block(n, depth, indent)...)
	}
	return rows
}

func (d mdDoc) block(n ast.Node, depth, indent int) []msgRow {
	pad := strings.Repeat(" ", indent)
	room := max(4, d.st.inner()-indent)
	text := func(lines []string) []msgRow {
		out := make([]msgRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, msgRow{lead: d.g.take(), text: pad + l, idx: d.idx})
		}
		return out
	}

	switch n.Kind() {
	case ast.KindHeading:
		h := n.(*ast.Heading)
		return text(wrap(mdHeadingStyle(h.Level).Render(renderInline(mdSource(n, d.src), d.ms)), room))

	case ast.KindThematicBreak:
		return text([]string{stDim.Render(strings.Repeat("─", room))})

	case ast.KindBlockquote:
		var rows []msgRow
		for _, r := range d.blocks(n, depth+1, indent+mdIndent) {
			// The gutter replaces the indent the nesting already paid for, so
			// a quote does not drift further right than its contents need.
			r.reindent(pad+"  ", pad+stDim.Render("│")+" ")
			rows = append(rows, r)
		}
		return rows

	case ast.KindList:
		return d.list(n.(*ast.List), depth, indent)

	case ast.KindCodeBlock:
		c := n.(*ast.CodeBlock)
		lang, _ := c.Language(d.src)
		code := strings.Split(strings.TrimRight(c.Value.Str(d.src), "\n"), "\n")
		return text(codeRows(code, lang, room, d.st.dark))

	case gast.KindTable:
		return text(d.table(n, room))

	case ast.KindParagraph:
		return d.paragraph(n, pad, room)
	}
	// Anything with children larkim has no frame for still shows its content.
	if n.HasChildren() {
		return d.blocks(n, depth, indent)
	}
	return nil
}

// mdHeadingStyle grades a heading by level. Feishu draws every level at one
// size, so this is the only place the structure a person wrote survives.
func mdHeadingStyle(level int) lipgloss.Style {
	if level <= 2 {
		return stBold.Foreground(colAccent)
	}
	return stBold
}

func (d mdDoc) list(l *ast.List, depth, indent int) []msgRow {
	// One step for the whole list, taken from its last marker: "10." is the
	// widest thing it draws, and an item indented less than its neighbour
	// would read as nesting rather than as the same level.
	step := mdIndent
	if l.IsOrdered() {
		step = len(strconv.Itoa(l.Start+l.ChildCount()-1)) + 2 // the dot, then the gap
	}
	var rows []msgRow
	n := l.Start
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "•"
		if depth > 0 {
			marker = "◦"
		}
		if l.IsOrdered() {
			marker = strconv.Itoa(n) + "."
			n++
		}
		inner := d.blocks(item, depth+1, indent+step)
		if len(inner) > 0 {
			// The marker takes the place of the first line's indent, so the
			// text of every item starts at the same column.
			inner[0].reindent(strings.Repeat(" ", indent+step),
				strings.Repeat(" ", indent)+stDim.Render(marker)+
					strings.Repeat(" ", step-lipgloss.Width(marker)))
		}
		rows = append(rows, inner...)
	}
	return rows
}

// paragraph keeps the existing inline path, which is what carries inline
// pictures, the client's own emoji and per-message mention styling.
func (d mdDoc) paragraph(n ast.Node, pad string, room int) []msgRow {
	var rows []msgRow
	for _, line := range strings.Split(mdSource(n, d.src), "\n") {
		keys, rest := splitImages(line)
		if len(keys) == 0 || strings.TrimSpace(rest) != "" {
			if segs := inlineSegs(rest, d.ms, d.st.emojiInline, d.st.docLabel); segs != nil {
				rows = append(rows, segRows(segs, pad, d.idx, d.st, d.g)...)
			} else {
				for _, l := range wrap(renderInline(rest, d.ms), room) {
					rows = append(rows, msgRow{lead: d.g.take(), text: pad + l, idx: d.idx})
				}
			}
		}
		for _, key := range keys {
			rows = append(rows, pictureRows(key, d.x, d.idx, d.st, d.g)...)
		}
	}
	return rows
}

// mdSource rebuilds the markdown behind a block's inline content. goldmark
// parsed the markup away and larkim's inline path still wants it: that path is
// what carries inline pictures, the client's own emoji and mention styling, so
// the markup is re-emitted rather than the path replaced.
func mdSource(n ast.Node, src []byte) string {
	var b strings.Builder
	mdWriteInline(&b, n, src)
	return b.String()
}

func mdWriteInline(b *strings.Builder, parent ast.Node, src []byte) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		switch c := n.(type) {
		case *ast.Text:
			b.WriteString(c.Value.Value(src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte('\n')
			}
		case *ast.CodeSpan:
			// A span keeps its content in Value rather than in children, so
			// recursing into it would drop the text entirely.
			b.WriteString("`" + c.Value.Value(src) + "`")
		case *ast.Emphasis:
			b.WriteString("*")
			mdWriteInline(b, c, src)
			b.WriteString("*")
		case *ast.Strong:
			b.WriteString("**")
			mdWriteInline(b, c, src)
			b.WriteString("**")
		case *ast.Link:
			b.WriteString("[")
			mdWriteInline(b, c, src)
			b.WriteString("](" + c.Destination.Value(src) + ")")
		case *ast.Image:
			b.WriteString("![")
			mdWriteInline(b, c, src)
			b.WriteString("](" + c.Destination.Value(src) + ")")
		case *ast.AutoLink:
			b.WriteString(c.Destination.Value(src))
		case *ast.RawHTML:
			// Feishu spells a mention as <at user_id="…">, which is raw HTML
			// to a markdown parser and has to reach renderInline intact.
			b.WriteString(c.Value.Str(src))
		default:
			mdWriteInline(b, n, src)
		}
	}
}

// table lays a grid out with lipgloss, which wraps a cell that will not fit
// rather than letting it run past the pane.
func (d mdDoc) table(n ast.Node, room int) []string {
	t := table.New().Width(room).Wrap(true).
		BorderStyle(stDim).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})
	cells := func(row ast.Node) []string {
		var out []string
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			out = append(out, renderInline(mdSource(c, d.src), d.ms))
		}
		return out
	}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case gast.KindTableHeader:
			t.Headers(cells(child)...)
		case gast.KindTableBody:
			// The data rows hang under a body node, not under the table.
			for row := child.FirstChild(); row != nil; row = row.NextSibling() {
				t.Row(cells(row)...)
			}
		case gast.KindTableRow:
			t.Row(cells(child)...)
		}
	}
	return strings.Split(t.Render(), "\n")
}
