package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestExpandEmoji_DrawsOfficialEmojiAndLeavesTheRest(t *testing.T) {
	require.Equal(t, "好的👍", ansi.Strip(expandEmoji("好的:THUMBSUP:")))
	require.Equal(t, "🙏👍🌹", ansi.Strip(expandEmoji(":THANKS::THUMBSUP::ROSE:")), "a run of emoji all expand")
	require.Equal(t, "心碎💔", ansi.Strip(expandEmoji("心碎:Lark_Emoji_HeartBroken_0:")), "the Lark_Emoji_ spelling is the same emoji")
	require.Equal(t, ":JIAYI:", ansi.Strip(expandEmoji(":JIAYI:")), "an emoji with no glyph keeps its key")
	require.Equal(t, ":DONE:", ansi.Strip(expandEmoji(":DONE:")), "the Feishu client has no bracketed spelling either")
	require.Equal(t, "图标 :lock: 保留", ansi.Strip(expandEmoji("图标 :lock: 保留")), "a card icon name is not an emoji")
	require.Equal(t, "10:30:00", ansi.Strip(expandEmoji("10:30:00")), "a clock time is not an emoji")
}

func TestExpandEmoji_ReadsTheBracketedNameATextMessageCarries(t *testing.T) {
	require.Equal(t, "谢谢🙏", ansi.Strip(expandEmoji("谢谢[THANKS]")), "an English client writes the key")
	require.Equal(t, "谢谢🙏👍", ansi.Strip(expandEmoji("谢谢[双手合十][赞]")), "a Chinese client writes the name")
	require.Equal(t, "点 [查看详情] 按钮", ansi.Strip(expandEmoji("点 [查看详情] 按钮")), "a bracketed noun is not an emoji")
	require.Equal(t, "[图片]", ansi.Strip(expandEmoji("[图片]")), "the stand-ins this list draws itself stay")
}

func TestEmojiWords_DropsAPieceThatRepeatsOneAlreadyDrawn(t *testing.T) {
	// Feishu's lettering emoji spell their own name, and their picture spells
	// it a third time; the cell says it once.
	require.Equal(t, "OK", ansi.Strip(emojiWords("OK", "OK", "OK", "", nil)))
	require.Equal(t, "Yes", ansi.Strip(emojiWords("Yes", "Yes", "Yes", "yes", []int{0, 1, 2})),
		"the term the query landed on spells the same word in another case")
	require.Equal(t, "Yes ✓", ansi.Strip(emojiWords("Yes", "Yes", "Yes ✓", "", nil)),
		"what the caller drew onto the name stays")
}

func TestEmojiWords_KeepsWhatSaysSomethingTheNameDoesNot(t *testing.T) {
	require.Equal(t, "THUMBSUP 赞", ansi.Strip(emojiWords("THUMBSUP", "赞", "赞", "", nil)))
	require.Equal(t, "THUMBSUP 赞 dianzan", ansi.Strip(emojiWords("THUMBSUP", "赞", "赞", "dianzan", []int{0})),
		"the pinyin the query reached it through is why this hit came back")
	require.Equal(t, "THUMBSUP 赞", ansi.Strip(emojiWords("THUMBSUP", "赞", "赞", "thumbsup", []int{0})),
		"a term that spells the key is already on the line")
}
