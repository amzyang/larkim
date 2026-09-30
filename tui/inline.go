package tui

import (
	"hash/fnv"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// hyperlink hands drawn text to the terminal as a link. A label says where it
// leads only once it is followed, and following one meant reaching for the
// OPEN list; kitty draws the target under the pointer instead, underlines the
// run while it is there, and opens it on Ctrl+Shift+click even while larkim
// holds the mouse. The link also travels with the text through a copy.
//
// The name is the target's own hash, which is what makes the halves of a link
// wrapped across rows hover as the one link they are: the fragments share it
// by construction, without anything having to number them.
func hyperlink(target, text string) string {
	if target == "" {
		return text
	}
	h := fnv.New64a()
	h.Write([]byte(target))
	return ansi.SetHyperlink(target, "id="+strconv.FormatUint(h.Sum64(), 36)) + text + ansi.ResetHyperlink()
}

// fileURL spells a downloaded attachment as a link to it. A path is not a URL
// until it is escaped: the names Feishu sends carry spaces and Chinese
// characters, and both end a URL where they stand.
func fileURL(path string) string {
	if path == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// emojiSpelling matches both ways Feishu spells an official emoji inside a
// body: the emoji_type between colons that a rich-text post's emotion element
// becomes, and the display name between brackets a plain text message carries.
// Which of the two matched decides how the spelling is looked up, so the
// groups are kept apart.
var emojiSpelling = regexp.MustCompile(`:([A-Za-z0-9_]{1,32}):|\[([^\[\]\n]{1,12})\]`)

// inlineSegs cuts one body line at the pieces that are not ordinary text: a
// link, spelled with a label or written out, which leads somewhere the row
// has to be able to hand over, and an emoji no Unicode character carries,
// which the client draws as a picture of its own. It reports nothing for a
// line holding neither, leaving it on the ordinary text path: the pieces cost
// the wrapping that a whole string gets for free.
//
// Which spellings count as an emoji here rides on ms, so that the pictures cut
// out of a line and the glyphs drawn into the text between them read the same
// body the same way.
func inlineSegs(line string, ms mentions, pic func(key string) picture, doc func(url string) (store.DocLabel, bool)) []rowSeg {
	cuts := linkCuts(line)
	cuts = append(cuts, autoLinkCuts(line, cuts, doc)...)
	cuts = append(cuts, emojiCuts(line, cuts, ms.spell, pic)...)
	if len(cuts) == 0 {
		return nil
	}
	return cutSegs(line, cuts, func(s string) string { return renderInline(s, ms) })
}

// cutSegs lays a line out as the pieces cuts names and the stretches of
// ordinary text between them, which plain draws. How that text is drawn is the
// caller's: a rendered line carries markup in it, a rich-text element's own
// words do not.
func cutSegs(line string, cuts []inlineCut, plain func(string) string) []rowSeg {
	slices.SortFunc(cuts, func(a, b inlineCut) int { return a.lo - b.lo })
	var segs []rowSeg
	last := 0
	for _, c := range cuts {
		if c.lo > last {
			segs = append(segs, rowSeg{text: plain(line[last:c.lo])})
		}
		if len(c.seg.urls) > 0 && c.seg.text == "" {
			c.seg.text = plain(line[c.lo:c.hi])
		}
		segs = append(segs, c.seg)
		last = c.hi
	}
	if last < len(line) {
		segs = append(segs, rowSeg{text: plain(line[last:])})
	}
	return segs
}

// inlineCut is one piece of a line that is not ordinary text, and the span of
// the line it was spelled by.
type inlineCut struct {
	lo, hi int
	seg    rowSeg
}

// linkCuts finds the links a line spells. The label keeps the whole inline
// path, so an emoji or a mention inside it is drawn the way it is anywhere
// else; what the label gains here is the target behind it.
func linkCuts(line string) []inlineCut {
	var cuts []inlineCut
	for _, m := range inlineMD.FindAllStringSubmatchIndex(line, -1) {
		if m[2] < 0 {
			continue // some other run of markup; renderInline still styles it
		}
		label, url := line[m[2]:m[3]], line[m[4]:m[5]]
		if url == "" {
			continue // nothing to hand over; the label is just text
		}
		if strings.TrimSpace(label) == "" {
			label = url
		}
		label = flatten(label)
		cuts = append(cuts, inlineCut{lo: m[0], hi: m[1], seg: rowSeg{
			urls: []string{url}, label: label, note: "opening " + label}})
	}
	return cuts
}

// bareURL matches a URL written out rather than spelled as a link. Feishu
// makes one pressable wherever it appears, so larkim has to find it in the
// text the same way. An address is ASCII, so Chinese running straight on after
// one — the bracket it was put in, the sentence it sits in — ends the match
// instead of being swallowed into the target. The trailing class leaves out
// the marks a sentence ends on besides: a full stop or a closing bracket after
// a URL belongs to the sentence.
var bareURL = regexp.MustCompile(`https?://[^\s<>"'\x60\[\]()\x{80}-\x{10FFFF}]*[^\s<>"'\x60\[\]().,;:!?\x{80}-\x{10FFFF}]`)

// autoLinkCuts finds the URLs a line writes out, skipping any inside a link
// already spelled with a label: that one has been cut, target and all.
func autoLinkCuts(line string, links []inlineCut, doc func(url string) (store.DocLabel, bool)) []inlineCut {
	var cuts []inlineCut
	for _, m := range bareURL.FindAllStringIndex(line, -1) {
		if slices.ContainsFunc(links, func(l inlineCut) bool { return m[0] < l.hi && m[1] > l.lo }) {
			continue
		}
		url := line[m[0]:m[1]]
		// A written-out URL carries no label to style, so it is styled here;
		// a spelled link gets that from renderInline along with the rest.
		text, label := stLink.Render(url), url
		if l, ok := doc(url); ok {
			// The client draws a Feishu document link as the document, not as
			// its address: a token says nothing about what it opens. A
			// document out of reach keeps its URL, which is all the client
			// leaves of one too, marked by the lock it draws beside it.
			if l.Denied {
				// No space before the address: a space is where the wrapper
				// breaks a run, and a URL too long for the row would leave the
				// mark stranded on a line of its own, or worse, sitting
				// against whatever text came before it.
				text = lockGlyph + text
			} else if title := flatten(l.Title); title != "" {
				text, label = docGlyph(l.Type)+" "+stLink.Render(title), title
			}
		}
		cuts = append(cuts, inlineCut{lo: m[0], hi: m[1], seg: rowSeg{
			text: text, urls: []string{url}, label: label, note: "opening " + label}})
	}
	return cuts
}

// lockGlyph marks a document this identity cannot open, the way the client
// marks one: the link stays, and the mark beside it says why following it
// will not help.
const lockGlyph = "🔒"

// docGlyph marks a document by the family its type puts it in, the way
// fileGlyph does for an attachment. Every glyph is two columns wide, so a
// title starts in the same column whichever family it is; the two that are
// only one column on their own carry a variation selector to say so.
func docGlyph(docType string) string {
	switch docType {
	case "sheet":
		return "📊"
	case "baseform":
		return "📝"
	case "minutes":
		return "🎧"
	case "bitable":
		return "🗂️"
	case "mindnote":
		return "🧠"
	case "slides":
		return "📽️"
	case "folder":
		return "📁"
	case "file":
		return "📎"
	default:
		return "📄"
	}
}

// emojiCuts finds the emoji a line spells that no Unicode character carries,
// skipping any inside a link: a label is drawn whole so that the whole of it
// leads to the same place. Only the spellings in sp are read; a body drawing
// from neither is left whole without a scan.
func emojiCuts(line string, links []inlineCut, sp emojiSpell, pic func(key string) picture) []inlineCut {
	if sp == spellNone {
		return nil
	}
	var cuts []inlineCut
	for _, m := range emojiSpelling.FindAllStringSubmatchIndex(line, -1) {
		if slices.ContainsFunc(links, func(l inlineCut) bool { return m[0] < l.hi && m[1] > l.lo }) {
			continue
		}
		name, lookup, group := "", emoji.ByName, spellBracket
		if m[2] >= 0 {
			name, lookup, group = line[m[2]:m[3]], emoji.ByKey, spellShortcode
		} else {
			name = line[m[4]:m[5]]
		}
		if sp&group == 0 {
			continue
		}
		e, ok := lookup(name)
		if !ok || e.Glyph != "" {
			continue
		}
		p := pic(e.Key)
		if p.cols == 0 {
			continue
		}
		cuts = append(cuts, inlineCut{lo: m[0], hi: m[1], seg: rowSeg{pic: p}})
	}
	return cuts
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
		// A run cut across rows leads where the whole of it led: both halves
		// of a wrapped label open the same link.
		frag := func(text string) rowSeg {
			return rowSeg{text: text, urls: s.urls, label: s.label, note: s.note}
		}
		for text := s.text; text != ""; {
			// A row the wrapping opened starts at a word, the way the client
			// starts one: the space it broke at belongs to neither row.
			if used == 0 && len(rows) > 0 {
				if text = dropLeadingSpaces(text); text == "" {
					break
				}
			}
			head, rest := takeText(text, w-used)
			if head == "" {
				// The run breaks nowhere the row has space for. One that could
				// start on a row of its own moves down whole; one that could
				// not — a URL longer than the row — is cut where the row ends.
				if used > 0 {
					if h, _ := takeText(text, w); h != "" {
						flush()
						continue
					}
				}
				head = closeStyle(cut(text, w-used))
				if head == "" {
					if used > 0 {
						flush()
						continue
					}
					break
				}
				rest = cutLeft(text, ansi.StringWidth(head))
			}
			line, used = append(line, frag(head)), used+ansi.StringWidth(head)
			if text = rest; text != "" {
				flush()
			}
		}
	}
	flush()
	return rows
}

// takeText puts as much of a styled run as fits in the columns a row has left,
// breaking only where the Feishu client would: at a space, between two
// ideographs, never before a closing mark nor after an opening one — the
// rules of UAX #14 that the client's browser engine follows. It returns what
// is left for the rows below, the break's own spaces dropped. An empty head
// means nothing fits beside what the row already holds.
func takeText(s string, avail int) (head, rest string) {
	if avail <= 0 {
		return "", s
	}
	if ansi.StringWidth(s) <= avail {
		return s, ""
	}
	fit, next, _, _ := lastBreak(ansi.Strip(s), avail)
	if fit == 0 {
		return "", s
	}
	return closeStyle(cut(s, fit)), cutLeft(s, next)
}

// lastBreak finds the last break opportunity in plain whose line fits avail
// columns. fit is the columns that line draws, its trailing spaces left out
// because a line ending at a break hangs them; next is the columns up to
// where the following line starts. fitAt and nextAt are the same two places
// as byte offsets into plain. All are zero when no break fits.
//
// The clusters come from uniseg, which segments by UAX #14 without cutting
// into a grapheme cluster, and are measured with ansi.StringWidth, the ruler
// every cut and every pane in larkim uses.
func lastBreak(plain string, avail int) (fit, next, fitAt, nextAt int) {
	cols, spaces, size := 0, 0, len(plain)
	for state := -1; plain != ""; {
		var cluster string
		var boundaries int
		cluster, plain, boundaries, state = uniseg.StepString(plain, state)
		w := ansi.StringWidth(cluster)
		cols += w
		if cluster == " " {
			spaces += w
		} else {
			spaces = 0
		}
		if cols-spaces > avail {
			break
		}
		if plain != "" && boundaries&uniseg.MaskLine != uniseg.LineDontBreak && cols > spaces {
			// A space is a byte and a column both.
			at := size - len(plain)
			fit, next, fitAt, nextAt = cols-spaces, cols, at-spaces, at
		}
	}
	return fit, next, fitAt, nextAt
}

// dropLeadingSpaces takes the spaces a styled run opens with off it. The
// escapes standing in front of them stay: they set up the style the rest of
// the run is drawn in, which is why a plain TrimLeft cannot do this.
func dropLeadingSpaces(s string) string {
	plain := ansi.Strip(s)
	n := len(plain) - len(strings.TrimLeft(plain, " "))
	if n == 0 {
		return s
	}
	return cutLeft(s, n)
}

// closeStyle ends a colour the cut went through, so the padding a row is
// filled out with does not carry an underline or a background on past the
// text.
func closeStyle(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	return s + ansi.ResetStyle
}
