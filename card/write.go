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
	// top is the writer of the card body itself. Only there does a component
	// the client sets apart — a centred title, a panel — become a block of
	// its own: inside a table cell or a list item there is no block to make.
	top bool
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

func (w *writer) flush() { w.flushAs(Block{}) }

// flushAs closes the block being built as b, which carries how it is drawn.
func (w *writer) flushAs(b Block) {
	if md := strings.TrimRight(strings.TrimLeft(w.b.String(), "\n"), " \t\n"); md != "" {
		b.Markdown = md
		w.blocks = append(w.blocks, b)
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
func (w *writer) button(b Button) {
	if b.Label == "" {
		return
	}
	// A button filling its width shares the line with nothing.
	if w.b.Len() == 0 && len(w.blocks) > 0 && !b.Fill {
		if last := &w.blocks[len(w.blocks)-1]; last.Buttons != nil && !last.Buttons[0].Fill {
			last.Buttons = append(last.Buttons, b)
			return
		}
	}
	w.flush()
	w.blocks = append(w.blocks, Block{Buttons: []Button{b}})
}

// buttonAction is one of the things a button does when pressed. Only a link
// target is readable here: an action_request carries its payload to the app
// that sent the card and leaves an empty value behind in the message.
type buttonAction struct {
	Type   string `json:"type"`
	Action struct {
		URL   string `json:"url"`
		PCURL string `json:"pcURL"`
	} `json:"action"`
}

// buttonURL is where a button leads. pcURL wins over url: it is the target
// the card names for a desktop, which is the only place larkim runs.
func buttonURL(raw json.RawMessage) string {
	var acts []buttonAction
	if len(raw) == 0 || json.Unmarshal(raw, &acts) != nil {
		return ""
	}
	for _, a := range acts {
		if a.Type != "open_url" {
			continue
		}
		if a.Action.PCURL != "" {
			return a.Action.PCURL
		}
		if a.Action.URL != "" {
			return a.Action.URL
		}
	}
	return ""
}

// at spells a mention the way a body carries one. The id a card @s by belongs
// to the sending app, so the reader's own mentions are reached through the
// key the attachment table pairs it with.
func (w *writer) at(userID string) string {
	who := w.people[userID]
	return `<at user_id="` + cmp.Or(who.MentionKey, userID) + `">` + who.Name + `</at>`
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
		w.write(coloured(styled(content(p), p.TextStyle), p.TextStyle.Color))
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
			w.write(coloured("「"+s+"」", p.Color))
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
		fill := p.WidthValue.Type == "builtin_width" && string(p.WidthValue.Value) == `"fill"`
		w.button(Button{Label: plain(p.Text), URL: buttonURL(p.Actions), Type: p.Type, Fill: fill})
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
		var cols []elem
		if json.Unmarshal(p.Columns, &cols) != nil {
			return
		}
		panel := w.top && p.BackgroundStyle != "" && p.BackgroundStyle != "default"
		if panel {
			w.flush()
		}
		from := len(w.blocks)
		if row, ok := w.row(p.Columns, cols); ok {
			w.flush()
			w.blocks = append(w.blocks, Block{Row: row})
		} else {
			// Columns holding more than a line stack: a pane that could keep
			// them abreast would still have to wrap each into a sliver of it.
			for _, col := range cols {
				w.gap()
				w.element(col)
				w.gap()
			}
		}
		if panel {
			w.flush()
			for i := from; i < len(w.blocks); i++ {
				w.blocks[i].Background = p.BackgroundStyle
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
			bold, align := p.TextStyle.Size == "heading", p.TextAlign
			if align == "left" {
				align = ""
			}
			if w.top && (bold || align != "") {
				w.flush()
				w.elements(els)
				w.flushAs(Block{Bold: bold, Align: align})
				return
			}
			// The client draws each markdown component as a paragraph of its
			// own, so the next one never runs on from this one's last word.
			w.gap()
			w.elements(els)
			w.gap()
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

// row reads a column_set as the one line the client draws it on, which it is
// when every column holds a line of text, a set of buttons, or nothing, and
// one of them holds something.
//
// The widths are read here from the raw columns rather than through prop:
// Feishu spells them in more than one shape, and one it spells unexpectedly
// would otherwise fail the whole card rather than just the row.
func (w *writer) row(raw json.RawMessage, cols []elem) ([]Cell, bool) {
	var sizes []struct {
		Property struct {
			Width  string `json:"width"`
			Weight int    `json:"weight"`
		} `json:"property"`
	}
	if json.Unmarshal(raw, &sizes) != nil || len(sizes) != len(cols) {
		return nil, false
	}
	cells := make([]Cell, 0, len(cols))
	filled := false
	for i, col := range cols {
		sub := writer{images: w.images, people: w.people}
		sub.element(col)
		blocks := sub.done()
		if len(blocks) > 1 {
			return nil, false
		}
		var c Cell
		if len(blocks) == 1 {
			b := blocks[0]
			if b.ImageKey != "" || len(b.Row) > 0 || strings.Contains(b.Markdown, "\n") {
				return nil, false
			}
			c.Markdown, c.Buttons, filled = b.Markdown, b.Buttons, true
		}
		// A column sized in pixels is drawn at the width it needs: a terminal
		// has cells, not pixels. Only a weighted column claims a share, though
		// every column carries a weight.
		if sizes[i].Property.Width == "weighted" {
			c.Weight = max(1, sizes[i].Property.Weight)
		}
		cells = append(cells, c)
	}
	return cells, filled
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
// styles from, a line at a time.
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
	return perLine(content, opening, closing)
}

// perLine puts opening and closing round each line of content that has text,
// since the markers do not survive a line break.
func perLine(content, opening, closing string) string {
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

// coloured spells the colour a run of text is drawn in the way the card's
// markdown spells one. It stands outside any emphasis, where a markdown
// parser still reads the emphasis inside it; the default colour needs no tag.
func coloured(content, color string) string {
	if content == "" || color == "" || color == "default" {
		return content
	}
	return perLine(content, `<font color="`+color+`">`, `</font>`)
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
// languages sends them all, read in preferredLocales order.
func content(p prop) string {
	if p.Content != "" {
		return p.Content
	}
	for _, lang := range preferredLocales {
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
	if base, suffix, ok := strings.CutLast(key, "_"); ok && base != "" {
		if _, err := strconv.Atoi(suffix); err == nil {
			key = base
		}
	}
	return key
}
