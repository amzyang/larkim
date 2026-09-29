package sync

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// PostElem is one element of a rich-text body, with the fields every tag
// uses. Every tag keeps the fields it arrived with rather than a spelling of
// them: flattening the body to markdown makes an asterisk somebody typed and
// one the flattening added the same character, which is a reading a drawn
// body must not be built on.
type PostElem struct {
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

// PostBody is a rich-text body: a title, and the paragraphs below it.
// content_v2 is the paragraph list the client writes now and content the older
// spelling of the same body, so the two are never merged — whichever is read
// is read whole.
type PostBody struct {
	Title     string       `json:"title"`
	ContentV2 [][]PostElem `json:"content_v2"`
	Content   [][]PostElem `json:"content"`
	Files     []postFile   `json:"files"`
}

// postFile is one entry of the attachment zone a post carries beside its
// paragraphs.
type postFile struct {
	FileKey  string `json:"file_key"`
	FileName string `json:"file_name"`
	IsFolder bool   `json:"is_folder"`
}

// Paragraphs is whichever of the two spellings this body arrived in.
func (b PostBody) Paragraphs() [][]PostElem {
	if len(b.ContentV2) > 0 {
		return b.ContentV2
	}
	return b.Content
}

// postText renders a rich-text message. The shape is lark-cli's, to the
// character: the @ runs, the picture references and the attachment tags are
// all read back out of a rendering by matching them, so a spelling of larkim's
// own would go unread.
func postText(contentRaw, mentionsJSON string) string {
	if !json.Valid([]byte(contentRaw)) {
		return "[Invalid rich text JSON]"
	}
	b := postBodyIn(contentRaw)
	var parts []string
	if b.Title != "" {
		parts = append(parts, b.Title)
	}
	for _, para := range b.Paragraphs() {
		var line strings.Builder
		for _, el := range para {
			line.WriteString(postElemText(el))
		}
		parts = append(parts, line.String())
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if out == "" {
		out = "[Rich text message]"
	}
	out = ResolveMentions(out, mentionsJSON)
	// The attachment zone is written under the words, in the same tag form a
	// standalone file message renders into.
	var files []string
	for _, f := range b.Files {
		if f.FileKey == "" {
			continue
		}
		tag := "file"
		if f.IsFolder {
			tag = "folder"
		}
		files = append(files, keyTag(tag, f.FileKey, f.FileName))
	}
	if len(files) > 0 {
		out += "\n" + strings.Join(files, "\n")
	}
	return out
}

// postBodyIn reads the body out of the two shapes the wire uses: the bare
// {title, content} the API returns, and the locale-wrapped {zh_cn: {...}} a
// post larkim sent itself reads back as.
func postBodyIn(contentRaw string) PostBody {
	var probe map[string]json.RawMessage
	if json.Unmarshal([]byte(contentRaw), &probe) != nil {
		return PostBody{}
	}
	var b PostBody
	_, bare := probe["content"]
	if _, ok := probe["title"]; ok || bare {
		json.Unmarshal([]byte(contentRaw), &b)
		return b
	}
	// A locale key, the three the client writes first and the rest in sorted
	// order, so the same body always reads the same way.
	for _, k := range append([]string{"zh_cn", "en_us", "ja_jp"}, slices.Sorted(maps.Keys(probe))...) {
		if v, ok := probe[k]; ok && json.Unmarshal(v, &b) == nil {
			return b
		}
	}
	return PostBody{}
}

// postElemText renders one element to its inline form.
func postElemText(el PostElem) string {
	switch el.Tag {
	case "text":
		return StyleMarkdown(el.Text, el.Style)
	case "a":
		switch {
		case el.Href != "" && el.Text != "":
			return StyleMarkdown(fmt.Sprintf("[%s](%s)", escapeMDLinkText(el.Text), el.Href), el.Style)
		case el.Href != "":
			return StyleMarkdown(el.Href, el.Style)
		}
		return StyleMarkdown(el.Text, el.Style)
	case "at":
		return StyleMarkdown(atRunText(el), el.Style)
	case "emotion":
		// Deliberately unstyled: a shortcode is an atomic token, not prose.
		if el.EmojiType == "" {
			return ""
		}
		return ":" + el.EmojiType + ":"
	case "md":
		return el.Text
	case "img":
		if el.ImageKey != "" {
			return fmt.Sprintf("![Image](%s)", el.ImageKey)
		}
		return "[Image]"
	case "media":
		if el.FileKey != "" {
			return fmt.Sprintf("[Media: %s]", el.FileKey)
		}
		return "[Media]"
	case "code_block":
		if el.Language != "" {
			return fmt.Sprintf("\n```%s\n%s\n```\n", el.Language, el.Text)
		}
		return fmt.Sprintf("\n```\n%s\n```\n", el.Text)
	case "hr":
		return "\n---\n"
	}
	return el.Text
}

// atRunText spells one mention the way a rendering carries it, which is the
// form the @ runs are read back out of a body by.
func atRunText(el PostElem) string {
	if el.UserID == "@_all" || el.UserID == "all" {
		return `<at user_id="all"></at>`
	}
	if el.UserName == "" {
		return "@" + el.UserID
	}
	if strings.HasPrefix(el.UserID, "ou") {
		return fmt.Sprintf(`<at user_id="%s">%s</at>`, el.UserID, el.UserName)
	}
	return "@" + el.UserName
}

// StyleMarkdown wraps text in the markup an element's style names, innermost
// first, so the same styles always come out spelled the same way. Feishu keeps
// emphasis as names beside the words rather than as markup around them, and
// this is what spells it back.
func StyleMarkdown(text string, styles []string) string {
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

func escapeMDLinkText(s string) string {
	return strings.NewReplacer(`[`, `\[`, `]`, `\]`).Replace(s)
}
