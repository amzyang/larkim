package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/emoji"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestEmojiInfo_DropsAPieceThatRepeatsTheName(t *testing.T) {
	t.Parallel()
	// Feishu's lettering emoji spell their own name, and the term a query
	// landed on may spell it again; the row already draws it once.
	ok := emoji.Emoji{Key: "OK", EN: "OK"}
	require.Empty(t, emojiInfo(emoji.Hit{Emoji: ok}))
	yes := emoji.Emoji{Key: "Yes", EN: "Yes"}
	require.Empty(t, emojiInfo(emoji.Hit{Emoji: yes, Term: "yes", Positions: []int{0, 1, 2}}),
		"the term the query landed on spells the same word in another case")
}

func TestEmojiInfo_KeepsWhatSaysSomethingTheNameDoesNot(t *testing.T) {
	t.Parallel()
	up := emoji.Emoji{Key: "THUMBSUP", EN: "赞"}
	require.Equal(t, "THUMBSUP", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up}), "\n")))
	require.Equal(t, "THUMBSUP\ndianzan", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up, Term: "dianzan", Positions: []int{0}}), "\n")),
		"the pinyin the query reached it through is why this hit came back")
	require.Equal(t, "THUMBSUP", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up, Term: "thumbsup", Positions: []int{0}}), "\n")),
		"a term that spells the key is already in the box")
}

func TestEmojiInfo_LeavesTheCharacterToTheIconColumn(t *testing.T) {
	t.Parallel()
	up := emoji.Emoji{Key: "THUMBSUP", Glyph: "👍", EN: "赞"}
	require.Equal(t, "THUMBSUP", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up, Term: "👍", Positions: []int{0}}), "\n")),
		"a query that landed on the character repeats what the icon column already draws")
}
