package tui

import (
	"regexp"
	"slices"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
)

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
func inlineSegs(line string, ms mentions, pic func(key string) picture, doc func(url string) (store.DocLabel, bool)) []rowSeg {
	cuts := linkCuts(line)
	cuts = append(cuts, autoLinkCuts(line, cuts, doc)...)
	cuts = append(cuts, emojiCuts(line, cuts, pic)...)
	if len(cuts) == 0 {
		return nil
	}
	slices.SortFunc(cuts, func(a, b inlineCut) int { return a.lo - b.lo })

	var segs []rowSeg
	last := 0
	for _, c := range cuts {
		if c.lo > last {
			segs = append(segs, rowSeg{text: renderInline(line[last:c.lo], ms)})
		}
		if len(c.seg.urls) > 0 && c.seg.text == "" {
			c.seg.text = renderInline(line[c.lo:c.hi], ms)
		}
		segs = append(segs, c.seg)
		last = c.hi
	}
	if last < len(line) {
		segs = append(segs, rowSeg{text: renderInline(line[last:], ms)})
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
		cuts = append(cuts, inlineCut{lo: m[0], hi: m[1], seg: rowSeg{
			urls: []string{url}, label: flatten(label), note: "opening " + flatten(label)}})
	}
	return cuts
}

// bareURL matches a URL written out rather than spelled as a link. Feishu
// makes one pressable wherever it appears, so larkim has to find it in the
// text the same way. The trailing class leaves out the marks a sentence ends
// on — a full stop or a closing bracket after a URL belongs to the sentence.
var bareURL = regexp.MustCompile(`https?://[^\s<>"'\x60\[\]()]*[^\s<>"'\x60\[\]().,;:!?，。；：！？、]`)

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
// leads to the same place.
func emojiCuts(line string, links []inlineCut, pic func(key string) picture) []inlineCut {
	var cuts []inlineCut
	for _, m := range emojiSpelling.FindAllStringSubmatchIndex(line, -1) {
		if slices.ContainsFunc(links, func(l inlineCut) bool { return m[0] < l.hi && m[1] > l.lo }) {
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
			head, rest := takeText(text, w-used)
			if head == "" && used > 0 {
				flush()
				continue
			}
			if head == "" {
				// A row's worth of one unbreakable word: cut it, the way a
				// wrapped body already cuts an overlong line.
				head = cut(text, w)
				if head == "" {
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
	return head, strings.TrimLeft(cutLeft(s, ansi.StringWidth(head)), " ")
}
