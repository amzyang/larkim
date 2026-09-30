package tui

import (
	"image/color"
	"regexp"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// inlineMD matches the markup lark-cli renders a message body into: links,
// bold and italic runs, strikethrough, inline code, the underline Feishu's
// rich text carries over, and the colour a card spells as a font tag. Italics require a non-space immediately
// inside the asterisks so ordinary prose ("3 * 4 * 5") is left alone, and the
// wider asterisk runs come first so a tripled one is not read as an italic
// wrapping an asterisk, nor a doubled one as an empty italic.
var inlineMD = regexp.MustCompile(`\[([^\]\n]*)\]\(([^)\s]*)\)` +
	`|\*\*\*([^*\n]+)\*\*\*` +
	`|\*\*([^*\n]+)\*\*` +
	`|<u>([^<]*)</u>` +
	`|<font color="([^"]*)">(.*?)</font>` +
	`|~~([^~\n]+)~~` +
	"|`([^`\n]+)`" +
	`|\*(\S[^*\n]*)\*`)

var (
	stLink  = lipgloss.NewStyle().Foreground(colAccent).Underline(true)
	stUnder = lipgloss.NewStyle().Underline(true)
	stCode  = lipgloss.NewStyle().Foreground(colAccent)
)

// hitPositions reports the runes of s that a search term stands on, in the
// form markName takes. Terms are matched the way the store matched them to
// find this message at all — every whitespace-separated word, case-insensitive
// substring — so what is marked is what was searched for. Overlapping terms
// coalesce, marking once.
func hitPositions(s string, terms []string) []int {
	if len(terms) == 0 {
		return nil
	}
	// Positions are rune indices, so the lowered copy is walked as runes too;
	// a byte offset would land mid-character on any Chinese word.
	lower := []rune(strings.ToLower(s))
	var pos []int
	for _, t := range terms {
		term := []rune(strings.ToLower(t))
		if len(term) == 0 {
			continue
		}
		for i := 0; i+len(term) <= len(lower); i++ {
			if slices.Equal(lower[i:i+len(term)], term) {
				for j := range len(term) {
					pos = append(pos, i+j)
				}
			}
		}
	}
	return pos
}

// personName is how a colleague is named on screen: the account suffix runs
// on from the name ("李明" + "01"), the way the tenant spells the account
// itself, so the chat list and the message list name the same person alike.
func personName(name, suffix string) string { return name + suffix }

// renderInline styles one line of message text against the message's own
// mentions. A link keeps only its label, which is what the Feishu client
// shows; the target stays in the raw content that `y` copies and `o` opens.
//
// An emphasis run is drawn by reading what it wraps in the style it adds,
// rather than by rendering its content as it stands: a rich-text element
// carries every style it was given at once and the markup spelling it back
// nests, so a layer that stopped at the markup below it would draw that
// markup instead of the words.
func renderInline(s string, ms mentions) string {
	var b strings.Builder
	last := 0
	// in draws what one emphasis run wraps, with the style it adds standing
	// over whatever the runs around it already gave the words.
	in := func(inner string, add func(lipgloss.Style) lipgloss.Style) string {
		return renderInline(inner, ms.styled(add(ms.base)))
	}
	for _, m := range inlineMD.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(ms.render(s[last:m[0]]))
		switch {
		case m[2] >= 0:
			label := s[m[2]:m[3]]
			if strings.TrimSpace(label) == "" {
				label = s[m[4]:m[5]]
			}
			b.WriteString(stLink.Render(expandEmoji(label, ms.spell)))
		case m[6] >= 0:
			b.WriteString(in(s[m[6]:m[7]], func(st lipgloss.Style) lipgloss.Style {
				return st.Bold(true).Italic(true)
			}))
		case m[8] >= 0:
			b.WriteString(in(s[m[8]:m[9]], func(st lipgloss.Style) lipgloss.Style { return st.Bold(true) }))
		case m[10] >= 0:
			b.WriteString(in(s[m[10]:m[11]], func(st lipgloss.Style) lipgloss.Style { return st.Underline(true) }))
		case m[12] >= 0:
			fg, ok := cardColour(s[m[12]:m[13]])
			b.WriteString(in(s[m[14]:m[15]], func(st lipgloss.Style) lipgloss.Style {
				if !ok {
					return st
				}
				return st.Foreground(fg)
			}))
		case m[16] >= 0:
			b.WriteString(in(s[m[16]:m[17]], func(st lipgloss.Style) lipgloss.Style { return st.Strikethrough(true) }))
		case m[18] >= 0:
			// Code is the one run that is literal by definition: whatever
			// asterisks are inside it are the code's own.
			b.WriteString(stCode.Render(s[m[18]:m[19]]))
		case m[20] >= 0:
			b.WriteString(in(s[m[20]:m[21]], func(st lipgloss.Style) lipgloss.Style { return st.Italic(true) }))
		}
		last = m[1]
	}
	b.WriteString(ms.render(s[last:]))
	return b.String()
}

// cardColour is the terminal colour closest to one a card names, the shade a
// name carries after its hyphen left off. The client's palette is wider than
// the terminal's; each name lands on the colour that keeps its meaning — red
// for an alarm, grey for an aside. A name with no such colour, default among
// them, leaves the text as it is.
func cardColour(name string) (color.Color, bool) {
	base, _, _ := strings.Cut(name, "-")
	switch base {
	case "red", "carmine":
		return colErr, true
	case "green", "turquoise":
		return lipgloss.Color("2"), true
	case "orange", "yellow":
		return colWarn, true
	case "blue", "wathet", "indigo":
		return colAccent, true
	case "purple", "violet":
		return lipgloss.Color("5"), true
	case "grey", "gray", "neutral":
		return colDim, true
	}
	return nil, false
}

// wrap breaks styled text to w columns, returning at least one line, each
// padded to w. It breaks where takeText does, so a body reads the same
// whichever path draws it.
func wrap(s string, w int) []string {
	w = max(4, w)
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")
	var lines []string
	for src := range strings.SplitSeq(s, "\n") {
		lines = wrapLine(lines, src, w)
	}
	return lines
}

// wrapLine appends the rows one source line breaks into. The line is read
// once, into its text, its escapes and where each cluster starts, and every
// row is cut from that: cutting the styled string itself costs the whole of
// what is left on every row, which a paragraph with no break in it pays once
// per row, on every reload of the page it is on.
func wrapLine(lines []string, src string, w int) []string {
	l := splitStyled(src)
	n := len(l.at) - 1
	i := 0
	for first := true; ; first = false {
		if !first {
			// A row the wrapping opened starts at a word: the space it broke
			// at belongs to neither row, nor does a mark an escape parted
			// from it.
			rest := l.plain[l.at[i]:]
			if sp := len(rest) - len(strings.TrimLeft(rest, " ")); sp > 0 {
				for i = l.clusterAt(l.at[i] + sp); i < n && l.cols[i+1] == l.cols[i]; i++ {
				}
			}
		}
		var end, next int
		if l.cols[n]-l.cols[i] <= w {
			end, next = n, n
		} else if fit, _, fitAt, nextAt := lastBreak(l.plain[l.at[i]:], w); fit > 0 {
			end, next = l.clusterAt(l.at[i]+fitAt), l.clusterAt(l.at[i]+nextAt)
			// The break is found in the text, where an emoji an escape cuts
			// into is one cluster; drawn, its pieces are wider.
			if end <= i || l.cols[end]-l.cols[i] > w {
				end = max(i+1, l.upTo(i, w))
				next = end
			}
		} else {
			// Nowhere to break inside the row: a URL longer than the row.
			end = max(i+1, l.upTo(i, w))
			next = end
		}
		row := l.row(i, end, end == n)
		lines = append(lines, row+strings.Repeat(" ", max(0, w-(l.cols[end]-l.cols[i]))))
		if l.cols[n]-l.cols[next] == 0 {
			return lines
		}
		i = next
	}
}

// styledLine is one line of styled text taken apart: its text, the escapes
// it carries and where in the text each stands, and the column every grapheme
// cluster of the text starts at. at and cols run one past the last cluster.
type styledLine struct {
	plain string
	escs  []lineEsc
	at    []int // byte offset into plain of each cluster
	cols  []int // columns before each cluster
}

type lineEsc struct {
	pos int // byte offset into plain the escape stands before
	seq string
}

// splitStyled takes a styled line apart, reading bytes the way ansi.Strip
// does so that its text is exactly what Strip would leave.
func splitStyled(s string) styledLine {
	var l styledLine
	var plain strings.Builder
	var esc strings.Builder
	flush := func() {
		if esc.Len() > 0 {
			l.escs = append(l.escs, lineEsc{pos: plain.Len(), seq: esc.String()})
			esc.Reset()
		}
	}
	pstate := parser.GroundState
	ri, rw := 0, 0
	for i := range len(s) {
		if pstate == parser.Utf8State {
			plain.WriteByte(s[i])
			if ri++; ri < rw {
				continue
			}
			pstate, ri, rw = parser.GroundState, 0, 0
			continue
		}
		state, action := parser.Table.Transition(pstate, s[i])
		switch {
		case action == parser.CollectAction && state == parser.Utf8State:
			flush()
			rw, ri = utf8LeadLen(s[i]), 1
			plain.WriteByte(s[i])
		case action == parser.PrintAction, action == parser.ExecuteAction:
			flush()
			plain.WriteByte(s[i])
		default:
			// A sequence ends where the parser is back on the ground, which
			// keeps each one whole for lineState to read.
			if esc.WriteByte(s[i]); state == parser.GroundState {
				flush()
			}
		}
		pstate = state
	}
	flush()
	l.plain = plain.String()
	// An escape ends a cluster, as it does for ansi.StringWidth: a keycap
	// drawn one rune at a time measures as its runes, and the row is padded
	// to what the styled string measures.
	cols, from := 0, 0
	for _, stop := range append(slices.Collect(func(yield func(int) bool) {
		for _, e := range l.escs {
			if !yield(e.pos) {
				return
			}
		}
	}), len(l.plain)) {
		for from < stop {
			c, cw := ansi.FirstGraphemeCluster(l.plain[from:stop], ansi.GraphemeWidth)
			l.at, l.cols = append(l.at, from), append(l.cols, cols)
			cols += cw
			from += len(c)
		}
	}
	l.at, l.cols = append(l.at, len(l.plain)), append(l.cols, cols)
	return l
}

// utf8LeadLen is how many bytes the rune a lead byte opens takes, counted the
// way ansi.Strip counts them: -1 for a byte no rune starts with.
func utf8LeadLen(b byte) int {
	switch {
	case b <= 0x7F:
		return 1
	case b >= 0xC0 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF7:
		return 4
	}
	return -1
}

// upTo is the cluster a cut c columns after cluster i ends before, a cluster
// no wider than nothing on the edge going with the row.
func (l styledLine) upTo(i, c int) int {
	j := i
	for j < len(l.at)-1 && l.cols[j+1]-l.cols[i] <= c {
		j++
	}
	return j
}

// clusterAt is the last cluster starting at or before byte b of the text.
func (l styledLine) clusterAt(b int) int {
	j, found := slices.BinarySearch(l.at, b)
	if !found {
		j--
	}
	return j
}

// row draws clusters i to end. It opens on the escapes still in force where i
// stands, which is what ansi.TruncateLeft keeps less what a reset since has
// cancelled, and closes what it left open unless it runs to the line's end,
// where the line's own escapes close it.
func (l styledLine) row(i, end int, last bool) string {
	a, b := l.at[i], l.at[end]
	var open lineState
	var out strings.Builder
	k := 0
	for ; k < len(l.escs) && l.escs[k].pos < a; k++ {
		open.take(l.escs[k].seq)
	}
	out.WriteString(open.String())
	at := a
	for ; k < len(l.escs) && (l.escs[k].pos < b || last); k++ {
		out.WriteString(l.plain[at:l.escs[k].pos])
		out.WriteString(l.escs[k].seq)
		at = l.escs[k].pos
	}
	out.WriteString(l.plain[at:b])
	if last {
		return out.String()
	}
	return closeStyle(closeLink(out.String()))
}

// lineState is what the escapes before a point in a line leave in force: the
// SGR since the last reset, the link still open, and any escape it has no
// reading of, kept whole.
type lineState struct {
	sgr   []string
	link  string
	other []string
}

func (s *lineState) take(seq string) {
	switch {
	case seq == "\x1b[m" || seq == "\x1b[0m":
		s.sgr = s.sgr[:0]
	case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
		s.sgr = append(s.sgr, seq)
	case strings.HasPrefix(seq, "\x1b]8;"):
		s.link = ""
		if linkTarget(seq[len("\x1b]8;"):]) != "" {
			s.link = seq
		}
	default:
		s.other = append(s.other, seq)
	}
}

func (s lineState) String() string {
	return strings.Join(s.other, "") + s.link + strings.Join(s.sgr, "")
}
