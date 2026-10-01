package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/emoji"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestExpandEmoji_DrawsOfficialEmojiAndLeavesTheRest(t *testing.T) {
	require.Equal(t, "好的👍", ansi.Strip(expandEmoji("好的:THUMBSUP:", spellShortcode)))
	require.Equal(t, "🙏👍🌹", ansi.Strip(expandEmoji(":THANKS::THUMBSUP::ROSE:", spellShortcode)), "a run of emoji all expand")
	require.Equal(t, "心碎💔", ansi.Strip(expandEmoji("心碎:Lark_Emoji_HeartBroken_0:", spellShortcode)), "the Lark_Emoji_ spelling is the same emoji")
	require.Equal(t, ":JIAYI:", ansi.Strip(expandEmoji(":JIAYI:", spellShortcode)), "an emoji with no glyph keeps its key")
	require.Equal(t, ":DONE:", ansi.Strip(expandEmoji(":DONE:", spellShortcode)), "the Feishu client has no bracketed spelling either")
	require.Equal(t, "图标 :lock: 保留", ansi.Strip(expandEmoji("图标 :lock: 保留", spellShortcode)), "a card icon name is not an emoji")
	require.Equal(t, "10:30:00", ansi.Strip(expandEmoji("10:30:00", spellShortcode)), "a clock time is not an emoji")
}

func TestExpandEmoji_ReadsTheBracketedNameATextMessageCarries(t *testing.T) {
	require.Equal(t, "谢谢🙏", ansi.Strip(expandEmoji("谢谢[THANKS]", spellBracket)), "an English client writes the key")
	require.Equal(t, "谢谢🙏👍", ansi.Strip(expandEmoji("谢谢[双手合十][赞]", spellBracket)), "a Chinese client writes the name")
	require.Equal(t, "点 [查看详情] 按钮", ansi.Strip(expandEmoji("点 [查看详情] 按钮", spellBracket)), "a bracketed noun is not an emoji")
	require.Equal(t, "[Image]", ansi.Strip(expandEmoji("[Image]", spellBracket)), "the stand-ins this list draws itself stay")
}

func TestEmojiInfo_DropsAPieceThatRepeatsTheName(t *testing.T) {
	// Feishu's lettering emoji spell their own name, and the term a query
	// landed on may spell it again; the row already draws it once.
	ok := emoji.Emoji{Key: "OK", EN: "OK"}
	require.Empty(t, emojiInfo(emoji.Hit{Emoji: ok}))
	yes := emoji.Emoji{Key: "Yes", EN: "Yes"}
	require.Empty(t, emojiInfo(emoji.Hit{Emoji: yes, Term: "yes", Positions: []int{0, 1, 2}}),
		"the term the query landed on spells the same word in another case")
}

func TestEmojiInfo_KeepsWhatSaysSomethingTheNameDoesNot(t *testing.T) {
	up := emoji.Emoji{Key: "THUMBSUP", EN: "赞"}
	require.Equal(t, "THUMBSUP", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up}), "\n")))
	require.Equal(t, "THUMBSUP\ndianzan", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up, Term: "dianzan", Positions: []int{0}}), "\n")),
		"the pinyin the query reached it through is why this hit came back")
	require.Equal(t, "THUMBSUP", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up, Term: "thumbsup", Positions: []int{0}}), "\n")),
		"a term that spells the key is already in the box")
}

func TestEmojiInfo_LeavesTheCharacterToTheIconColumn(t *testing.T) {
	up := emoji.Emoji{Key: "THUMBSUP", Glyph: "👍", EN: "赞"}
	require.Equal(t, "THUMBSUP", ansi.Strip(strings.Join(emojiInfo(emoji.Hit{Emoji: up, Term: "👍", Positions: []int{0}}), "\n")),
		"a query that landed on the character repeats what the icon column already draws")
}
