package sync

import "github.com/amzyang/larkim/card"

// cardText renders an interactive card from the card JSON its body carries,
// which is the only place the card's block structure survives: the text
// lark-cli renders it into runs block elements together, which glues a heading
// onto the list beneath it. A card with nothing in it is named by its type,
// the way an unreadable one is.
func cardText(contentRaw string) string {
	c, ok := card.Parse(contentRaw)
	if !ok {
		return "[Card]"
	}
	return c.Markdown()
}
