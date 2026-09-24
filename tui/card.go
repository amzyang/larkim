package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// cardRule is the left edge that stands for the card's frame. One column of
// it costs less than a box and still separates the card from plain messages.
const cardRule = "▌"

var (
	stCardTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

	// cardOpen matches the first line of the DSL lark-cli renders an
	// interactive message into.
	cardOpen = regexp.MustCompile(`^<card(?: title="((?:[^"\\]|\\.)*)")?(?: subtitle="((?:[^"\\]|\\.)*)")?>$`)
	// cardTagLine is the header's tag strip, which lark-cli writes on the line
	// below the open tag.
	cardTagLine = regexp.MustCompile(`^(?:「[^」]*」\s*)+$`)
	// cardActionLine is a row that holds nothing but buttons and link buttons.
	cardActionLine = regexp.MustCompile(`^\s*(?:\[[^\]\n]+\](?:\([^)\s]*\))?\s*)+$`)
	cardAction     = regexp.MustCompile(`\[([^\]\n]+)\](?:\(([^)\s]*)\))?`)
	// cardImage matches a line that is nothing but one image element. lark-cli
	// writes the alt text between the glyph and the key, and an alt of its own
	// may hold brackets, so the key is taken from the last group on the line.
	cardImage = regexp.MustCompile(`^🖼️.*\(img_key:(img_[A-Za-z0-9_-]+)\)$`)
)

// cardRow is one rendered line of a card: framed text, or the picture named
// by imgKey, which the message list places itself.
type cardRow struct {
	text   string
	imgKey string
}

// card is an interactive message taken apart: what the Feishu client draws in
// the header band, and the body below it.
type card struct {
	title, subtitle, tags string
	body                  []string
}

// parseCard reads the <card> DSL. It reports false for anything else, so a
// message whose text merely starts with a tag is left alone.
func parseCard(content string) (card, bool) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != "</card>" {
		return card{}, false
	}
	m := cardOpen.FindStringSubmatch(strings.TrimSpace(lines[0]))
	if m == nil {
		return card{}, false
	}
	c := card{title: cardUnescape(m[1]), subtitle: cardUnescape(m[2])}
	c.body = lines[1 : len(lines)-1]
	if len(c.body) > 0 && cardTagLine.MatchString(c.body[0]) {
		c.tags, c.body = strings.TrimSpace(c.body[0]), c.body[1:]
	}
	for len(c.body) > 0 && strings.TrimSpace(c.body[len(c.body)-1]) == "" {
		c.body = c.body[:len(c.body)-1]
	}
	return c, true
}

// cardUnescape reverses the attribute escaping lark-cli applies to the title
// and subtitle.
func cardUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// renderCard draws a card the way the Feishu client frames one: a title band,
// the body, and the actions as buttons on a row of their own.
func renderCard(c card, width int, ms mentions) []cardRow {
	inner := max(4, width-2)
	var out []cardRow
	add := func(s string) {
		for _, line := range wrap(s, inner) {
			out = append(out, cardRow{text: stAccent.Render(cardRule) + " " + line})
		}
	}

	var head []string
	if c.title != "" {
		head = append(head, stCardTitle.Render(flatten(expandEmoji(c.title))))
	}
	if c.subtitle != "" {
		head = append(head, stDim.Render(flatten(expandEmoji(c.subtitle))))
	}
	if c.tags != "" {
		head = append(head, stDim.Render(c.tags))
	}
	if len(head) > 0 {
		add(strings.Join(head, " "))
	}
	for _, line := range c.body {
		trimmed := strings.TrimSpace(line)
		switch img := cardImage.FindStringSubmatch(trimmed); {
		case trimmed == "---":
			add(stDim.Render(strings.Repeat("─", inner)))
		case img != nil:
			out = append(out, cardRow{imgKey: img[1]})
		case cardActionLine.MatchString(line):
			add(renderCardActions(line))
		default:
			add(renderInline(line, ms))
		}
	}
	if len(out) == 0 {
		add(stDim.Render("(empty card)"))
	}
	return out
}

// renderCardActions turns a button row into filled pills. The link target
// drops out the same way it does in body text: the label is what the button
// shows.
func renderCardActions(line string) string {
	var buttons []string
	for _, m := range cardAction.FindAllStringSubmatch(line, -1) {
		label := stBtn.Render(expandEmoji(strings.TrimSpace(m[1])))
		buttons = append(buttons, stBtnEdge.Render(chipLeft)+label+stBtnEdge.Render(chipRight))
	}
	return strings.Join(buttons, " ")
}
