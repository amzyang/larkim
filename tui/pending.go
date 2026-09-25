package tui

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// pendingText stands in for a message lark-cli has not rendered yet. A body
// arrives one sync step before its rendering and, in a backfilled range, can
// wait minutes behind the render queue, so the raw OpenAPI JSON would be a
// common sight. Text and a post carry their own words, which read fine with
// the mention placeholders still in them once the paragraphs a rich-text body
// arrives in are flattened; anything else is named by its type until the
// rendering lands and replaces this.
func pendingText(msgType, contentRaw string) string {
	switch msgType {
	case "text":
		var v struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(contentRaw), &v) == nil && v.Text != "" {
			return sync.UnwrapParagraphs(v.Text)
		}
	case "post":
		text, pics := postText(contentRaw)
		switch {
		case text != "":
			return text
		case pics:
			return msgTypeLabel("image")
		}
	}
	return msgTypeLabel(msgType)
}

// postElem is the one element shape a rich-text body uses: every tag that
// carries words carries them in text, and an at carries the name besides.
type postElem struct {
	Tag       string `json:"tag"`
	Text      string `json:"text"`
	UserName  string `json:"user_name"`
	EmojiType string `json:"emoji_type"`
}

type postBody struct {
	Title   string       `json:"title"`
	Content [][]postElem `json:"content"`
}

// postText reads the words a rich-text body carries, paragraph per line, and
// whether it carries pictures besides. The rendering that replaces it brings
// the markdown, the pictures and the formatting dropped here; this is what the
// post can say before it lands. A post of pictures alone has no words to
// return, and the picture names it better than the type does.
func postText(contentRaw string) (text string, pics bool) {
	body, ok := postBodyOf(contentRaw)
	if !ok {
		return "", false
	}
	lines := make([]string, 0, len(body.Content)+1)
	if body.Title != "" {
		lines = append(lines, body.Title)
	}
	for _, para := range body.Content {
		var line strings.Builder
		for _, el := range para {
			switch el.Tag {
			case "img":
				// A picture is placed by the rendering, which is also what
				// sizes it; all this path can say is that one is coming.
				pics = true
			case "media", "hr":
				// A clip is placed by the rendering too, and a rule is not words.
			case "at":
				line.WriteString("@" + el.UserName)
			case "emotion":
				// The bracketed spelling is the one expandEmoji resolves.
				line.WriteString("[" + el.EmojiType + "]")
			default:
				line.WriteString(el.Text)
			}
		}
		lines = append(lines, line.String())
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), pics
}

// postBodyOf reads a body in either form the wire uses: the bare
// {title, content} the API returns, and the locale-wrapped {zh_cn: {...}}
// lark-cli sends, which is how a post larkim sent itself reads back.
func postBodyOf(contentRaw string) (postBody, bool) {
	var b postBody
	if json.Unmarshal([]byte(contentRaw), &b) == nil && (b.Content != nil || b.Title != "") {
		return b, true
	}
	var byLocale map[string]postBody
	if json.Unmarshal([]byte(contentRaw), &byLocale) != nil {
		return postBody{}, false
	}
	for _, loc := range slices.Sorted(maps.Keys(byLocale)) {
		if b := byLocale[loc]; b.Content != nil || b.Title != "" {
			return b, true
		}
	}
	return postBody{}, false
}

// pendingSummary is pendingText for a chat's last message, on one line the
// way a rendered summary is.
func pendingSummary(c store.Chat) string {
	return flatten(expandEmoji(pendingText(c.LastMsgType, c.LastContentRaw)))
}
