package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/card"
)

var stCardTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

// cardTemplate is the band a header template paints: the client's colour for
// it, and the ink that reads on it.
type cardTemplate struct{ bg, fg color.Color }

var (
	inkLight = lipgloss.Color("#ffffff")
	inkDark  = lipgloss.Color("#1f2329")
)

// cardTemplates are the client's header colours. A pale one takes dark ink,
// the way the client writes on it.
var cardTemplates = map[string]cardTemplate{
	"blue":      {lipgloss.Color("#3370ff"), inkLight},
	"wathet":    {lipgloss.Color("#b7d4fa"), inkDark},
	"turquoise": {lipgloss.Color("#04b49c"), inkLight},
	"green":     {lipgloss.Color("#2ea121"), inkLight},
	"yellow":    {lipgloss.Color("#f5c400"), inkDark},
	"orange":    {lipgloss.Color("#ff8800"), inkLight},
	"red":       {lipgloss.Color("#f54a45"), inkLight},
	"carmine":   {lipgloss.Color("#e22e6f"), inkLight},
	"violet":    {lipgloss.Color("#b147cc"), inkLight},
	"purple":    {lipgloss.Color("#7f3bf5"), inkLight},
	"indigo":    {lipgloss.Color("#4954e6"), inkLight},
	"grey":      {lipgloss.Color("#8f959e"), inkLight},
}

// cardHead is the band the client paints above a card's body, w columns
// wide: in the template's colour when the card names one, which is what
// tells an alarm from a report at a glance.
func cardHead(c card.Card, w int) []string {
	tpl, banded := cardTemplates[c.Template]
	title, aside := stCardTitle, stDim
	if banded {
		title, aside = lipgloss.NewStyle().Bold(true), lipgloss.NewStyle()
	}
	var parts []string
	if c.Title != "" {
		parts = append(parts, title.Render(flatten(expandEmoji(c.Title, spellAll))))
	}
	if c.Subtitle != "" {
		parts = append(parts, aside.Render(flatten(expandEmoji(c.Subtitle, spellAll))))
	}
	if c.Tags != "" {
		parts = append(parts, aside.Render(c.Tags))
	}
	if len(parts) == 0 {
		return nil
	}
	head := strings.Join(parts, " ")
	if !banded {
		return wrap(head, w)
	}
	// The band keeps a column clear at either end, the inset the client
	// gives its title.
	band := lipgloss.NewStyle().Background(tpl.bg).Foreground(tpl.fg)
	lines := wrap(head, w-2)
	for i, l := range lines {
		lines[i] = paint(band, " "+l+" ")
	}
	return lines
}

// The pills a button type is drawn as. A terminal draws no border, so the
// client's outlined kinds are a pale fill in the outline's colour; its filled
// kinds keep the fill; its text kinds stay text.
var (
	stBtnDefault     = lipgloss.NewStyle().Foreground(colChatSelText).Background(lipgloss.Color("#dee0e3")).Padding(0, 1)
	stBtnDanger      = lipgloss.NewStyle().Foreground(lipgloss.Color("#d83931")).Background(lipgloss.Color("#fdddd9")).Padding(0, 1)
	stBtnPrimaryFill = lipgloss.NewStyle().Foreground(inkLight).Background(colMentionMe).Padding(0, 1)
	stBtnDangerFill  = lipgloss.NewStyle().Foreground(inkLight).Background(lipgloss.Color("#f54a45")).Padding(0, 1)
	stBtnPrimaryText = lipgloss.NewStyle().Foreground(colAccent).Padding(0, 1)
	stBtnDangerText  = lipgloss.NewStyle().Foreground(colErr).Padding(0, 1)
	stBtnPlainText   = lipgloss.NewStyle().Padding(0, 1)
)

// buttonStyle is the pill a card button of type typ is drawn as. A type this
// build does not know is a primary one, the pill every button wore before
// their types were read.
func buttonStyle(typ string) lipgloss.Style {
	switch typ {
	case "default":
		return stBtnDefault
	case "danger":
		return stBtnDanger
	case "primary_filled":
		return stBtnPrimaryFill
	case "danger_filled":
		return stBtnDangerFill
	case "primary_text":
		return stBtnPrimaryText
	case "danger_text":
		return stBtnDangerText
	case "text":
		return stBtnPlainText
	}
	return stBtn
}

// cardGist is what a card says on one line: the summary the sender wrote for
// exactly this, the band it is titled by, or its whole body run together when
// it carries neither, for the caller to cut at its width. The markers a
// document spells its structure with say nothing at this width.
//
// The summary comes first because that is the line the client shows in its
// own chat list, and a band is often the same few words on every card a bot
// posts, where the summary names the one alarm this card is about.
//
// The body goes in whole rather than by its first line: a bot that opens with
// a greeting would otherwise be summed up by the greeting. A picture is named
// where it sits, as in gistBody; buttons are left out, since their labels are
// controls rather than anything the card says.
func cardGist(c card.Card) string {
	if c.Summary != "" {
		return c.Summary
	}
	if head := strings.TrimSpace(c.Title + " " + c.Tags); head != "" {
		return head
	}
	var parts []string
	for _, top := range c.Blocks {
		for _, b := range top.Stacked() {
			if b.ImageKey != "" {
				parts = append(parts, msgTypeLabel("image"))
			}
			for line := range strings.SplitSeq(b.Markdown, "\n") {
				if text := inlineText(strings.TrimLeft(line, "#>-* \t")); strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, " ")
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
		style := buttonStyle(b.Type)
		if b.Fill {
			// The whole line is the button, its label in the middle of it.
			style = style.Width(w).Align(lipgloss.Center)
		}
		pill := style.Render(expandEmoji(label, spellAll))
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
			pill = hyperlink(url, pill)
		}
		cur.text += pill
		used += width
	}
	flush()
	return lines
}
