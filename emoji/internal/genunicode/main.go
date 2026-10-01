// Command genunicode writes emoji/unicode_table.go: every Unicode emoji, skin
// tones included, with English and Chinese names and the search terms a
// composer matches them by.
//
// The data is the same pair alfred-emoji builds on — unicode-emoji-json for
// the characters, their names and CLDR order, emojilib for English keywords —
// plus CLDR's own annotations for the Chinese. Files are fetched from a CDN at
// pinned versions and cached on disk, so only the first run, or a run after a
// pin is bumped, needs the network.
package main

import (
	"encoding/json/v2"
	"flag"
	"fmt"
	"go/format"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/amzyang/larkim/emoji/internal/genutil"
)

// The pins. A bump is the one way new emoji arrive, and the one way the cache
// is refreshed.
const (
	unicodeEmojiJSON = "unicode-emoji-json@0.9.0"
	emojilib         = "emojilib@4.0.3"
	cldrAnnotations  = "cldr-annotations-full@48.2.0"
	cldrDerived      = "cldr-annotations-derived-full@48.2.0"
)

const cdn = "https://cdn.jsdelivr.net/npm"

// meta is one unicode-emoji-json entry.
type meta struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	SkinTone bool   `json:"skin_tone_support"`
}

// annotation is one CLDR entry: tts is the name a screen reader speaks, default
// the keywords.
type annotation struct {
	Default []string `json:"default"`
	TTS     []string `json:"tts"`
}

// sources is everything the table is built from, already decoded.
type sources struct {
	order    []string            // every base emoji, in CLDR order
	meta     map[string]meta     // keyed by emoji
	keywords map[string][]string // emojilib, keyed by emoji
	zh       map[string]annotation
	zhTone   map[string]annotation // CLDR's derived annotations: the toned sequences
}

// row is one emoji of the table before its terms are spelled.
type row struct {
	glyph, key, en string
	zh             string   // the display name, which may be empty
	zhTags         []string // CLDR's Chinese keywords
	tags           []string // emojilib's English keywords
}

// tone is one of the five Fitzpatrick modifiers and how CLDR spells it.
type tone struct {
	r          rune
	name, slug string
}

var tones = []tone{
	{0x1F3FB, "light skin tone", "light_skin_tone"},
	{0x1F3FC, "medium-light skin tone", "medium_light_skin_tone"},
	{0x1F3FD, "medium skin tone", "medium_skin_tone"},
	{0x1F3FE, "medium-dark skin tone", "medium_dark_skin_tone"},
	{0x1F3FF, "dark skin tone", "dark_skin_tone"},
}

func toneOf(r rune) (tone, bool) {
	i := slices.IndexFunc(tones, func(t tone) bool { return t.r == r })
	if i < 0 {
		return tone{}, false
	}
	return tones[i], true
}

// bare strips the variation selector, which CLDR leaves off its keys where
// unicode-emoji-json keeps it: ☺️ is ☺ to CLDR.
func bare(s string) string { return strings.ReplaceAll(s, "️", "") }

// untoned is a sequence with its skin tones taken out, and the tones in the
// order they stood. A sequence without one answers no tones.
func untoned(s string) (string, []tone) {
	var b strings.Builder
	var ts []tone
	for _, r := range s {
		if t, ok := toneOf(r); ok {
			ts = append(ts, t)
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), ts
}

// rows builds the table: the base emoji in CLDR order, then every toned
// sequence CLDR names, grouped under its base in that same order.
//
// The toned sequences are read from CLDR rather than built by putting a
// modifier after the base, the way alfred-emoji does it: that rule breaks the
// sequences with a direction or a second person in them, where the modifier
// goes after each person and nowhere else, and CLDR already lists every
// sequence Unicode defines, the two-person mixes included.
func rows(src sources) []row {
	byBare := make(map[string]string, len(src.order))
	for _, g := range src.order {
		byBare[bare(g)] = g
	}
	toned := map[string][]string{}
	for seq := range src.zhTone {
		stripped, ts := untoned(seq)
		if len(ts) == 0 {
			continue
		}
		base, ok := byBare[bare(stripped)]
		if !ok || !src.meta[base].SkinTone {
			continue
		}
		toned[base] = append(toned[base], seq)
	}

	out := make([]row, 0, len(src.order))
	for _, g := range src.order {
		out = append(out, baseRow(src, g))
	}
	for _, g := range src.order {
		for _, seq := range slices.Sorted(slices.Values(toned[g])) {
			out = append(out, toneRow(src, g, seq))
		}
	}
	return out
}

func baseRow(src sources, g string) row {
	m := src.meta[g]
	zh := src.zh[bare(g)]
	return row{glyph: g, key: m.Slug, en: m.Name, zh: first(zh.TTS), zhTags: zh.Default, tags: src.keywords[g]}
}

// toneRow names a toned sequence the way CLDR does in English — "waving hand:
// medium skin tone", two tones joined by a comma — and keys it by the base's
// slug with the tones' appended.
func toneRow(src sources, base, seq string) row {
	m := src.meta[base]
	_, ts := untoned(seq)
	names, slugs := make([]string, len(ts)), make([]string, len(ts))
	for i, t := range ts {
		names[i], slugs[i] = t.name, t.slug
	}
	if len(ts) == 2 && ts[0] == ts[1] {
		names, slugs = names[:1], slugs[:1]
	}
	en := m.Name + ": " + strings.Join(names, ", ")
	key := m.Slug + "_" + strings.Join(slugs, "_")
	zh := src.zhTone[seq]
	return row{glyph: seq, key: key, en: en, zh: first(zh.TTS), zhTags: zh.Default, tags: src.keywords[base]}
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// terms spells a row's names first and its keywords after, and reports how
// many of them are names. A keyword that spells the same as a name is a name.
func terms(r row, sound map[string][2]string) ([]string, int) {
	names := append([]string{r.glyph}, genutil.Terms(sound, []string{r.zh}, []string{r.en, key(r.key)})...)
	var out []string
	for _, t := range genutil.Terms(sound, r.zhTags, r.tags) {
		if !slices.Contains(names, t) {
			out = append(out, t)
		}
	}
	return append(names, out...), len(names)
}

func unique(s []string) []string {
	var out []string
	for _, v := range s {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func main() {
	out := flag.String("out", "unicode_table.go", "the file to write")
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		log.Fatal(err)
	}
	cache := flag.String("cache", filepath.Join(cacheRoot, "larkim", "genunicode"), "where fetched files are kept")
	flag.Parse()

	f := fetcher{base: cdn, dir: *cache, client: &http.Client{Timeout: 30 * time.Second}}
	src := load(f)
	rs := rows(src)

	var names []string
	for _, r := range rs {
		names = append(names, r.zh)
		names = append(names, r.zhTags...)
	}
	sound := genutil.Sounds(unique(names))
	write(*out, rs, sound)
	fmt.Fprintf(os.Stderr, "genunicode: wrote %d emoji to %s\n", len(rs), *out)
}

func load(f fetcher) sources {
	var src sources
	decode(f, unicodeEmojiJSON, "data-ordered-emoji.json", &src.order)
	decode(f, unicodeEmojiJSON, "data-by-emoji.json", &src.meta)
	decode(f, emojilib, "dist/emoji-en-US.json", &src.keywords)
	var zh struct {
		Annotations struct {
			Annotations map[string]annotation `json:"annotations"`
		} `json:"annotations"`
	}
	decode(f, cldrAnnotations, "annotations/zh/annotations.json", &zh)
	src.zh = zh.Annotations.Annotations
	var zhTone struct {
		AnnotationsDerived struct {
			Annotations map[string]annotation `json:"annotations"`
		} `json:"annotationsDerived"`
	}
	decode(f, cldrDerived, "annotationsDerived/zh/annotations.json", &zhTone)
	src.zhTone = zhTone.AnnotationsDerived.Annotations
	return src
}

func decode(f fetcher, pkg, path string, v any) {
	b, err := f.fetch(pkg, path)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Fatalf("%s/%s: %v", pkg, path, err)
	}
}

func write(path string, rs []row, sound map[string][2]string) {
	var b strings.Builder
	b.WriteString("// Code generated by emoji/internal/genunicode. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "// Data from %s and %s (MIT), and %s and %s\n", unicodeEmojiJSON, emojilib, cldrAnnotations, cldrDerived)
	b.WriteString("// (Unicode License v3, Copyright © 1991-2025 Unicode, Inc.).\n\npackage emoji\n\n")
	b.WriteString("// unicodeTable is every Unicode emoji, base forms in CLDR order and then\n")
	b.WriteString("// their skin tones. Feishu's own emoji are not taken out here; Unicode does that.\n")
	b.WriteString("var unicodeTable = []Emoji{\n")
	for _, r := range rs {
		ts, n := terms(r, sound)
		fmt.Fprintf(&b, "\t{Key: %q, Glyph: %q, ZH: %q, EN: %q, Names: %d,\n\t\tTerms: []string{", key(r.key), r.glyph, r.zh, r.en, n)
		for i, t := range ts {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", t)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		log.Fatal(err)
	}
}

// key keeps a slug clear of emoji.Fold, which reads a trailing _<digits> as
// the client's version suffix: keycap_1 and keycap_2 would both fold to
// KEYCAP. Joining the digits on keeps each key its own.
func key(slug string) string {
	base, suffix, ok := strings.CutLast(slug, "_")
	if ok && suffix != "" && strings.Trim(suffix, "0123456789") == "" {
		return base + suffix
	}
	return slug
}
