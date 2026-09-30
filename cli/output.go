package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func table(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = inline(c)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
}

// scrub drops every escape sequence and control character out of text a
// sender chose. Whatever Feishu carries reaches the terminal verbatim
// otherwise, and a terminal obeys the sequences it is written: with kitty's
// allow_remote_control on, a message body holding a \x1bP@kitty-cmd payload
// runs commands. A sequence goes whole, payload and all, which is what the
// TUI's cell buffer does with one it has no reading of; taking only its ESC
// would print the rest as text. Newlines and tabs stay — they are layout the
// printers already account for.
//
// Invalid UTF-8 goes first: the parser reads a stray byte such as 0x9b as a
// C1 CSI, which would swallow the character after it. What ansi.Strip leaves
// of the C0 set and DEL is dropped last.
//
// Only what larkim prints straight to the terminal comes through here.
func scrub(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		}
		return r
	}, ansi.Strip(strings.ToValidUTF8(s, "")))
}

// inline is scrub for a field sharing its line with others: a newline or a tab
// in a name would break the row it sits in, whoever put it there.
func inline(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(scrub(s), "\n", " "), "\t", " ")
}

func fmtMs(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
}

func oneLine(s string, max int) string {
	// Scrubbed before the cut so the budget is spent on characters that will be
	// drawn, and so the cut cannot fall inside an escape sequence.
	s = inline(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
