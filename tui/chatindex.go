package tui

import (
	"strings"

	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/store"
)

// chatIndex answers the `/` filter. A name contributes itself, its pinyin and
// its initials, so a list of Chinese names is reachable without leaving the
// home row.
type chatIndex struct{ ix *fuzzy.Index }

func newChatIndex() *chatIndex { return &chatIndex{ix: fuzzy.NewIndex()} }

// match reports whether a chat answers the query, and which runes of its name
// the query landed on. Positions are empty unless the name itself matched:
// underlining name runes at pinyin offsets would point at the wrong
// characters, so a hit reached through pinyin marks nothing.
func (c *chatIndex) match(ch store.Chat, query string) (pos []int, ok bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, true
	}
	// An id is reached by the piece of it one remembers. Fuzzy over a hex
	// string would match almost anything, so it stays a plain substring.
	if strings.Contains(ch.ChatID, strings.ToLower(query)) {
		return nil, true
	}
	// Spell the name the reader sees: flatten collapses whitespace runs, so
	// matching the raw name would hand back positions that point past the
	// characters on screen.
	return c.ix.Match(ch.ChatID, flatten(ch.Name), query)
}
