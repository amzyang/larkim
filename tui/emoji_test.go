package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestExpandEmoji_DrawsOfficialEmojiAndLeavesTheRest(t *testing.T) {
	require.Equal(t, "好的👍", ansi.Strip(expandEmoji("好的:THUMBSUP:")))
	require.Equal(t, "✅👍👌", ansi.Strip(expandEmoji(":DONE::THUMBSUP::OK:")), "a run of emoji all expand")
	require.Equal(t, "心碎💔", ansi.Strip(expandEmoji("心碎:Lark_Emoji_HeartBroken_0:")), "the Lark_Emoji_ spelling is the same emoji")
	require.Equal(t, "[JIAYI]", ansi.Strip(expandEmoji(":JIAYI:")), "an emoji with no glyph is named instead")
	require.Equal(t, "图标 :lock: 保留", ansi.Strip(expandEmoji("图标 :lock: 保留")), "a card icon name is not an emoji")
	require.Equal(t, "10:30:00", ansi.Strip(expandEmoji("10:30:00")), "a clock time is not an emoji")
}

func TestEmojiKey_FoldsTheSpellings(t *testing.T) {
	require.Equal(t, "ROSE", emojiKey("Lark_Emoji_Rose_0"))
	require.Equal(t, "ROSE", emojiKey("ROSE"))
	require.Equal(t, "STATUS_PRIVATEMESSAGE", emojiKey("Status_PrivateMessage"), "a name ending in a word keeps it")
	require.Equal(t, "18X", emojiKey("18X"))
}

func TestExpandEmoji_ReadsTheBracketedNameATextMessageCarries(t *testing.T) {
	require.Equal(t, "好的👌", ansi.Strip(expandEmoji("好的[OK]")), "an English client writes the key")
	require.Equal(t, "谢谢🙏👍", ansi.Strip(expandEmoji("谢谢[双手合十][赞]")), "a Chinese client writes the name")
	require.Equal(t, "点 [查看详情] 按钮", ansi.Strip(expandEmoji("点 [查看详情] 按钮")), "a bracketed noun is not an emoji")
	require.Equal(t, "[图片]", ansi.Strip(expandEmoji("[图片]")), "the stand-ins this list draws itself stay")
}
