package tui

import (
	"strings"

	"github.com/amzyang/larkim/emoji"
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

// emojiIcon is an emoji as the icon column draws it: the client's own
// picture, with a bare dot standing in where the terminal draws none. A
// Unicode row carries a character rather than a key, and the character is
// what it draws — it is what accepting the row writes, where a built-in goes
// to Feishu as its key.
func emojiIcon(e emoji.Emoji) offerIcon {
	if e.Rect[2] == 0 {
		return offerIcon{text: e.Glyph}
	}
	return offerIcon{text: stDim.Render("·"), image: emoji.Picture("", e.Key)}
}
