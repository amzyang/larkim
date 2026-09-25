package tui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/amzyang/larkim/store"
)

// atAll is the tag that reaches the whole room. Feishu spells the target
// "all" rather than an open id, and the name goes inside nothing: the client
// draws its own word for it.
const atAll = `<at user_id="all"></at>`

// resolveMentions turns the `@name` runs a draft carries into the tags Feishu
// notifies on. Typing a name is not enough — the client sends the tag, and
// without it an @ reaches nobody — so this runs on the way out, over both the
// text and the post paths, which lark-cli normalizes alike.
//
// Resolution is by name rather than by the offset the picker inserted at: the
// draft is ordinary text the reader goes on editing, and an offset would land
// on the wrong word the moment they typed in front of it. A stale entry in
// picked is therefore harmless, and a name nobody answers to stays literal
// rather than becoming a tag that names somebody at random.
//
// picked wins over roster, which is what settles two colleagues sharing a
// display name: the reader already said which one they meant.
func resolveMentions(draft string, picked map[string]string, roster []store.Contact) string {
	if !strings.Contains(draft, "@") {
		return draft
	}
	// Longest name first, so 张三丰 is not cut short into 张三 plus a stray
	// 丰 — the same rule the reading side resolves by.
	names := make([]string, 0, len(picked)+len(roster))
	ids := make(map[string]string, len(picked)+len(roster))
	for name, id := range picked {
		names, ids[name] = append(names, name), id
	}
	for _, c := range roster {
		if c.Name == "" || ids[c.Name] != "" {
			continue
		}
		names, ids[c.Name] = append(names, c.Name), c.OpenID
	}
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })

	var b strings.Builder
	for i := 0; i < len(draft); {
		if draft[i] != '@' || !atBoundary(draft, i) {
			b.WriteByte(draft[i])
			i++
			continue
		}
		rest := draft[i+1:]
		// allName carries its own @, which this loop has already consumed.
		if name, ok := cutName(rest, strings.TrimPrefix(allName, "@")); ok {
			b.WriteString(atAll)
			i += 1 + len(name)
			continue
		}
		matched := ""
		for _, name := range names {
			if n, ok := cutName(rest, name); ok {
				matched = n
				break
			}
		}
		if matched == "" {
			b.WriteByte('@')
			i++
			continue
		}
		b.WriteString(`<at user_id="` + ids[matched] + `">` + matched + `</at>`)
		i += 1 + len(matched)
	}
	return b.String()
}

// atBoundary reports whether the @ at i opens a mention rather than sitting
// inside a word, which is what keeps an email address from being read as one.
func atBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	r := lastRune(s[:i])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '-'
}

// cutName reports whether rest opens with name followed by something that can
// end a mention, so "@张三丰" is not answered by "张三".
func cutName(rest, name string) (string, bool) {
	if name == "" || !strings.HasPrefix(rest, name) {
		return "", false
	}
	after := rest[len(name):]
	if after == "" {
		return name, true
	}
	r := firstRune(after)
	// A CJK name runs straight into the next word with no space, so a letter
	// after it does not disqualify the match; only another name would, and the
	// longest-first order has already settled that.
	return name, !unicode.IsDigit(r) && r != '_'
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}
