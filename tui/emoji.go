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

// emojiSpell is the set of spellings a body draws an official emoji from.
//
// Neither spelling is a spelling everywhere. Feishu keeps a post's text
// verbatim: a paragraph somebody typed "[赞]" or ":DONE:" into arrives as
// those characters and the client draws them as those characters, an emoji in
// a post coming from an emotion element alone. Drawing every spelling
// everywhere had larkim show an emoji where the client shows brackets, which
// is the one thing a reader cannot check without leaving larkim.
type emojiSpell uint8

const (
	spellNone      emojiSpell = 0
	spellShortcode emojiSpell = 1 << iota // :KEY:, how larkim writes an emotion element back
	spellBracket                          // [Name], how a plain text message carries one
	spellAll       = spellShortcode | spellBracket
)

// spellOf is the spellings a message of this type draws an emoji from. A post
// larkim holds only the flattened rendering of carries its emotion elements as
// the shortcodes sync wrote them; a text message carries the client's bracketed
// name. Anything else — a card, an attachment, a type larkim has no reading of
// — keeps both, there being no evidence about how the client spells those.
func spellOf(msgType string) emojiSpell {
	switch msgType {
	case "post":
		return spellShortcode
	case "text":
		return spellBracket
	}
	return spellAll
}

// expandEmoji draws Feishu's official emoji, in the spellings sp allows. A key
// the terminal has no glyph for stays as it came, since the Feishu client draws
// it as a picture and has no bracketed spelling to fall back on either.
func expandEmoji(s string, sp emojiSpell) string {
	if sp == spellNone {
		return s
	}
	if sp&spellShortcode != 0 && strings.Contains(s, ":") {
		s = drawEmoji(shortcode, s, emoji.ByKey)
	}
	if sp&spellBracket == 0 || !strings.Contains(s, "[") {
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

// emojiKey is the key Feishu speaks, where it says something the name does not.
func emojiKey(e emoji.Emoji) string {
	if strings.EqualFold(e.Key, e.Name()) {
		return ""
	}
	return e.Key
}

// emojiTerm is the term a query landed on, marked, where it spells neither the
// key nor the name. It says which spelling answered — which pinyin, which
// alias — because otherwise a hit reached that way looks arbitrary. The
// character is not one of them: it is already drawn in the icon column, so
// naming it would repeat what is on screen.
func emojiTerm(e emoji.Emoji, term string, pos []int) string {
	if term == "" || term == e.Glyph || strings.EqualFold(term, e.Name()) || strings.EqualFold(term, e.Key) {
		return ""
	}
	return markMatch(term, pos)
}

// emojiInfo is what the box beside a completion list says about the emoji the
// cursor is on: the key Feishu speaks and the term a query landed on, which a
// list row has no room for.
func emojiInfo(h emoji.Hit) []string {
	return infoLines(emojiKey(h.Emoji), emojiTerm(h.Emoji, h.Term, h.Positions))
}

// emojiIcon is an emoji as the icon column draws it: the character where one
// carries the same feeling, the client's own picture where none does, and a
// bare dot where the pictures were never cut out.
func emojiIcon(e emoji.Emoji) offerIcon {
	if e.Glyph != "" {
		return offerIcon{text: e.Glyph}
	}
	return offerIcon{text: stDim.Render("·"), image: emoji.Picture("", e.Key)}
}
