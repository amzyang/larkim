package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/card"
)

var stCardTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

// cardHead is the band the client paints above a card's body.
func cardHead(c card.Card) string {
	var parts []string
	if c.Title != "" {
		parts = append(parts, stCardTitle.Render(flatten(expandEmoji(c.Title))))
	}
	if c.Subtitle != "" {
		parts = append(parts, stDim.Render(flatten(expandEmoji(c.Subtitle))))
	}
	if c.Tags != "" {
		parts = append(parts, stDim.Render(c.Tags))
	}
	return strings.Join(parts, " ")
}

// cardGist is what a card says on one line: the band it is titled by, or the
// first thing its body says when it carries no band. The markers a document
// spells its structure with say nothing at this width.
func cardGist(c card.Card) string {
	if head := strings.TrimSpace(c.Title + " " + c.Tags); head != "" {
		return head
	}
	for _, b := range c.Blocks {
		for _, line := range strings.Split(b.Markdown, "\n") {
			if text := plainInline(strings.TrimLeft(line, "#>-* \t")); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

// cardButtonLine is one drawn line of an action row: the pills that fit it,
// and where each one sits, so a press lands on the button under it.
type cardButtonLine struct {
	text  string
	zones []clickZone
}

// cardButtons draw an action row as the filled pills the client shows, packed
// into lines w columns wide. A pill is an atom: it moves to the next line
// whole rather than being cut.
//
// Every pill leads somewhere. A button that opens a link opens it here too. A
// button that calls back to the app that sent the card cannot be pressed from
// outside the client at all — the payload reaches that app alone and no open
// API submits one — so it hands over to the client at this very message,
// which is the nearest larkim can come to the press the reader meant.
func cardButtons(bs []card.Button, w int, client string) []cardButtonLine {
	const gap = " "
	var lines []cardButtonLine
	var cur cardButtonLine
	used := 0
	flush := func() {
		if cur.text != "" {
			lines, cur, used = append(lines, cur), cardButtonLine{}, 0
		}
	}
	for _, b := range bs {
		label := strings.TrimSpace(b.Label)
		pill := stBtn.Render(expandEmoji(label))
		width := lipgloss.Width(pill)
		if used > 0 && used+len(gap)+width > w {
			flush()
		}
		if used > 0 {
			cur.text += gap
			used += len(gap)
		}
		url, note := b.URL, "opening "+label
		if url == "" {
			url, note = client, "opening in Feishu"
		}
		if url != "" {
			cur.zones = append(cur.zones, clickZone{x0: used, x1: used + width,
				urls: []string{url}, label: label, note: note})
		}
		cur.text += pill
		used += width
	}
	flush()
	return lines
}
