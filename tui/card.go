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

// cardButtons draws an action row as the filled pills the client shows. A
// button's target drops out the same way a link's does: the label is what it
// shows.
func cardButtons(labels []string) string {
	pills := make([]string, 0, len(labels))
	for _, l := range labels {
		pills = append(pills, stBtn.Render(expandEmoji(strings.TrimSpace(l))))
	}
	return strings.Join(pills, " ")
}
