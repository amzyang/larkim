package emoji

//go:generate go run ./internal/genunicode

import (
	"sync"
)

// Unicode is the Unicode emoji beside Feishu's own: what a draft may carry as
// a plain character where Feishu has no emoji of its own for it. Every one of
// them is NoReaction, because Feishu takes only its own keys as a reaction.
//
// A row is a different thing from a built-in emoji, not a second spelling of
// one: the character is the payload a draft writes, where a built-in goes to
// Feishu as its key and is drawn as its picture. The two are listed side by
// side without deduplication — what matches a built-in is its own picture,
// what matches a Unicode row is the character. Their Order continues past the
// client's own panel, so an unqueried composer offers Feishu's first.
var Unicode = sync.OnceValue(func() []Emoji {
	out := make([]Emoji, 0, len(unicodeTable))
	for _, e := range unicodeTable {
		if _, taken := ByKey(e.Key); taken {
			continue
		}
		e.Order, e.NoReaction = len(table)+2+len(out), true
		out = append(out, e)
	}
	return out
})
