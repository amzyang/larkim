// Package fuzzy matches a typed query against names, the way fzf does, and
// spells Chinese names the way they are reached from a latin keyboard.
package fuzzy

import (
	"slices"
	"strings"
	"unicode"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
	"github.com/mozillazg/go-pinyin"
)

// Sound spells a name the way it is typed on a latin keyboard. Runes the
// dictionary has no reading for — the digits in 18禁, the V in V5 — are kept
// as they are, so the term stays the whole name rather than the Chinese part
// of it.
func Sound(name string, style int) string {
	args := pinyin.NewArgs()
	args.Style = style
	var b strings.Builder
	for _, r := range name {
		if p := pinyin.SinglePinyin(r, args); len(p) > 0 {
			b.WriteString(p[0])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Terms is what a query is matched against: the name itself, its pinyin and
// its pinyin initials, because those are the three ways one reaches for 平台组
// without leaving the home row. A name that sounds like itself — anything
// already latin — contributes only once.
func Terms(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	out := []string{name}
	for _, style := range []int{pinyin.Normal, pinyin.FirstLetter} {
		if s := strings.ToLower(Sound(name, style)); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// Chars pre-tokenizes terms for the matcher. Building them once keeps the
// per-keystroke work to the matching itself.
func Chars(terms []string) []util.Chars {
	out := make([]util.Chars, 0, len(terms))
	for _, t := range terms {
		out = append(out, util.ToChars([]byte(t)))
	}
	return out
}

// slabSize is fzf's own scratch space for one match. The longest name here is
// a few dozen bytes, so this is already far more than the matcher can use.
const slabSize = 4096

// Matcher scores queries against terms. It is not safe for concurrent use:
// the scratch slab is reused from one match to the next.
type Matcher struct{ slab *util.Slab }

func NewMatcher() *Matcher { return &Matcher{slab: util.MakeSlab(slabSize, slabSize)} }

// Best is the highest-scoring term, its index, and the runes of it the query
// landed on. A score of zero means nothing matched, and the index is then -1.
func (mt *Matcher) Best(terms []util.Chars, query string) (i, score int, pos []int) {
	// Smart case, the way fzf and ripgrep read a query: a lowercase one asks
	// about neither case, a query with a capital in it means that capital.
	sensitive := strings.ToLower(query) != query
	pattern := []rune(query)
	i = -1
	for j := range terms {
		res, p := algo.FuzzyMatchV2(sensitive, true, true, &terms[j], pattern, true, mt.slab)
		if res.Score <= 0 || res.Score <= score {
			continue
		}
		i, score, pos = j, res.Score, sorted(p)
	}
	return i, score, pos
}

func sorted(pos *[]int) []int {
	if pos == nil {
		return nil
	}
	out := slices.Clone(*pos)
	slices.Sort(out)
	return out
}

// Index memoizes the spellings of named things, so a filter that runs on
// every keystroke spells each name once rather than once per keystroke. It is
// not safe for concurrent use: the matcher's scratch slab is shared.
type Index struct {
	mt    *Matcher
	terms map[spelled][]util.Chars
}

// spelled is what a memoized spelling belongs to: the thing, and the name it
// had when it was spelled, so a rename respells rather than answering to the
// old name.
type spelled struct{ key, name string }

func NewIndex() *Index {
	return &Index{mt: NewMatcher(), terms: map[spelled][]util.Chars{}}
}

// Match reports whether name answers query. pos is the runes of the name the
// query landed on, and is empty unless the name itself matched: a hit reached
// through pinyin has no offsets into the name to point at. key identifies the
// thing being matched.
func (ix *Index) Match(key, name, query string) (pos []int, ok bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, true
	}
	if HasCJK(query) {
		return substring(name, query)
	}
	i, score, pos := ix.mt.Best(ix.spellings(spelled{key, name}), query)
	if score <= 0 {
		return nil, false
	}
	if i != 0 {
		return nil, true
	}
	return pos, true
}

func (ix *Index) spellings(of spelled) []util.Chars {
	if t, ok := ix.terms[of]; ok {
		return t
	}
	t := Chars(Terms(of.name))
	ix.terms[of] = t
	return t
}

// HasCJK reports whether a query is written in Chinese. Such a query is
// matched as a run rather than a subsequence: 年会 is a word, and letting it
// land on the 年 and the 会 of "2026年高途新春启动会" answers a question
// nobody asked. Latin queries keep the subsequence match, which is what makes
// pinyin initials work.
func HasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return true
		}
	}
	return false
}

// substring matches a run of runes, case-insensitively, and reports where it
// landed so the caller can underline it.
func substring(name, query string) (pos []int, ok bool) {
	hay, needle := []rune(strings.ToLower(name)), []rune(strings.ToLower(query))
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) != string(needle) {
			continue
		}
		pos = make([]int, len(needle))
		for j := range needle {
			pos[j] = i + j
		}
		return pos, true
	}
	return nil, false
}
