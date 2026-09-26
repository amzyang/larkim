package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// gistCols is how wide an official emoji is drawn on a summary line. A gist
// stands in for a whole message in one row, so an icon on it takes the narrow
// size a chat row's reaction takes rather than the one it gets beside a body.
const gistCols = 2

// gist is one emoji's picture on a summary line.
func (p emojiPics) gist(key string) picture { return p.pic(key, gistCols) }

// emojiGist is the same picture for a row of the message list, which carries
// its renderer on the style rather than in a value of its own.
func (st msgStyle) emojiGist(key string) picture {
	return emojiPics{place: st.place, dir: st.dataDir}.gist(key)
}

// emojiSegs cuts a summary line at the official emoji no Unicode character
// carries, so each of them stands in the line as the picture the Feishu client
// draws rather than as the bracketed name it was spelled with. The stretches
// between them go through render, which styles them the way the caller would
// have styled the whole line.
//
// A line spelling none comes back nil, which is what keeps an ordinary line
// one string: a string is what a selection can tint and what every caller
// already knows how to cut.
func emojiSegs(line string, pic func(key string) picture, render func(string) string) []rowSeg {
	cuts := emojiCuts(line, nil, pic)
	if len(cuts) == 0 {
		return nil
	}
	var segs []rowSeg
	last := 0
	for _, c := range cuts {
		if c.lo > last {
			segs = append(segs, rowSeg{text: render(line[last:c.lo])})
		}
		segs = append(segs, c.seg)
		last = c.hi
	}
	if last < len(line) {
		segs = append(segs, rowSeg{text: render(line[last:])})
	}
	return segs
}

// truncateSegs cuts a line of pieces to n columns, the last of them spent on
// the ellipsis that says it was cut, the way truncate does for a string. A
// picture goes in whole or not at all: its cells name one image to the
// terminal, and half of them name nothing.
func truncateSegs(segs []rowSeg, n int) []rowSeg {
	if n <= 0 {
		return nil
	}
	if segsWidth(segs) <= n {
		return segs
	}
	var out []rowSeg
	used := 0
	for _, s := range segs {
		room := n - used - 1
		if s.pic.cols > 0 {
			if s.pic.cols > room {
				break
			}
			out, used = append(out, s), used+s.pic.cols
			continue
		}
		if w := lipgloss.Width(s.text); w <= room {
			out, used = append(out, s), used+w
			continue
		}
		if room > 0 {
			out = append(out, rowSeg{text: cut(s.text, room)})
		}
		break
	}
	return append(out, rowSeg{text: "…"})
}

// gistSegs is a summary line in pieces: a head already styled, then the gist
// with its emoji drawn as pictures, cut to what w leaves after the head. It
// answers nil when the gist needs no picture, leaving the caller on the string
// path it had before.
func gistSegs(head, gist string, w int, style lipgloss.Style, pic func(key string) picture) []rowSeg {
	segs := emojiSegs(gist, pic, func(t string) string { return style.Render(t) })
	if segs == nil {
		return nil
	}
	segs = truncateSegs(segs, w-lipgloss.Width(head))
	if head == "" {
		return segs
	}
	return append([]rowSeg{{text: head}}, segs...)
}

// padSegs is padBetween for a line in pieces: the line cut to what the mark at
// the far edge leaves it, then the spaces that push that mark to the edge.
func padSegs(segs []rowSeg, right string, w int) []rowSeg {
	rw := lipgloss.Width(right)
	if rw >= w {
		return []rowSeg{{text: fit(right, w)}}
	}
	gap := 1
	if rw == 0 {
		gap = 0
	}
	segs = truncateSegs(segs, w-rw-gap)
	return append(segs, rowSeg{text: strings.Repeat(" ", w-rw-segsWidth(segs)) + right})
}

// joinSegsWidth is the drawn form of a line of pieces at its own width, for a
// pane that builds strings rather than rows: the composer's reply bar and the
// forward chooser's header both sit in a block that is fitted afterwards, and
// a line already at its width is one fit leaves alone.
func (m Model) joinSegsWidth(segs []rowSeg) string { return m.joinSegs(segs, segsWidth(segs)) }
