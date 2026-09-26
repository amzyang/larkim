// Package emoji is larkim's single source of truth for Feishu's emoji: the
// emoji_type keys the API speaks, the names a message spells them as, the
// Unicode a terminal can draw, and the search terms a picker matches against.
//
// Everything but the Unicode column is generated from the installed Lark
// client's own offline assets; see internal/gen.
package emoji

//go:generate go run ./internal/gen

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"sync"
)

// Emoji is one of Feishu's emoji.
type Emoji struct {
	// Key is the emoji_type the API speaks. It is case-sensitive there, and
	// spelled every which way: OK, BubbleTea, Status_PrivateMessage, 18X.
	Key string
	// Glyph is the Unicode this terminal can draw in its place, or "" when no
	// character carries the same feeling and a name or picture has to stand in.
	Glyph string
	// ZH and EN are the names the client displays, which are also what a text
	// message carries between brackets.
	ZH, EN string
	// Rect is this emoji's rectangle in the client's sprite sheet, as
	// x, y, width, height. Sync cuts the picture out of it.
	Rect [4]int
	// Terms is what a picker query is matched against: the Chinese names, their
	// pinyin and pinyin initials, the aliases, the English name and the key.
	Terms []string
	// Order is the emoji's place in the client's own panel, which is the order
	// a picker falls back to when nothing has been typed.
	Order int
	// NoReaction marks an emoji Feishu refuses as a reaction however it reaches
	// this client: another tenant's culture emoji, the ones the client has
	// withdrawn, and the Unicode ones in common.go, which are characters a
	// message carries rather than keys the reaction API knows.
	NoReaction bool
	// Delisted marks an emoji the client has withdrawn. It is the one kind of
	// NoReaction a message cannot carry either: named inside one it reaches
	// the other side as "[Sensitive emoji]", so of the two ways an emoji
	// travels — as itself and as the picture it is drawn with — only the
	// picture is left.
	Delisted bool
}

// index is the table joined to its Unicode column, with the spellings that
// carry a glyph but no table entry of their own added on the end.
var index = sync.OnceValue(func() []Emoji {
	out := make([]Emoji, 0, len(table)+len(glyphs))
	for _, e := range table {
		e.Glyph = glyphs[Fold(e.Key)]
		out = append(out, e)
	}
	for key, glyph := range glyphs {
		if !slices.ContainsFunc(out, func(e Emoji) bool { return Fold(e.Key) == key }) {
			out = append(out, Emoji{Key: key, Glyph: glyph, Order: len(table) + 1})
		}
	}
	slices.SortFunc(out, func(a, b Emoji) int { return strings.Compare(a.Key, b.Key) })
	return out
})

// byKey and byName are the lookups over index, built once alongside it.
var (
	byKey = sync.OnceValue(func() map[string]Emoji {
		m := make(map[string]Emoji, len(index()))
		for _, e := range index() {
			m[Fold(e.Key)] = e
		}
		return m
	})
	byName = sync.OnceValue(func() map[string]Emoji {
		m := make(map[string]Emoji, 2*len(index()))
		for _, e := range index() {
			for _, name := range []string{e.ZH, e.EN} {
				if name != "" {
					m[name] = e
				}
			}
		}
		return m
	})
)

// All is every emoji, ordered by key. It includes the spellings that carry a
// glyph but nothing else, because a message's text can name one; a picker
// wants Reactable instead.
func All() []Emoji { return index() }

// Reactable reports whether this emoji may be put on a message as a reaction.
// Three kinds may not: another tenant's culture emoji and the ones the client
// has withdrawn, both of which Feishu rejects outright, and the bare spellings
// that live in glyphs.go alone, which the client never offers and which would
// land on a message as a reaction nobody can draw.
func (e Emoji) Reactable() bool { return e.EN != "" && !e.NoReaction }

// Offerable reports whether the picker lists this emoji at all. Everything the
// client names is offered, reaction or not: one Feishu refuses as a reaction
// still reaches the other side as the picture the client draws it with, which
// is the only way it reaches them. The bare spellings are left out — they have
// no name to search by and no rectangle to cut a picture from.
func (e Emoji) Offerable() bool { return e.EN != "" }

// Name is what this emoji is called on screen and between the brackets a
// message carries it in: the English name, because the client here runs in
// English and every client's table holds both languages' names. A bare
// spelling from glyphs.go has no name of its own and stands for itself.
func (e Emoji) Name() string { return cmp.Or(e.EN, e.Key) }

// Fold puts the spellings of one emoji on a single lookup key: the client
// sends both the bare `Rose` and the `Lark_Emoji_Rose_0` form.
func Fold(s string) string {
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "LARK_EMOJI_")
	if i := strings.LastIndex(s, "_"); i > 0 && strings.Trim(s[i+1:], "0123456789") == "" {
		s = s[:i]
	}
	return s
}

// ByKey resolves an emoji_type, however it is spelled. A skin-tone spelling
// resolves to the emoji it is a tone of: the client offers only the default,
// but Feishu takes every tone as a reaction, so one arrives under a key no
// picker ever showed and there is no picture or name of its own to draw.
func ByKey(key string) (Emoji, bool) {
	folded := Fold(key)
	if e, ok := byKey()[folded]; ok {
		return e, true
	}
	e, ok := byKey()[toneVariants[folded]]
	return e, ok
}

// ByName resolves the bracketed form a text message carries, which is a
// display name in whichever language the sender's client was set to — or, on a
// client that has no name for it, the key itself.
func ByName(name string) (Emoji, bool) {
	if e, ok := byName()[name]; ok {
		return e, true
	}
	return ByKey(name)
}

// Chip is one emoji's standing on a message: how many people reacted with it,
// who they were, and whether the reader is one of them.
type Chip struct {
	Key   string
	Count int
	Mine  bool
	// Operators are the reactors Feishu named, earliest first. Only one page
	// of details comes back, so a busy emoji's later reactors are missing
	// from it and Count stays the only full total.
	Operators []string
}

// reactionBlock is what lark-cli attaches to a message it pulled. count is a
// string over the wire, which is why it is read as a number rather than an int.
type reactionBlock struct {
	Counts []struct {
		ReactionType string      `json:"reaction_type"`
		Count        json.Number `json:"count"`
	} `json:"counts"`
	Details []struct {
		EmojiType string `json:"emoji_type"`
		Operator  struct {
			OperatorID   string `json:"operator_id"`
			OperatorType string `json:"operator_type"`
		} `json:"operator"`
		// ActionTime is when this reaction landed, in Unix seconds, and comes
		// over the wire as a string.
		ActionTime json.Number `json:"action_time"`
	} `json:"details"`
}

// Summary reads a message's stored reaction block. Feishu sends the totals and
// the individual reactions separately, and only the latter say who reacted, so
// the reactors and the reader's own mark are read off the details while the
// count stays the server's — the details are one page and may not hold every
// reactor.
//
// The chips come back in the order the client shows them, which is when each
// emoji was first put on the message rather than the alphabetical order the
// totals arrive in. It is an order that never reshuffles as the counts move.
//
// A block that does not decode yields nothing rather than a guess.
func Summary(reactionsJSON, selfOpenID string) []Chip {
	if reactionsJSON == "" {
		return nil
	}
	var blk reactionBlock
	if json.Unmarshal([]byte(reactionsJSON), &blk) != nil {
		return nil
	}
	type reaction struct {
		operator string
		at       int64
	}
	by := map[string][]reaction{}
	for _, d := range blk.Details {
		at, _ := d.ActionTime.Int64()
		by[d.EmojiType] = append(by[d.EmojiType], reaction{d.Operator.OperatorID, at})
	}
	for _, rs := range by {
		slices.SortStableFunc(rs, func(a, b reaction) int { return cmp.Compare(a.at, b.at) })
	}
	// An emoji nobody on the page reacted with has no time to sort by, and
	// keeps the place the totals gave it at the end of the strip.
	first := func(key string) int64 {
		if rs := by[key]; len(rs) > 0 {
			return rs[0].at
		}
		return math.MaxInt64
	}
	chips := make([]Chip, 0, len(blk.Counts))
	for _, c := range blk.Counts {
		n, err := c.Count.Int64()
		if c.ReactionType == "" || err != nil || n <= 0 {
			continue
		}
		chip := Chip{Key: c.ReactionType, Count: int(n)}
		for _, r := range by[c.ReactionType] {
			chip.Operators = append(chip.Operators, r.operator)
			chip.Mine = chip.Mine || (selfOpenID != "" && r.operator == selfOpenID)
		}
		chips = append(chips, chip)
	}
	slices.SortStableFunc(chips, func(a, b Chip) int { return cmp.Compare(first(a.Key), first(b.Key)) })
	return chips
}
