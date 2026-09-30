package tui

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// safeLine is a drawn row with every escape sequence dropped but the two the
// renderer puts there itself: SGR, and the OSC 8 hyperlink a link wears. A
// row's text is a sender's, and Feishu carries whatever bytes they typed, so
// an ESC in a message body arrives here intact. A terminal obeys what it is
// written — with kitty's allow_remote_control on, a \x1bP@kitty-cmd payload
// runs commands — and cursor motion, clipboard writes and picture transmission
// are all a message body's for the taking otherwise.
//
// The TUI needs none of this: it draws into a cell buffer, which keeps a
// sequence it has no reading of out of the frame. Only a page written straight
// to the terminal, as UnreadPage writes one, comes through here. The pictures
// are transmitted around it, before any row, so no allowance for them is made.
func safeLine(s string) string {
	var out, esc strings.Builder
	linked := false // a hyperlink opened on this line and not yet closed
	state := parser.GroundState
	for i := 0; i < len(s); i++ {
		next, action := parser.Table.Transition(state, s[i])
		switch {
		case action == parser.CollectAction && next == parser.Utf8State:
			// The lead byte of a rune the table hands over to UTF-8 decoding.
			// The whole rune is taken here rather than its continuation bytes
			// counted down, because a malformed one would otherwise pass the
			// byte after it — an ESC among them — off as text.
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n <= 1 {
				state = parser.GroundState
				continue // not a rune after all, and a lead byte alone draws nothing
			}
			out.WriteString(s[i : i+n])
			i += n - 1
			next = parser.GroundState
		case action == parser.PrintAction:
			out.WriteByte(s[i])
		case action == parser.ExecuteAction:
			// A bare C0 byte. Nothing the renderer draws needs one, and a
			// terminal acts on every one of them.
		default:
			// Sequence bytes accumulate until the parser is back on the ground,
			// which is where the sequence is whole and can be judged. One left
			// unterminated at the end of the line is dropped with the rest.
			esc.WriteByte(s[i])
			if next == parser.GroundState {
				if seq := esc.String(); keepSeq(seq, &linked) {
					out.WriteString(seq)
				}
				esc.Reset()
			}
		}
		state = next
	}
	// A link the line leaves open runs on: every cell drawn after it, to the
	// end of the terminal's line and on down the page, leads where that one
	// link led. The renderer closes its own; a sender's would not.
	if linked {
		out.WriteString(ansi.ResetHyperlink())
	}
	return out.String()
}

// keepSeq is whether a whole escape sequence is one the renderer wrote,
// tracking through linked whether a hyperlink is open so safeLine can close one
// the line leaves running.
//
// SGR is matched on its parameter bytes rather than on the trailing m alone, so
// a private form such as \x1b[>4;2m — xterm's modifyOtherKeys, not a colour —
// does not pass as styling.
func keepSeq(seq string, linked *bool) bool {
	if sgr, ok := strings.CutPrefix(seq, "\x1b["); ok {
		return strings.HasSuffix(sgr, "m") &&
			strings.IndexFunc(sgr[:len(sgr)-1], func(r rune) bool {
				return (r < '0' || r > '9') && r != ';' && r != ':'
			}) < 0
	}
	rest, ok := strings.CutPrefix(seq, "\x1b]8;")
	if !ok {
		return false
	}
	// The OSC's own bytes, less the terminator the parser stopped on. A
	// terminal skips the control bytes it is allowed to hold, so one here is
	// only there to be smuggled past the reading below.
	body := strings.TrimSuffix(strings.TrimSuffix(rest, "\x1b\\"), "\x07")
	if strings.IndexFunc(body, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return false
	}
	target := linkTarget(rest)
	if target == "" {
		// The closing half, which is only ever answering an opening one: a
		// lone close would end a link the row before it opened.
		if !*linked {
			return false
		}
		*linked = false
		return true
	}
	if !openable(target) {
		return false
	}
	*linked = true
	return true
}

// linkSchemes are the schemes the renderer builds a target from: http and
// https for a URL a message spells, file for an attachment already downloaded,
// and lark for an applink into the desktop client.
//
// A target reaches the terminal ready to be followed, and following one hands
// it to macOS `open`, which gives it to whichever app claims its scheme. What
// spells the target is the sender either way — that is true of the client too,
// and of every link in the TUI — but the schemes larkim itself draws are these,
// so a body naming one it does not is a body reaching past the renderer.
var linkSchemes = []string{"http://", "https://", "file://", "lark://"}

// openable is whether a link target is one of the kinds larkim draws. A scheme
// is ASCII and case-insensitive, and a target holding neither a scheme nor a
// host is not a link at all.
func openable(target string) bool {
	return slices.ContainsFunc(linkSchemes, func(s string) bool {
		return len(target) > len(s) && strings.EqualFold(target[:len(s)], s)
	})
}
