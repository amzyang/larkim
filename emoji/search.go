package emoji

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

// Hit is one emoji a query matched, and where in its name it matched, so the
// picker can show the reader why this one came back.
type Hit struct {
	Emoji Emoji
	// Term is the search term that matched — the Chinese name, its pinyin, an
	// alias — and Positions are the runes of it the query landed on.
	Term      string
	Positions []int
	score     int
}

// Index is a prepared set of emoji to search. Building it once keeps the
// per-keystroke work to the matching itself.
type Index struct {
	items []Emoji
	terms [][]util.Chars
	slab  *util.Slab
	// recent is the emoji used most lately, newest first, which is what an
	// empty query answers with: a person reaches for the same handful all day.
	recent []string
}

// NewReactionIndex prepares the emoji that may be put on a message.
func NewReactionIndex() *Index { return NewIndex(Emoji.Reactable) }

// NewIndex prepares the emoji that pass keep. A nil keep takes all of them.
func NewIndex(keep func(Emoji) bool) *Index {
	ix := &Index{slab: util.MakeSlab(slabSize, slabSize)}
	for _, e := range All() {
		if keep != nil && !keep(e) {
			continue
		}
		chars := make([]util.Chars, 0, len(e.Terms))
		for _, t := range e.Terms {
			chars = append(chars, util.ToChars([]byte(t)))
		}
		ix.items = append(ix.items, e)
		ix.terms = append(ix.terms, chars)
	}
	return ix
}

// slabSize is fzf's own scratch space for one match. The longest term here is
// a few dozen bytes, so this is already far more than the matcher can use.
const slabSize = 4096

// Len is how many emoji the index holds, which is the denominator the picker
// shows beside the hit count.
func (ix *Index) Len() int { return len(ix.items) }

// Use records that an emoji was just chosen, so it leads the next empty query.
func (ix *Index) Use(key string) {
	key = Fold(key)
	ix.recent = slices.DeleteFunc(ix.recent, func(k string) bool { return k == key })
	ix.recent = append([]string{key}, ix.recent...)
	if len(ix.recent) > MaxRecent {
		ix.recent = ix.recent[:MaxRecent]
	}
}

// Recent is the emoji chosen lately, newest first.
func (ix *Index) Recent() []string { return slices.Clone(ix.recent) }

// SetRecent replaces the recent list, dropping anything this index does not
// hold — the list outlives the table, and an emoji can leave it.
func (ix *Index) SetRecent(keys []string) {
	ix.recent = nil
	for _, k := range keys {
		if slices.ContainsFunc(ix.items, func(e Emoji) bool { return Fold(e.Key) == Fold(k) }) {
			ix.recent = append(ix.recent, Fold(k))
		}
	}
	if len(ix.recent) > MaxRecent {
		ix.recent = ix.recent[:MaxRecent]
	}
}

// MaxRecent bounds the remembered list. Past a screenful of them the ordering
// stops being something a person can hold in their head anyway.
const MaxRecent = 30

// Search ranks the emoji against a query, best first. An empty query answers
// with the recently used ones and then the client's own panel order, which is
// what the reader sees in Feishu itself.
func (ix *Index) Search(query string) []Hit {
	query = strings.TrimSpace(query)
	if query == "" {
		return ix.unqueried()
	}
	// Smart case, the way fzf and ripgrep read a query: a lowercase one asks
	// about neither case, a query with a capital in it means that capital.
	sensitive := strings.ToLower(query) != query
	pattern := []rune(query)
	var hits []Hit
	for i, e := range ix.items {
		best := Hit{}
		for j, term := range ix.terms[i] {
			res, pos := algo.FuzzyMatchV2(sensitive, true, true, &term, pattern, true, ix.slab)
			if res.Score <= 0 || res.Score <= best.score {
				continue
			}
			best = Hit{Emoji: e, Term: e.Terms[j], Positions: sorted(pos), score: res.Score}
		}
		if best.score > 0 {
			hits = append(hits, best)
		}
	}
	slices.SortStableFunc(hits, func(a, b Hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return ix.rank(a.Emoji) - ix.rank(b.Emoji)
	})
	return hits
}

// unqueried is what an empty query answers with.
func (ix *Index) unqueried() []Hit {
	hits := make([]Hit, 0, len(ix.items))
	for _, e := range ix.items {
		hits = append(hits, Hit{Emoji: e, Term: e.ZH})
	}
	slices.SortStableFunc(hits, func(a, b Hit) int { return ix.rank(a.Emoji) - ix.rank(b.Emoji) })
	return hits
}

// rank orders two emoji that a query cannot tell apart: the recently used
// first, in the order they were used, then the client's own panel order.
func (ix *Index) rank(e Emoji) int {
	if i := slices.Index(ix.recent, Fold(e.Key)); i >= 0 {
		return i - len(ix.recent)
	}
	return e.Order
}

func sorted(pos *[]int) []int {
	if pos == nil {
		return nil
	}
	out := slices.Clone(*pos)
	slices.Sort(out)
	return out
}

// recentFile is where the picker's remembered list lives. It is derived data:
// deleting it costs the ordering of an empty query and nothing else.
const recentFile = "emoji-recent.json"

// LoadRecent fills the index's remembered list from the data dir. A file that
// is missing or unreadable leaves the list empty, which is the state a first
// run is in anyway.
func (ix *Index) LoadRecent(dataDir string) {
	b, err := os.ReadFile(filepath.Join(dataDir, recentFile))
	if err != nil {
		return
	}
	var keys []string
	if json.Unmarshal(b, &keys) == nil {
		ix.SetRecent(keys)
	}
}

// SaveRecent writes the remembered list back.
func (ix *Index) SaveRecent(dataDir string) error {
	b, err := json.Marshal(ix.recent)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, recentFile), b, 0o600)
}
