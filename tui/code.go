package tui

import (
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// codeRule is the left edge a code block is drawn behind, thinner than the
// card's ▌ so a block inside a message does not read as a card of its own.
const codeRule = "▏"

// codeStyles are the two chroma palettes, picked by the terminal background.
// GitHub's are tuned for a white and a near-black backing respectively, which
// is as close as a fixed palette gets to the terminal it lands on.
const (
	codeStyleLight = "github"
	codeStyleDark  = "github-dark"
)

// codeCopyGlyph is the block's Copy action (FA copy), drawn at its top right
// where the client puts the button it shows on hover. A terminal has no hover,
// so it is always there, dim. Like the other Nerd glyphs it pairs with an
// en-space to hold its two columns.
const codeCopyGlyph = "" + enSpace

// codeCopyCols is what every code row gives up so the icon's column stays
// clear down the block: the gap before the icon, then the icon.
var codeCopyCols = 1 + lipgloss.Width(codeCopyGlyph)

// codeBlock lays a code block's source out as body rows, the copy icon's
// target on the first.
func codeBlock(src, lang string, width int, dark bool, idx int, g *leads) []msgRow {
	src = strings.TrimRight(src, "\n")
	lines, zones := codeRows(src, lang, width, dark)
	rows := textRows(lines, idx, g)
	rows[0].addZones(zones)
	return rows
}

// codeRows draws a code block the way the Feishu client frames one: the lines
// numbered down a rule, syntax coloured, and wrapped to the pane so nothing
// of a long line is lost. Only a line's first row carries its number, which
// is what keeps a wrapped statement legible as one line. The zone is the copy
// icon on the first row, in the columns of the strings returned; a block too
// narrow to frame has none.
func codeRows(src, lang string, width int, dark bool) ([]string, []clickZone) {
	code := highlightCode(src, lang, dark)
	digits := len(strconv.Itoa(len(code)))
	room := width - digits - 3 - codeCopyCols
	if room < 8 {
		// Too narrow to frame: the code itself is worth more than the gutter.
		var out []string
		for _, line := range code {
			out = append(out, wrap(line, width)...)
		}
		return out, nil
	}
	cont := stDim.Render(codeRule + strings.Repeat(" ", digits+2))
	out := make([]string, 0, len(code))
	for i, line := range code {
		for j, row := range wrap(line, room) {
			gutter := cont
			if j == 0 {
				gutter = stDim.Render(codeRule + " " + pad(strconv.Itoa(i+1), digits) + " ")
			}
			out = append(out, gutter+row)
		}
	}
	out[0] += " " + stDim.Render(codeCopyGlyph)
	return out, []clickZone{{x0: width - codeCopyCols + 1, x1: width, copy: src, note: "code copied"}}
}

// pad right-aligns a line number under the widest one in the block.
func pad(s string, w int) string { return strings.Repeat(" ", max(0, w-len(s))) + s }

// lexerFor resolves a fence's language to a lexer, once per language. A name
// chroma's registry has no entry for falls through to a filename-glob scan
// across every bundled lexer, which costs milliseconds; the cursor keys
// re-render the whole page, so a chat of code blocks would pay that scan on
// every keystroke.
var lexerFor = memoLexer(lexers.Get)

// memoLexer wraps a lookup so each language is resolved once. A fence naming
// no language is answered without asking at all: there is nothing to match on,
// and it is the name chroma spends longest refusing.
func memoLexer(lookup func(string) chroma.Lexer) func(string) chroma.Lexer {
	var cache sync.Map
	return func(lang string) chroma.Lexer {
		if lang == "" {
			return nil
		}
		if v, ok := cache.Load(lang); ok {
			lexer, _ := v.(chroma.Lexer)
			return lexer
		}
		lexer := lookup(lang)
		cache.Store(lang, lexer)
		return lexer
	}
}

// highlightCode colours one code block, a styled string per source line.
// Tokenising is done over the whole block because a string or a comment
// carries its colour across the line it opened on; the tokens are cut back
// into lines afterwards so each can be numbered and fitted on its own.
//
// A language chroma has no lexer for — Feishu's own PLAIN_TEXT among them —
// falls back to no colour at all rather than a guess.
func highlightCode(src, lang string, dark bool) []string {
	want := strings.Split(src, "\n")
	lexer := lexerFor(strings.ToLower(lang))
	if lexer == nil {
		return want
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return want
	}
	name := codeStyleLight
	if dark {
		name = codeStyleDark
	}
	style := styles.Get(name)
	out := make([]string, 0, len(want))
	var b strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		st := codeTokenStyle(style.Get(tok.Type), tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			if part != "" {
				b.WriteString(st.Render(part))
			}
		}
	}
	out = append(out, b.String())
	// Chroma ends the stream on a newline of its own when the source has none,
	// and the rows are numbered off this slice, so it is held to the lines the
	// block actually has.
	for len(out) < len(want) {
		out = append(out, "")
	}
	return out[:len(want)]
}

// codeTokenStyle is one token's colour. Plain text keeps the terminal's own
// foreground: a palette that names a near-white for it would paint every
// uncoloured run, and the background entry is dropped for the same reason the
// rule carries no tint — it would fight the row's selection.
func codeTokenStyle(e chroma.StyleEntry, t chroma.TokenType) lipgloss.Style {
	st := lipgloss.NewStyle()
	switch t {
	case chroma.Text, chroma.None, chroma.Background:
		return st
	}
	if e.Colour.IsSet() {
		st = st.Foreground(lipgloss.Color(e.Colour.String()))
	}
	if e.Bold == chroma.Yes {
		st = st.Bold(true)
	}
	if e.Italic == chroma.Yes {
		st = st.Italic(true)
	}
	if e.Underline == chroma.Yes {
		st = st.Underline(true)
	}
	return st
}
