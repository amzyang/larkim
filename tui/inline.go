package tui

import (
	"regexp"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/charmbracelet/x/ansi"
)

// emojiSpelling matches both ways Feishu spells an official emoji inside a
// body: the emoji_type between colons that a rich-text post's emotion element
// becomes, and the display name between brackets a plain text message carries.
// Which of the two matched decides how the spelling is looked up, so the
// groups are kept apart.
var emojiSpelling = regexp.MustCompile(`:([A-Za-z0-9_]{1,32}):|\[([^\[\]\n]{1,12})\]`)

// inlineSegs cuts one body line at the emoji no Unicode character carries,
// which the client draws as a picture of its own. It reports nothing for a
// line that holds none, leaving it on the ordinary text path: the pieces cost
// the wrapping that a whole string gets for free.
func inlineSegs(line string, ms mentions, pic func(key string) picture) []rowSeg {
	var segs []rowSeg
	last := 0
	for _, m := range emojiSpelling.FindAllStringSubmatchIndex(line, -1) {
		// A markdown link's label is not an emoji, however it is spelled:
		// cutting [了解](https://…) apart would lose the link with it.
		if m[4] >= 0 && m[1] < len(line) && line[m[1]] == '(' {
			continue
		}
		name, lookup := "", emoji.ByName
		if m[2] >= 0 {
			name, lookup = line[m[2]:m[3]], emoji.ByKey
		} else {
			name = line[m[4]:m[5]]
		}
		e, ok := lookup(name)
		if !ok || e.Glyph != "" {
			continue
		}
		p := pic(e.Key)
		if p.cols == 0 {
			continue
		}
		if m[0] > last {
			segs = append(segs, rowSeg{text: renderInline(line[last:m[0]], ms)})
		}
		segs = append(segs, rowSeg{pic: p})
		last = m[1]
	}
	if len(segs) == 0 {
		return nil
	}
	if last < len(line) {
		segs = append(segs, rowSeg{text: renderInline(line[last:], ms)})
	}
	return segs
}

// wrapSegs packs a line's pieces into rows w columns wide. A picture is an
// atom: it moves to the next row whole rather than being cut, because the
// cells it stands in name one image to the terminal and half of them name
// nothing.
func wrapSegs(segs []rowSeg, w int) [][]rowSeg {
	var rows [][]rowSeg
	var line []rowSeg
	used := 0
	flush := func() {
		if len(line) > 0 {
			rows, line, used = append(rows, line), nil, 0
		}
	}
	for _, s := range segs {
		if s.pic.cols > 0 {
			if used+s.pic.cols > w {
				flush()
			}
			line, used = append(line, s), used+s.pic.cols
			continue
		}
		for text := s.text; text != ""; {
			head, rest := takeText(text, w-used)
			if head == "" && used > 0 {
				flush()
				continue
			}
			if head == "" {
				// A row's worth of one unbreakable word: cut it, the way a
				// wrapped body already cuts an overlong line.
				head, rest = ansi.Truncate(text, w, ""), ansi.TruncateLeft(text, w, "")
				if head == "" {
					break
				}
			}
			line, used = append(line, rowSeg{text: head}), used+ansi.StringWidth(head)
			if text = rest; text != "" {
				flush()
			}
		}
	}
	flush()
	return rows
}

// takeText puts as much of a styled run as fits in the columns a row has left,
// breaking where the run has a word boundary, and returns what is left for the
// rows below. An empty head means nothing fits beside what the row already
// holds.
func takeText(s string, avail int) (head, rest string) {
	if avail <= 0 {
		return "", s
	}
	if ansi.StringWidth(s) <= avail {
		return s, ""
	}
	head = ansi.Wordwrap(s, avail, "")
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	if w := ansi.StringWidth(head); w == 0 || w > avail {
		return "", s
	}
	// The break itself is a space the next row does not open with.
	return head, strings.TrimLeft(ansi.TruncateLeft(s, ansi.StringWidth(head), ""), " ")
}
