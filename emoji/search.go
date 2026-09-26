package emoji

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amzyang/larkim/fuzzy"
	"github.com/junegunn/fzf/src/util"
)

// Hit is one emoji a query matched, and where in its name it matched, so the
// picker can show the reader why this one came back.
type Hit struct {
	Emoji Emoji
	// Term is the search term that matched — a Chinese name, its pinyin, an
	// alias — and Positions are the runes of it the query landed on. Both are
	// empty when nothing was typed and the whole panel came back.
	Term      string
	Positions []int
	score     int
}

// Index is a prepared set of emoji to search. Building it once keeps the
// per-keystroke work to the matching itself.
type Index struct {
	items []Emoji
	terms [][]util.Chars
	mt    *fuzzy.Matcher
	// used is the emoji this reader reaches for, most used first, which is
	// what an empty query answers with: a person works out of the same handful
	// all day.
	used []usage
	// file is where this index's list lives. Two indexes sharing one file
	// would each drop the other's keys through setUsed, which keeps only what
	// the index itself holds.
	file string
}

// usage is how often one emoji has been reached for. The list it lives in is
// held in rank order, so the file it is written to reads back as the ranking
// without sorting it again — and a tie between two counts keeps whichever was
// used later in front, which no count of its own could say.
type usage struct {
	Key string `json:"k"`
	N   int    `json:"n"`
}

// NewReactionIndex prepares the emoji the picker offers. It is more than the
// reactions: the ones Feishu refuses as a reaction are chosen the same way and
// go on the conversation as a picture instead, so leaving them out of the
// search would put them out of reach entirely.
func NewReactionIndex() *Index { return newIndex(reactionUsedFile, All(), Emoji.Offerable) }

// NewComposerIndex prepares the emoji a draft may carry: Feishu's own, and the
// Unicode ones it has no answer for. Everything here is either a character any
// terminal draws or a name a Feishu text message spells, so the composer can
// offer more than the reaction picker may.
func NewComposerIndex() *Index {
	return newIndex(composerUsedFile, slices.Concat(All(), Common()), nil)
}

// newIndex prepares the items that pass keep. A nil keep takes all of them.
func newIndex(file string, items []Emoji, keep func(Emoji) bool) *Index {
	ix := &Index{mt: fuzzy.NewMatcher(), file: file}
	for _, e := range items {
		if keep != nil && !keep(e) {
			continue
		}
		ix.items = append(ix.items, e)
		ix.terms = append(ix.terms, fuzzy.Chars(e.Terms))
	}
	return ix
}

// Len is how many emoji the index holds, which is the denominator the picker
// shows beside the hit count.
func (ix *Index) Len() int { return len(ix.items) }

// Use records that an emoji was just chosen and moves it to the head of the
// emoji reached for as often as it now has been.
//
// Counting rather than remembering the order: one reach for something rare
// would otherwise park it in front of the handful a reader works out of for
// the rest of the day, and a picker whose first row moves is one nobody can
// press without reading it.
func (ix *Index) Use(key string) {
	key = Fold(key)
	n := 1
	if i := slices.IndexFunc(ix.used, func(u usage) bool { return u.Key == key }); i >= 0 {
		n = ix.used[i].N + 1
		ix.used = slices.Delete(ix.used, i, i+1)
	}
	at := slices.IndexFunc(ix.used, func(u usage) bool { return u.N <= n })
	if at < 0 {
		at = len(ix.used)
	}
	ix.used = slices.Insert(ix.used, at, usage{Key: key, N: n})
	ix.trim()
}

// Used is the emoji this reader reaches for, most used first.
func (ix *Index) Used() []string {
	out := make([]string, 0, len(ix.used))
	for _, u := range ix.used {
		out = append(out, u.Key)
	}
	return out
}

// UsedLen is how many emoji this reader has reached for, which is what tells a
// picker whether its unnarrowed answer is this reader's own habits or the
// client's panel order.
func (ix *Index) UsedLen() int { return len(ix.used) }

// setUsed replaces the list, dropping anything this index does not hold — the
// list outlives the table, and an emoji can leave it. The order it arrives in
// is the ranking, which is the order it was written in.
func (ix *Index) setUsed(list []usage) {
	ix.used = nil
	for _, u := range list {
		k := Fold(u.Key)
		if u.N > 0 && slices.ContainsFunc(ix.items, func(e Emoji) bool { return Fold(e.Key) == k }) {
			ix.used = append(ix.used, usage{Key: k, N: u.N})
		}
	}
	ix.trim()
}

// trim drops the tail: the least used, and among those the longest since used.
func (ix *Index) trim() {
	if len(ix.used) > MaxUsed {
		ix.used = ix.used[:MaxUsed]
	}
}

// MaxUsed bounds the remembered list. Past a screenful of them the ordering
// stops being something a person can hold in their head anyway.
const MaxUsed = 30

// Search ranks the emoji against a query, best first. An empty query answers
// with the ones this reader uses most and then the client's own panel order,
// which is the frequently used band Feishu's own panel opens with.
func (ix *Index) Search(query string) []Hit {
	query = strings.TrimSpace(query)
	if query == "" {
		return ix.unqueried()
	}
	var hits []Hit
	for i, e := range ix.items {
		j, score, pos := ix.mt.Best(ix.terms[i], query)
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{Emoji: e, Term: e.Terms[j], Positions: pos, score: score})
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
		hits = append(hits, Hit{Emoji: e})
	}
	slices.SortStableFunc(hits, func(a, b Hit) int { return ix.rank(a.Emoji) - ix.rank(b.Emoji) })
	return hits
}

// rank orders two emoji that a query cannot tell apart: the ones this reader
// uses first, most used before the rest, then the client's own panel order.
func (ix *Index) rank(e Emoji) int {
	key := Fold(e.Key)
	if i := slices.IndexFunc(ix.used, func(u usage) bool { return u.Key == key }); i >= 0 {
		return i - len(ix.used)
	}
	return e.Order
}

// The remembered lists are derived data: deleting one costs the ordering of an
// empty query and nothing else. Reacting and writing are separate acts, so they
// are remembered apart.
const (
	reactionUsedFile = "emoji-used.json"
	composerUsedFile = "emoji-used-write.json"
)

// LoadUsed fills the index's remembered list from the data dir. A file that is
// missing or unreadable leaves the list empty, which is the state a first run
// is in anyway.
func (ix *Index) LoadUsed(dataDir string) {
	b, err := os.ReadFile(filepath.Join(dataDir, ix.file))
	if err != nil {
		return
	}
	var list []usage
	if json.Unmarshal(b, &list) == nil {
		ix.setUsed(list)
	}
}

// SaveUsed writes the remembered list back.
func (ix *Index) SaveUsed(dataDir string) error {
	b, err := json.Marshal(ix.used)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, ix.file), b, 0o600)
}
