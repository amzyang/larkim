package card

import (
	"cmp"
	"encoding/json"
	"strconv"
	"strings"
)

// writer walks the card tree into the blocks a reader draws. Text is written
// as markdown, which is what the card's own elements spell out, and the blank
// lines between blocks are the whole point: without them a heading, a list and
// a quote parse as one paragraph.
type writer struct {
	images map[string]string
	people map[string]person
	blocks []Block
	b      strings.Builder
}

func (w *writer) done() []Block {
	w.flush()
	return w.blocks
}

// write adds inline text to the block being built.
func (w *writer) write(s string) { w.b.WriteString(s) }

// gap closes the block being built, so what follows starts a new one.
func (w *writer) gap() {
	s := w.b.String()
	switch {
	case s == "" || strings.HasSuffix(s, "\n\n"):
	case strings.HasSuffix(s, "\n"):
		w.b.WriteString("\n")
	default:
		w.b.WriteString("\n\n")
	}
}

// block writes one whole construct — a heading, a quote, a table — with the
// gaps that keep it apart from its neighbours.
func (w *writer) block(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	w.gap()
	w.b.WriteString(s)
	w.gap()
}

func (w *writer) flush() {
	if md := strings.TrimRight(strings.TrimLeft(w.b.String(), "\n"), " \t\n"); md != "" {
		w.blocks = append(w.blocks, Block{Markdown: md})
	}
	w.b.Reset()
}

// image gives a picture a block of its own: a reader places it itself.
func (w *writer) image(id string) {
	key := w.images[id]
	if key == "" {
		return
	}
	w.flush()
	w.blocks = append(w.blocks, Block{ImageKey: key})
}

// button joins the row being built when nothing came between, so the buttons
// of an action row — or of a column each — read as the one row the client
// draws.
func (w *writer) button(label string) {
	if label == "" {
		return
	}
	if w.b.Len() == 0 && len(w.blocks) > 0 {
		if last := &w.blocks[len(w.blocks)-1]; last.Buttons != nil {
			last.Buttons = append(last.Buttons, label)
			return
		}
	}
	w.flush()
	w.blocks = append(w.blocks, Block{Buttons: []string{label}})
}

// at spells a mention the way a body carries one. The id a card @s by belongs
// to the sending app, so the reader's own mentions are reached through the
// key the attachment table pairs it with.
func (w *writer) at(userID string) string {
	who := w.people[userID]
	key := userID
	if who.MentionKey != "" {
		key = who.MentionKey
	}
	return `<at user_id="` + key + `">` + who.Name + `</at>`
}

// capture renders elements as one run of inline markdown, for the constructs
// that need their text before they can be written: a heading's line, a quote's
// gutter, a list item, a table cell.
func (w *writer) capture(els []elem) (string, []Block) {
	sub := writer{images: w.images, people: w.people}
	sub.elements(els)
	return strings.TrimSpace(sub.b.String()), sub.blocks
}

// after places what an inline context could not hold — a picture inside a
// list item — below the text it sat in, which is where it can be drawn at all.
func (w *writer) after(extra []Block) {
	if len(extra) == 0 {
		return
	}
	w.flush()
	w.blocks = append(w.blocks, extra...)
}

// blockOf writes one element as a block of its own.
func (w *writer) blockOf(e *elem) {
	if e == nil {
		return
	}
	text, extra := w.capture([]elem{*e})
	w.block(text)
	w.after(extra)
}

func (w *writer) elements(els []elem) {
	for _, e := range els {
		w.element(e)
	}
}

func (w *writer) element(e elem) {
	p := e.Property
	switch e.Tag {
	case "plain_text", "text":
		w.write(styled(content(p), p.TextStyle))
	case "br":
		w.write("\n")
	case "code_span":
		if p.Content != "" {
			w.write("`" + p.Content + "`")
		}
	case "link":
		w.write("[" + p.Content + "](" + p.URL.URL + ")")
	case "number_tag":
		w.write("[" + plain(p.Text) + "](" + p.URL.URL + ")")
	case "text_tag":
		if s := plain(p.Text); s != "" {
			w.write("「" + s + "」")
		}
	case "at":
		w.write(w.at(p.UserID))
	case "emoji":
		w.write(":" + emojiKey(p.Key) + ":")
	case "heading":
		text, extra := w.capture(p.Elements)
		if text != "" {
			w.block(strings.Repeat("#", min(max(p.Level, 1), 6)) + " " + text)
		}
		w.after(extra)
	case "blockquote":
		text, extra := w.capture(p.Elements)
		w.block(quote(text))
		w.after(extra)
	case "list":
		w.list(p.Items)
	case "code_block":
		w.block(code(p))
	case "table":
		w.table(p)
	case "hr":
		w.block("---")
	case "img":
		w.image(p.ImageID)
	case "button":
		w.button(plain(p.Text))
	case "div":
		w.blockOf(p.Text)
		for _, f := range p.Fields {
			w.blockOf(f.Text)
		}
		w.blockOf(p.Extra)
	case "note":
		text, extra := w.capture(p.Elements)
		w.block(text)
		w.after(extra)
	case "column_set":
		// Columns stand side by side in the client and stack here: a
		// document has no room to keep them abreast.
		var cols []elem
		if json.Unmarshal(p.Columns, &cols) == nil {
			for _, col := range cols {
				w.gap()
				w.element(col)
				w.gap()
			}
		}
	case "action", "actions":
		var acts []elem
		if json.Unmarshal(p.Actions, &acts) == nil {
			w.elements(acts)
		}
	case "collapsible_panel":
		w.blockOf(p.Header)
		w.elements(p.Elements)
	case "markdown", "markdown_v1":
		if els := e.children(); len(els) > 0 {
			w.elements(els)
			return
		}
		w.block(content(p))
	case "standard_icon", "ud_icon", "card_header", "fallback_text", "markdown_fallback":
		// An icon is named by a token no reader outside the client can draw,
		// and a fallback stands in for elements that are read for themselves.
	default:
		// An element this package has no frame for still shows what it holds.
		w.elements(e.children())
	}
}

func (w *writer) list(items []listItem) {
	var lines []string
	var extras []Block
	for i, it := range items {
		marker := "- "
		if it.Type == "ol" {
			marker = strconv.Itoa(cmp.Or(it.Order, i+1)) + ". "
		}
		text, extra := w.capture(it.Elements)
		lines = append(lines, strings.Repeat("  ", it.Level)+marker+text)
		extras = append(extras, extra...)
	}
	w.block(strings.Join(lines, "\n"))
	w.after(extras)
}

// table writes the grid as a markdown table.
func (w *writer) table(p prop) {
	var cols []tableCol
	if json.Unmarshal(p.Columns, &cols) != nil || len(cols) == 0 {
		return
	}
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = tableCellText(cmp.Or(c.DisplayName, c.Name))
	}
	rows := []string{"| " + strings.Join(head, " | ") + " |", "|" + strings.Repeat(" --- |", len(cols))}
	for _, r := range p.Rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = w.cell(r[c.Name])
		}
		rows = append(rows, "| "+strings.Join(cells, " | ")+" |")
	}
	w.block(strings.Join(rows, "\n"))
}

// cell reads one table cell, which holds either a string or an element.
func (w *writer) cell(c tableCell) string {
	if len(c.Data) == 0 {
		return ""
	}
	if c.Data[0] == '"' {
		var s string
		json.Unmarshal(c.Data, &s)
		return tableCellText(s)
	}
	var e elem
	if json.Unmarshal(c.Data, &e) != nil {
		return ""
	}
	text, _ := w.capture([]elem{e})
	return tableCellText(text)
}

// tableCellText fits text into one cell of a markdown table: the row ends at a
// newline, and a pipe of its own would open a column.
func tableCellText(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// quote puts every line of a quote behind the marker, so a quote of
// several lines stays one quote.
func quote(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// code rebuilds a code block from the tokens the card had it coloured in.
func code(p prop) string {
	lines := make([]string, 0, len(p.Contents))
	for _, l := range p.Contents {
		var b strings.Builder
		for _, c := range l.Contents {
			b.WriteString(c.Content)
		}
		lines = append(lines, strings.TrimRight(b.String(), "\n"))
	}
	code := strings.Join(lines, "\n")
	if strings.TrimSpace(code) == "" {
		return ""
	}
	lang := p.Language
	if lang == "plain_text" {
		lang = ""
	}
	return "```" + lang + "\n" + code + "\n```"
}

// styled spells the attributes a run of text carries as the markup a reader
// styles from. A run that spans lines takes them one at a time:
// the markers do not survive a line break.
func styled(content string, style textStyle) string {
	if content == "" || len(style.Attributes) == 0 {
		return content
	}
	opening, closing := "", ""
	for _, a := range style.Attributes {
		switch a {
		case "bold":
			opening, closing = opening+"**", "**"+closing
		case "italic":
			opening, closing = opening+"*", "*"+closing
		case "strikethrough":
			opening, closing = opening+"~~", "~~"+closing
		case "underline":
			opening, closing = opening+"<u>", "</u>"+closing
		}
	}
	if opening == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = opening + l + closing
		}
	}
	return strings.Join(lines, "\n")
}

// plain reads the text of an element that holds nothing else: a title, a
// button's label, a tag.
func plain(e *elem) string {
	if e == nil {
		return ""
	}
	if s := content(e.Property); s != "" {
		return s
	}
	var b strings.Builder
	for _, c := range e.children() {
		b.WriteString(plain(&c))
	}
	return b.String()
}

// content is the text an element holds. A sender who wrote in several
// languages sends them all, and Chinese comes first: that is the client larkim
// sits beside.
func content(p prop) string {
	if p.Content != "" {
		return p.Content
	}
	for _, lang := range []string{"zh_cn", "en_us", "ja_jp"} {
		if s := p.I18nContent[lang]; s != "" {
			return s
		}
	}
	return ""
}

// emojiKey trims a card's spelling of an emoji down to the emoji_type the
// rest of larkim speaks: Lark_Emoji_OK_0 is OK.
func emojiKey(key string) string {
	key = strings.TrimPrefix(key, "Lark_Emoji_")
	if i := strings.LastIndex(key, "_"); i > 0 {
		if _, err := strconv.Atoi(key[i+1:]); err == nil {
			key = key[:i]
		}
	}
	return key
}
