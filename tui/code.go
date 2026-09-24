package tui

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// codeRule is the left edge a code block is drawn behind, thinner than the
// card's ▌ so a block inside a message does not read as a card of its own.
const codeRule = "▏"

// codeFence matches the line a rich-text code block opens and closes with. It
// is anchored to the whole line on purpose: lark-cli writes a card's code
// block into the middle of a sentence, with no newline either side, and a
// fence that is not a line of its own is not a block this list should frame.
var codeFence = regexp.MustCompile("^```[ \t]*([A-Za-z0-9_+#-]*)[ \t]*$")

// codeStyles are the two chroma palettes, picked by the terminal background.
// GitHub's are tuned for a white and a near-black backing respectively, which
// is as close as a fixed palette gets to the terminal it lands on.
const (
	codeStyleLight = "github"
	codeStyleDark  = "github-dark"
)

// takeCode collects the lines a fence opened at open holds, and reports the
// index the body loop carries on from. A block Feishu never closed runs to the
// end of the message, which is what the client shows while an edit is in
// flight.
func takeCode(lines []string, open int) (code []string, next int) {
	for i := open + 1; i < len(lines); i++ {
		if codeFence.MatchString(lines[i]) {
			return trimBlank(lines[open+1 : i]), i
		}
	}
	return trimBlank(lines[open+1:]), len(lines) - 1
}

// trimBlank drops the blank line lark-cli leaves between the last line of a
// code block and its closing fence, which would otherwise draw a numbered row
// with nothing on it.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// codeRows draws a code block the way the Feishu client frames one: the lines
// numbered down a rule, syntax coloured, and each cut to the pane rather than
// wrapped — a wrapped statement loses the shape that makes it readable, and
// `y` still copies the block whole.
func codeRows(code []string, lang string, width int, dark bool) []string {
	if len(code) == 0 {
		return nil
	}
	digits := len(strconv.Itoa(len(code)))
	room := width - digits - 3
	if room < 8 {
		// Too narrow to frame: the code itself is worth more than the gutter.
		digits, room = 0, width
	}
	out := make([]string, 0, len(code))
	for i, line := range highlightCode(strings.Join(code, "\n"), lang, dark) {
		gutter := ""
		if digits > 0 {
			gutter = stDim.Render(codeRule + " " + pad(strconv.Itoa(i+1), digits) + " ")
		}
		if ansi.StringWidth(line) > room {
			line = ansi.Truncate(line, max(1, room-1), "…")
		}
		out = append(out, gutter+line)
	}
	return out
}

// pad right-aligns a line number under the widest one in the block.
func pad(s string, w int) string { return strings.Repeat(" ", max(0, w-len(s))) + s }

// highlightCode colours one code block, a styled string per source line.
// Tokenising is done over the whole block because a string or a comment
// carries its colour across the line it opened on; the tokens are cut back
// into lines afterwards so each can be numbered and fitted on its own.
//
// A language chroma has no lexer for — Feishu's own PLAIN_TEXT among them —
// falls back to no colour at all rather than a guess.
func highlightCode(src, lang string, dark bool) []string {
	want := strings.Split(src, "\n")
	lexer := lexers.Get(strings.ToLower(lang))
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
