package emoji

//go:generate go run ./internal/genunicode

import (
	"strings"
	"sync"
)

// Unicode is the Unicode emoji beside Feishu's own: what a draft may carry as a
// plain character where Feishu has no emoji of its own for it. Every one of them
// is NoReaction, because Feishu takes only its own keys as a reaction.
//
// A character one of Feishu's emoji already draws is left to that emoji, which
// can also be a reaction and which the reader knows by Feishu's name for it, so
// that one character still answers with one emoji. Their Order continues past
// the client's own panel, so an unqueried composer offers Feishu's first.
var Unicode = sync.OnceValue(func() []Emoji {
	owned := map[string]bool{}
	for _, e := range All() {
		// A bare spelling carries no terms and is never offered, so the
		// character it spells is still free for a row a query can reach.
		if e.Glyph != "" && len(e.Terms) > 0 {
			owned[bare(e.Glyph)] = true
		}
	}
	out := make([]Emoji, 0, len(unicodeTable))
	for _, e := range unicodeTable {
		if owned[bare(e.Glyph)] {
			continue
		}
		if _, taken := ByKey(e.Key); taken {
			continue
		}
		e.Order, e.NoReaction = len(table)+2+len(out), true
		out = append(out, e)
	}
	return out
})

// bare drops the variation selector, which Feishu's glyphs and Unicode's data
// spell inconsistently: ✌️ and ✌ are one character to a reader.
func bare(s string) string { return strings.ReplaceAll(s, "️", "") }
