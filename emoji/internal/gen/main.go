// Command gen writes emoji/table.go from the Lark client's own offline emoji
// assets, so the names, images and search terms track whatever the installed
// client ships rather than a table kept by hand.
//
// The client keeps its emoji in one directory of plain files — a sprite sheet,
// the rectangles inside it, and the display names in eighteen locales. That is
// everything except the panel order, which lives only in the client's
// JavaScript bundle; order.json carries a copy of it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"log"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mozillazg/go-pinyin"
)

// assetsDir is where the macOS client keeps the files below. Versions/Current
// is a symlink the client maintains, so no version is spelled out here.
const assetsDir = "/Applications/sagtjy516.app/Contents/Frameworks/Lark Framework.framework/" +
	"Versions/Current/Resources/assets/emoji"

// tones prefix the skin-tone variants of the hand emoji. Every key carrying
// one is a variant of the default the client offers alongside it, and only the
// default belongs in a picker. Longest first, so MediumDark is not read as
// Medium.
var tones = []string{"MediumLight", "MediumDark", "Medium", "Light", "Dark"}

// yearly matches the new-year emoji, which are dead the January after they
// ship and would otherwise crowd the picker.
var yearly = regexp.MustCompile(`^\d{4}$`)

// noReaction are other tenants' culture emoji. They reach this client through
// the same table as everything else, but Feishu rejects them as reactions, so
// the picker has to leave them out rather than write a reaction that fails.
var noReaction = map[string]bool{
	"PursueUltimate": true, "CustomerSuccess": true,
	"Responsible": true, "Ambitious": true,
}

// nonTerm strips everything a query could not carry from a search term.
var nonTerm = regexp.MustCompile(`[^a-z0-9]+`)

// toneSuffix is how the client marks a skin tone in a display name: 鼓掌（中等浅色）
// is 鼓掌 in one of five tones. The names are what ties a variant back to the
// emoji it is a tone of — the keys do not, because Applaud and APPLAUSE are
// the same emoji spelled two ways.
var toneSuffix = regexp.MustCompile(`（[^）]*）$`)

type spriteMeta struct {
	KeyMap map[string]string `json:"keyMap"`
	Coords map[string]struct {
		X, Y, Width, Height int
	} `json:"coords"`
}

type entry struct {
	Key, ZH, EN string
	Rect        [4]int // x, y, width, height inside the sprite sheet
	Terms       []string
	Order       int
	NoReaction  bool
}

func main() {
	src := flag.String("assets", assetsDir, "the client's assets/emoji directory")
	out := flag.String("out", "table.go", "the file to write")
	flag.Parse()

	var meta spriteMeta
	readJSON(filepath.Join(*src, "sprite-meta.json"), &meta)
	var i18n map[string]map[string]string
	readJSON(filepath.Join(*src, "emojiKeyToI18n.json"), &i18n)

	var aliases map[string][]string
	readJSON("aliases.json", &aliases)
	var order map[string]int
	readJSON("order.json", &order)

	entries := make([]entry, 0, len(meta.KeyMap))
	folded := map[string]string{}
	for key, image := range meta.KeyMap {
		if skip(key) {
			continue
		}
		names, ok := i18n[key]
		if !ok {
			log.Fatalf("%s is in the sprite but carries no display name", key)
		}
		box, ok := meta.Coords[image]
		if !ok {
			log.Fatalf("%s names %s, which the sprite has no rectangle for", key, image)
		}
		// The folded key is the emoji's filename once the sprite is cut up, and
		// two emoji sharing one would overwrite each other on a case-insensitive
		// filesystem.
		if was, clash := folded[Fold(key)]; clash {
			log.Fatalf("%s and %s fold to the same name", was, key)
		}
		folded[Fold(key)] = key
		// An emoji outside the panel sorts after every one inside it, in the
		// order the picker falls back to: the key itself.
		pos, ok := order[key]
		if !ok {
			pos = len(order) + 1
		}
		entries = append(entries, entry{
			Key: key, ZH: names["zh-CN"], EN: names["en-US"],
			Rect:  [4]int{box.X, box.Y, box.Width, box.Height},
			Terms: terms(names["zh-CN"], names["en-US"], key, aliases[key]),
			Order: pos, NoReaction: noReaction[key],
		})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.Key, b.Key) })
	write(*out, entries, toneBases(meta.KeyMap, i18n, entries))
	fmt.Fprintf(os.Stderr, "gen: wrote %d emoji to %s\n", len(entries), *out)
}

// skip drops the keys a picker must not offer: the skin-tone variants of an
// emoji already listed, and the new-year emoji of years gone by.
func skip(key string) bool {
	if yearly.MatchString(key) {
		return true
	}
	return slices.ContainsFunc(tones, func(t string) bool { return strings.HasPrefix(key, t) })
}

// terms is what a query is matched against. Each Chinese name contributes the
// name itself, its pinyin and the pinyin initials, because those are the three
// ways one reaches for 赞 without leaving the home row.
func terms(zh, en, key string, aliases []string) []string {
	var out []string
	add := func(s string) {
		if s = nonTerm.ReplaceAllString(strings.ToLower(s), ""); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	for _, name := range append([]string{zh}, aliases...) {
		if name == "" {
			continue
		}
		out = append(out, name) // the name itself keeps its Chinese
		add(sound(name, pinyin.Normal))
		add(sound(name, pinyin.FirstLetter))
	}
	add(en)
	add(key)
	return out
}

// sound spells a name the way it is typed on a latin keyboard. Runes the
// dictionary has no reading for — the digits in 18禁, the V in V5 — are kept
// as they are, so the term stays the whole name rather than the Chinese part
// of it.
func sound(name string, style int) string {
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

// Fold is emoji.Fold, repeated here because the generator cannot import the
// package it writes into without a chicken-and-egg build.
func Fold(s string) string {
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "LARK_EMOJI_")
	if i := strings.LastIndex(s, "_"); i > 0 && strings.Trim(s[i+1:], "0123456789") == "" {
		s = s[:i]
	}
	return s
}

func readJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Fatalf("%s: %v", path, err)
	}
}

// toneBases maps every skin-tone variant onto the emoji it is a tone of.
// Feishu sends the variants as reactions even though its picker offers only
// the default, so a reader whose colleague reacted with a dark-skinned thumbs
// up has to be shown a thumbs up rather than a key they cannot read.
func toneBases(keyMap map[string]string, i18n map[string]map[string]string, entries []entry) map[string]string {
	base := make(map[string]string, len(entries))
	for _, e := range entries {
		base[e.ZH] = Fold(e.Key)
	}
	out := map[string]string{}
	for key := range keyMap {
		if !skip(key) || yearly.MatchString(key) {
			continue
		}
		name := toneSuffix.ReplaceAllString(i18n[key]["zh-CN"], "")
		if to, ok := base[name]; ok {
			out[Fold(key)] = to
			continue
		}
		log.Fatalf("%s is a tone of %q, which names no emoji", key, name)
	}
	return out
}

func write(path string, entries []entry, tones map[string]string) {
	var b strings.Builder
	b.WriteString("// Code generated by emoji/internal/gen. DO NOT EDIT.\n\npackage emoji\n\n")
	b.WriteString("// table is every emoji the Lark client ships, minus the skin-tone variants\n")
	b.WriteString("// and the new-year emoji. Glyph is not here: it is the one column no client\n")
	b.WriteString("// asset can answer, and it lives in glyphs.go.\nvar table = []Emoji{\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "\t{Key: %q, ZH: %q, EN: %q, Rect: [4]int{%d, %d, %d, %d}, Order: %d,",
			e.Key, e.ZH, e.EN, e.Rect[0], e.Rect[1], e.Rect[2], e.Rect[3], e.Order)
		if e.NoReaction {
			b.WriteString(" NoReaction: true,")
		}
		b.WriteString("\n\t\tTerms: []string{")
		for i, t := range e.Terms {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", t)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n\n")
	b.WriteString("// toneVariants maps a skin-tone spelling onto the emoji it is a tone of.\n")
	b.WriteString("// The client offers only the default, but Feishu accepts and sends every\n")
	b.WriteString("// tone, so a reaction can arrive under a key no picker ever showed.\n")
	b.WriteString("var toneVariants = map[string]string{\n")
	for _, k := range slices.Sorted(maps.Keys(tones)) {
		fmt.Fprintf(&b, "\t%q: %q,\n", k, tones[k])
	}
	b.WriteString("}\n")
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		log.Fatal(err)
	}
}
