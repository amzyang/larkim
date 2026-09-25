// Package card reads the interactive cards Feishu sends, straight from the
// card JSON a message carries. That JSON is the only place a card's block
// structure survives: the text the message also renders to runs block
// elements together, which glues a heading onto the list beneath it.
package card

import (
	"encoding/json"
	"strings"
)

// Block is one piece of a card body: markdown for everything a document can
// spell, and the pictures and buttons it cannot.
type Block struct {
	Markdown string
	ImageKey string
	Buttons  []string
}

// Card is an interactive message taken apart: what the Feishu client draws in
// the header band, and the body below it.
type Card struct {
	Title, Subtitle, Tags string
	Blocks                []Block
}

// Parse reads the card out of a message's raw content. It reports false for
// anything that is not a card, and for a card with nothing in it, which is
// then left to be named by its message type.
func Parse(contentRaw string) (Card, bool) {
	var env envelope
	if json.Unmarshal([]byte(contentRaw), &env) != nil || env.JSONCard == "" {
		return Card{}, false
	}
	var d doc
	if json.Unmarshal([]byte(env.JSONCard), &d) != nil {
		return Card{}, false
	}
	var c Card
	if d.Header != nil {
		p := d.Header.Property
		c.Title, c.Subtitle = plain(p.Title), plain(p.Subtitle)
		var tags []string
		for _, t := range p.TextTagList {
			if s := plain(t.Property.Text); s != "" {
				tags = append(tags, "「"+s+"」")
			}
		}
		c.Tags = strings.Join(tags, " ")
	}
	att := attachedOf(env.Attachment)
	w := writer{images: att.images, people: att.people}
	if d.Body != nil {
		w.elements(d.Body.children())
	}
	c.Blocks = w.done()
	if c.Title == "" && c.Subtitle == "" && c.Tags == "" && len(c.Blocks) == 0 {
		return Card{}, false
	}
	return c, true
}

// Markdown is the card as a document to read elsewhere: the band, the body as
// the markdown it was built from, and the buttons as their labels.
func (c Card) Markdown() string {
	var parts []string
	if head := strings.TrimSpace(strings.TrimSpace(c.Title+" "+c.Subtitle) + " " + c.Tags); head != "" {
		parts = append(parts, head)
	}
	for _, b := range c.Blocks {
		switch {
		case b.Markdown != "":
			parts = append(parts, b.Markdown)
		case len(b.Buttons) > 0:
			parts = append(parts, "["+strings.Join(b.Buttons, "] [")+"]")
		}
	}
	return strings.Join(parts, "\n\n")
}

// envelope is what an interactive message's content is: the card itself as
// embedded JSON, and the table its pictures and mentions live in.
type envelope struct {
	JSONCard   string          `json:"json_card"`
	Attachment json.RawMessage `json:"json_attachment"`
}

// doc is the card. The two card_schema versions differ only in how the body
// carries its children, which children() settles, so the version itself
// decides nothing here.
type doc struct {
	Header *elem `json:"header"`
	Body   *elem `json:"body"`
}

// elem is one node of the card tree: a tag naming what it is, and the property
// bag holding what it carries.
type elem struct {
	Tag      string `json:"tag"`
	Property prop   `json:"property"`
	// Elements is where a card_schema 1 body keeps its children: that body is
	// a bare list, with no tag or property bag around it.
	Elements []elem `json:"elements"`
}

// children is what an element holds, whichever of the two shapes it arrived
// in.
func (e elem) children() []elem {
	if len(e.Property.Elements) > 0 {
		return e.Property.Elements
	}
	return e.Elements
}

// prop is that bag. A tag reads the few fields it uses and leaves the rest
// zero; columns and actions stay raw because Feishu gives each of them two
// shapes, and only the tag says which one arrived.
type prop struct {
	Content     string                 `json:"content"`
	I18nContent map[string]string      `json:"i18nContent"`
	Elements    []elem                 `json:"elements"`
	Text        *elem                  `json:"text"`
	Title       *elem                  `json:"title"`
	Subtitle    *elem                  `json:"subtitle"`
	TextTagList []elem                 `json:"textTagList"`
	Header      *elem                  `json:"header"`
	Extra       *elem                  `json:"extra"`
	Fields      []field                `json:"fields"`
	Items       []listItem             `json:"items"`
	Level       int                    `json:"level"`
	Language    string                 `json:"language"`
	Contents    []codeLine             `json:"contents"`
	Columns     json.RawMessage        `json:"columns"`
	Rows        []map[string]tableCell `json:"rows"`
	Actions     json.RawMessage        `json:"actions"`
	URL         urlRef                 `json:"url"`
	ImageID     string                 `json:"imageID"`
	UserID      string                 `json:"userID"`
	Key         string                 `json:"key"`
	TextStyle   textStyle              `json:"textStyle"`
}

type (
	field struct {
		Text *elem `json:"text"`
	}
	urlRef struct {
		URL string `json:"url"`
	}
	textStyle struct {
		Attributes []string `json:"attributes"`
	}
	// listItem is one line of a list. Nesting is a level on the item itself,
	// not a list inside a list.
	listItem struct {
		Elements []elem `json:"elements"`
		Level    int    `json:"level"`
		Order    int    `json:"order"`
		Type     string `json:"type"`
	}
	// codeLine is one line of a code block, cut into the runs the card had it
	// coloured in.
	codeLine struct {
		Contents []struct {
			Content string `json:"content"`
		} `json:"contents"`
	}
	tableCol struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	}
	tableCell struct {
		Data json.RawMessage `json:"data"`
	}
)

// person is one of the people a card's body @s: the name the card itself
// carries, and the key that ties them to the message's own mentions, which is
// where the open id a reader knows them by lives.
type person struct {
	Name       string `json:"content"`
	MentionKey string `json:"mention_key"`
}

// attached is the table beside a card body: the pictures it names by id, and
// the people it @s by an id of the sending app's own.
type attached struct {
	images map[string]string
	people map[string]person
}

// attachedOf reads that table. Feishu sends it either inline or as JSON of its
// own.
func attachedOf(raw json.RawMessage) attached {
	if len(raw) == 0 {
		return attached{}
	}
	if raw[0] == '"' {
		var embedded string
		if json.Unmarshal(raw, &embedded) != nil {
			return attached{}
		}
		raw = json.RawMessage(embedded)
	}
	var att struct {
		Images map[string]struct {
			OriginKey string `json:"origin_key"`
		} `json:"images"`
		AtUsers map[string]person `json:"at_users"`
	}
	if json.Unmarshal(raw, &att) != nil {
		return attached{}
	}
	out := attached{images: make(map[string]string, len(att.Images)), people: att.AtUsers}
	for id, img := range att.Images {
		out.images[id] = img.OriginKey
	}
	return out
}
