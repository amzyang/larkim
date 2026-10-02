package ai

import (
	"strings"
)

// Segment is one piece of a finished answer: a card the reader can act on,
// or the commentary standing between them. What sits outside the blocks is
// commentary for the reader alone and never sends.
type Segment struct {
	Card bool
	Text string
}

// SplitAnswer breaks an answer on the <reply> blocks the system prompt asks
// for: each block is one sendable option, and the rest is commentary. The
// markers decide their own parsing:
//
//   - A marker counts wherever it stands on a line, outside inline code:
//     models write <reply>text</reply> on one line as often as on three.
//   - A <reply> inside a fenced code block is text the answer is quoting,
//     not an option it is offering; outside a block, fences guard the
//     opening marker and nothing else.
//   - Inside a block, </reply> closes it even under an unbalanced fence the
//     option opened itself — a formatting slip in an option is far likelier
//     than a marker quoted in code, and the card goes to the wire as one
//     unit anyway, so the fence does not outlive its block.
//   - A closer with no block to close is marker noise: swallowed rather than
//     let the whole-text fallback post the tag itself.
//
// An answer with no block is commentary for the reader alone — Copy and
// Insert only, never Send. A block left unclosed while the answer streams
// is the card it opened, with whatever has arrived.
func SplitAnswer(text string) []Segment {
	var segs []Segment
	inCard, fence, marked := false, false, false
	var buf strings.Builder
	// flush closes the segment under construction; empty ones (the blank
	// lines around markers) are nothing to show.
	flush := func(card bool) {
		if s := strings.TrimSpace(buf.String()); s != "" {
			segs = append(segs, Segment{Card: card, Text: s})
		}
		buf.Reset()
	}
	piece := func(line string) {
		switch trimmed := strings.TrimSpace(line); {
		case inCard && trimmed == "</reply>":
			flush(true)
			inCard, marked, fence = false, true, false
		case !fence && trimmed == "</reply>":
			marked = true
		case !fence && !inCard && trimmed == "<reply>":
			flush(false)
			inCard, marked = true, true
		default:
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			buf.WriteString(line)
			buf.WriteByte('\n')
			fence = !fence
			continue
		}
		if fence && !inCard {
			piece(line)
			continue
		}
		for _, p := range splitMarkers(line) {
			piece(p)
		}
	}
	flush(inCard)
	// No marker at all is commentary only; markers that offered only empty
	// blocks leave the commentary as commentary, with nothing to send.
	if !marked {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []Segment{{Card: false, Text: strings.TrimSpace(text)}}
	}
	return segs
}

// splitMarkers cuts a line around each <reply> and </reply> in it, so a
// block written on one line — <reply>text</reply> — parses as one written
// across three. A marker inside `inline code` is quoted text and stays put.
func splitMarkers(line string) []string {
	var out []string
	start, code := 0, false
	for i := range len(line) {
		if line[i] == '`' {
			code = !code
			continue
		}
		if code || line[i] != '<' {
			continue
		}
		for _, m := range []string{"<reply>", "</reply>"} {
			if strings.HasPrefix(line[i:], m) {
				if s := line[start:i]; strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
				out = append(out, m)
				i += len(m) - 1
				start = i + 1
				break
			}
		}
	}
	if s := line[start:]; strings.TrimSpace(s) != "" || len(out) == 0 {
		out = append(out, s)
	}
	return out
}
