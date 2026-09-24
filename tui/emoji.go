package tui

import (
	"regexp"
	"strings"

	"github.com/amzyang/larkim/emoji"
)

// Feishu spells an official emoji two ways, and both reach the rendered text:
// a rich-text post carries an emotion element, which lark-cli writes as the
// emoji_type key between colons, while a plain text message carries the
// emoji's display name in brackets, in whichever language the sender's client
// was set to. Anything else shaped like either — a card icon name, a clock
// time, a bracketed noun — is left alone by expandEmoji.
var (
	shortcode   = regexp.MustCompile(`:([A-Za-z0-9_]{1,32}):`)
	bracketName = regexp.MustCompile(`\[([^\[\]\n]{1,12})\]`)
)

// expandEmoji draws Feishu's official emoji. A key with no glyph of its own
// becomes its bracketed name, the way the Feishu client writes an emoji it
// cannot draw inline, and anything that is not an official emoji is left
// exactly as it came.
func expandEmoji(s string) string {
	if strings.Contains(s, ":") {
		s = shortcode.ReplaceAllStringFunc(s, func(m string) string {
			name := m[1 : len(m)-1]
			e, ok := emoji.ByKey(name)
			switch {
			case !ok:
				return m
			case e.Glyph == "":
				return stDim.Render("[" + name + "]")
			default:
				return e.Glyph
			}
		})
	}
	if !strings.Contains(s, "[") {
		return s
	}
	return bracketName.ReplaceAllStringFunc(s, func(m string) string {
		if e, ok := emoji.ByName(m[1 : len(m)-1]); ok && e.Glyph != "" {
			return e.Glyph
		}
		return m
	})
}
