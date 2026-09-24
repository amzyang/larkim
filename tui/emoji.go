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

// expandEmoji draws Feishu's official emoji. A key the terminal has no glyph
// for stays as it came, since the Feishu client draws it as a picture and has
// no bracketed spelling to fall back on either.
func expandEmoji(s string) string {
	if strings.Contains(s, ":") {
		s = drawEmoji(shortcode, s, emoji.ByKey)
	}
	if !strings.Contains(s, "[") {
		return s
	}
	return drawEmoji(bracketName, s, emoji.ByName)
}

// drawEmoji swaps every spelling re matches for the glyph lookup finds under
// it, stripping the delimiters with it. A spelling that names no emoji, or one
// no character carries, stays as it came.
func drawEmoji(re *regexp.Regexp, s string, lookup func(string) (emoji.Emoji, bool)) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		if e, ok := lookup(m[1 : len(m)-1]); ok && e.Glyph != "" {
			return e.Glyph
		}
		return m
	})
}
