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
// for: each block is one sendable option, and the rest is commentary. A
// fenced code block is content, not markers — a <reply> inside one is text
// the answer is quoting, not an option it is offering. An answer with no
// block is the message itself, which is one card; a block left unclosed
// while the answer streams is the card it opened, with whatever has arrived.
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
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = !fence
		}
		switch {
		case fence:
			buf.WriteString(line)
			buf.WriteByte('\n')
		case trimmed == "<reply>":
			flush(false)
			inCard, marked = true, true
		case trimmed == "</reply>":
			flush(true)
			inCard, marked = false, true
		default:
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush(inCard)
	// No marker at all means the answer is the message itself, which is one
	// card; markers that offered only empty blocks leave the commentary as
	// commentary, with nothing to send.
	if !marked {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []Segment{{Card: true, Text: strings.TrimSpace(text)}}
	}
	return segs
}
