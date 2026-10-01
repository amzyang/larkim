package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitAnswer_AnAnswerWithNoBlockIsOneCard(t *testing.T) {
	require.Equal(t, []Segment{{Card: true, Text: "今晚合 #4412，风险已过。"}},
		SplitAnswer("今晚合 #4412，风险已过。"))
}

func TestSplitAnswer_SeveralBlocksAreSeveralCardsWithCommentaryBetween(t *testing.T) {
	segs := SplitAnswer("两个选择：\n<reply>\n今晚合。\n</reply>\n或者稳一点：\n<reply>\n明早合，先跑回归。\n</reply>")
	require.Equal(t, []Segment{
		{Card: false, Text: "两个选择："},
		{Card: true, Text: "今晚合。"},
		{Card: false, Text: "或者稳一点："},
		{Card: true, Text: "明早合，先跑回归。"},
	}, segs)
}

func TestSplitAnswer_AnUnclosedBlockStreamsAsTheCardItOpened(t *testing.T) {
	segs := SplitAnswer("草稿如下：\n<reply>\n好的，今晚合")
	require.Equal(t, []Segment{
		{Card: false, Text: "草稿如下："},
		{Card: true, Text: "好的，今晚合"},
	}, segs, "a half-arrived card is already the card, so the reader can watch it grow")
}

func TestSplitAnswer_AMarkerInsideACodeFenceIsQuotedTextNotAnOption(t *testing.T) {
	// The fenced pair opens no card, so the answer offers no option at all —
	// which makes it the message itself, one card with the template inside.
	segs := SplitAnswer("模板长这样：\n```\n<reply>\n占位\n</reply>\n```\n按它填即可。")
	require.Equal(t, []Segment{
		{Card: true, Text: "模板长这样：\n```\n<reply>\n占位\n</reply>\n```\n按它填即可。"},
	}, segs)
	// And with a real block after it, the fenced one stays quoted text.
	segs = SplitAnswer("```\n<reply>\n</reply>\n```\n<reply>\n真选项\n</reply>")
	require.Equal(t, []Segment{
		{Card: false, Text: "```\n<reply>\n</reply>\n```"},
		{Card: true, Text: "真选项"},
	}, segs)
}

func TestSplitAnswer_AnEmptyAnswerHasNothing(t *testing.T) {
	require.Empty(t, SplitAnswer(""))
	require.Empty(t, SplitAnswer("  \n  "))
	// Markers alone offer nothing to send and say nothing between.
	require.Equal(t, []Segment{{Card: false, Text: "嗯。"}},
		SplitAnswer("嗯。\n<reply>\n</reply>"))
}
