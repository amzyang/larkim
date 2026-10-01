// Package genutil holds what the emoji generators share: the way a name turns
// into the search terms a picker matches, and the pinyin those terms need.
package genutil

import (
	"log"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// nonTerm strips everything a query could not carry from a search term.
var nonTerm = regexp.MustCompile(`[^a-z0-9]+`)

// Terms is what a query is matched against. Each Chinese name contributes the
// name itself, its pinyin and the pinyin initials, because those are the three
// ways one reaches for 赞 without leaving the home row. Every latin name
// contributes its lowercase letters and digits.
func Terms(sound map[string][2]string, zh, latin []string) []string {
	var out []string
	add := func(s string) {
		if s = nonTerm.ReplaceAllString(strings.ToLower(s), ""); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	for _, name := range zh {
		if name == "" {
			continue
		}
		out = append(out, name) // the name itself keeps its Chinese
		add(sound[name][0])
		add(sound[name][1])
	}
	for _, name := range latin {
		add(name)
	}
	return out
}

// pinyinScript reads one name per line and answers with its full pinyin and
// its initials, tab-separated.
//
// It is pypinyin rather than the go-pinyin this module already carries because
// only pypinyin reads a name as a phrase: go-pinyin looks a character up on its
// own, so it takes the first reading of every polyphone and spells 音乐 yinle,
// 调皮 diaopi and 精神补给 jingshenbugei — none of which anyone would type.
// This runs at generation time and its answers are baked into the generated
// tables, so the binary keeps its pure-Go runtime and nothing but
// `go generate` needs Python.
const pinyinScript = `
import sys
from pypinyin import Style, lazy_pinyin
for line in sys.stdin.read().splitlines():
    full = "".join(lazy_pinyin(line, style=Style.NORMAL))
    initials = "".join(lazy_pinyin(line, style=Style.FIRST_LETTER))
    print(f"{full}\t{initials}")
`

// Sounds spells every name in one call, because starting an interpreter per
// name would cost more than the whole generation does. names must hold no
// duplicates and no newlines.
func Sounds(names []string) map[string][2]string {
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
