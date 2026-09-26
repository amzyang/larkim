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
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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

// zodiac are the new-year greetings named after their animal rather than their
// year. They go stale the same January the numbered ones do, and there is
// nothing in their key to tell the year apart from the animal, so they are
// listed by hand alongside yearly.
var zodiac = map[string]bool{
	"RoarForYou": true, "JubilantRabbit": true, "HappyDragon": true, "SiSiASYouWish": true,
}

// yearly matches the new-year emoji, which are dead the January after they
// ship and would otherwise crowd the picker.
var yearly = regexp.MustCompile(`^\d{4}$`)

// foreign are other tenants' culture emoji. They reach this client through the
// same table as everything else and go inside a message fine, but Feishu
// rejects them as reactions from any other tenant: "prohibit the use of custom
// emojis from other companies".
var foreign = map[string]bool{
	"PursueUltimate": true, "CustomerSuccess": true,
	"Responsible": true, "Ambitious": true,
}

// delisted are the emoji the client has withdrawn. The sprite still carries
// them, so they draw on a message somebody reacted with them years ago, but
// sending one now reaches the other side as "[Sensitive emoji]" and Feishu
// refuses it as a reaction the same way it refuses a foreign one.
//
// Both lists are kept by hand: the client states them in its bundle rather
// than in the assets this reads, and the pair of them is short enough that
// parsing the bundle would cost more than a check after an upgrade.
var delisted = map[string]bool{
	"ATTENTION": true, "WELLDONE": true, "FOLLOWME": true,
	"DETERGENT": true, "AWESOME": true, "GOODJOB": true,
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
	Delisted    bool
}

func main() {
	src := flag.String("assets", assetsDir, "the client's assets/emoji directory")
	out := flag.String("out", "table.go", "the file to write")
	sheet := flag.String("sheet", "sprite-min.png", "where to copy the client's sprite sheet")
	flag.Parse()

	var meta spriteMeta
	readJSON(filepath.Join(*src, "sprite-meta.json"), &meta)
	var i18n map[string]map[string]string
	readJSON(filepath.Join(*src, "emojiKeyToI18n.json"), &i18n)

	var aliases map[string][]string
	readJSON("aliases.json", &aliases)
	var order map[string]int
	readJSON("order.json", &order)

	kept := make([]string, 0, len(meta.KeyMap))
	for key := range meta.KeyMap {
		if !skip(key) {
			kept = append(kept, key)
		}
	}
	slices.Sort(kept)
	sound := sounds(spoken(i18n, aliases, kept))

	entries := make([]entry, 0, len(kept))
	folded := map[string]string{}
	for _, key := range kept {
		image := meta.KeyMap[key]
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
			Terms: terms(sound, names["zh-CN"], names["en-US"], key, aliases[key]),
			Order: pos, NoReaction: foreign[key] || delisted[key], Delisted: delisted[key],
		})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.Key, b.Key) })
	write(*out, entries, toneBases(meta.KeyMap, i18n, entries))
	copySheet(filepath.Join(*src, "sprite-min.png"), *sheet)
	fmt.Fprintf(os.Stderr, "gen: wrote %d emoji to %s, sheet to %s\n", len(entries), *out, *sheet)
}

// copySheet carries the sprite sheet into the package, where go:embed puts it
// in the binary. The rectangles in table.go address this exact sheet, so a
// table written from one client version and a sheet from another would cut the
// wrong pictures out.
func copySheet(src, dst string) {
	b, err := os.ReadFile(src)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		log.Fatal(err)
	}
}

// skip drops the keys a picker must not offer. The two reasons are told apart
// because only one of them leaves an emoji behind to map onto: a tone is a
// spelling of an emoji still in the table, an expired greeting is nothing.
func skip(key string) bool { return expired(key) || tone(key) }

// expired is a new-year emoji of a year gone by, named after either.
func expired(key string) bool { return yearly.MatchString(key) || zodiac[key] }

// tone is a skin-tone variant of an emoji the client offers alongside it.
func tone(key string) bool {
	return slices.ContainsFunc(tones, func(t string) bool { return strings.HasPrefix(key, t) })
}

// terms is what a query is matched against. Each Chinese name contributes the
// name itself, its pinyin and the pinyin initials, because those are the three
// ways one reaches for 赞 without leaving the home row.
func terms(sound map[string][2]string, zh, en, key string, aliases []string) []string {
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
		add(sound[name][0])
		add(sound[name][1])
	}
	add(en)
	add(key)
	return out
}

// pinyinScript reads one name per line and answers with its full pinyin and
// its initials, tab-separated.
//
// It is pypinyin rather than the go-pinyin this module already carries because
// only pypinyin reads a name as a phrase: go-pinyin looks a character up on its
// own, so it takes the first reading of every polyphone and spells 音乐 yinle,
// 调皮 diaopi and 精神补给 jingshenbugei — none of which anyone would type.
// This runs at generation time and its answers are baked into table.go, so the
// binary keeps its pure-Go runtime and nothing but `go generate` needs Python.
const pinyinScript = `
import sys
from pypinyin import Style, lazy_pinyin
for line in sys.stdin.read().splitlines():
    full = "".join(lazy_pinyin(line, style=Style.NORMAL))
    initials = "".join(lazy_pinyin(line, style=Style.FIRST_LETTER))
    print(f"{full}\t{initials}")
`

// sounds spells every name in one call, because starting an interpreter per
// name would cost more than the whole generation does.
func sounds(names []string) map[string][2]string {
	cmd := exec.Command("uv", "run", "--quiet", "--with", "pypinyin", "python", "-c", pinyinScript)
	cmd.Stdin = strings.NewReader(strings.Join(names, "\n"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("uv run pypinyin: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(names) {
		log.Fatalf("pypinyin answered %d of %d names", len(lines), len(names))
	}
	sound := make(map[string][2]string, len(names))
	for i, line := range lines {
		full, initials, _ := strings.Cut(line, "\t")
		sound[names[i]] = [2]string{full, initials}
	}
	return sound
}

// spoken is every name a term is built from, deduplicated so one pypinyin call
// covers the table.
func spoken(i18n map[string]map[string]string, aliases map[string][]string, keys []string) []string {
	var names []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	for _, key := range keys {
		add(i18n[key]["zh-CN"])
		for _, a := range aliases[key] {
			add(a)
		}
	}
	return names
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
		if !tone(key) {
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
		if e.Delisted {
			b.WriteString(" Delisted: true,")
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
