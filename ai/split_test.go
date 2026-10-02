package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitAnswer_AnAnswerWithNoBlockIsCommentary(t *testing.T) {
	require.Equal(t, []Segment{{Card: false, Text: "今晚合 #4412，风险已过。"}},
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
	// The fenced pair opens no card, so the answer is commentary only.
	segs := SplitAnswer("模板长这样：\n```\n<reply>\n占位\n</reply>\n```\n按它填即可。")
	require.Equal(t, []Segment{
		{Card: false, Text: "模板长这样：\n```\n<reply>\n占位\n</reply>\n```\n按它填即可。"},
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

func TestSplitAnswer_AnUnbalancedFenceInABlockDoesNotEatTheRest(t *testing.T) {
	// The option's own ``` never balances; the closing marker still closes,
	// and the second option behind it is still an option.
	segs := SplitAnswer("两个：\n<reply>\n```\ncode\n</reply>\n或者：\n<reply>\n明早合。\n</reply>")
	require.Equal(t, []Segment{
		{Card: false, Text: "两个："},
		{Card: true, Text: "```\ncode"},
		{Card: false, Text: "或者："},
		{Card: true, Text: "明早合。"},
	}, segs)
}

func TestSplitAnswer_ABlockOnOneLineIsStillACard(t *testing.T) {
	segs := SplitAnswer("下面是草稿：\n\n<reply>读完了。</reply>\n\n<reply>hello 收到。</reply>")
	require.Equal(t, []Segment{
		{Card: false, Text: "下面是草稿："},
		{Card: true, Text: "读完了。"},
		{Card: true, Text: "hello 收到。"},
	}, segs)
	segs = SplitAnswer("可以这样回 <reply>好的，\n今晚合。</reply> 你看呢")
	require.Equal(t, []Segment{
		{Card: false, Text: "可以这样回"},
		{Card: true, Text: "好的，\n今晚合。"},
		{Card: false, Text: "你看呢"},
	}, segs)
}

func TestSplitAnswer_AMarkerInInlineCodeIsQuotedText(t *testing.T) {
	require.Equal(t, []Segment{{Card: false, Text: "用 `<reply>` 包住选项。"}},
		SplitAnswer("用 `<reply>` 包住选项。"))
}

func TestSplitAnswer_AStrayCloserMakesNoCard(t *testing.T) {
	// A closer with nothing open opens nothing: the text around it stays
	// commentary, and the whole-text fallback does not post the tag.
	require.Equal(t, []Segment{{Card: false, Text: "foo\nbar"}},
		SplitAnswer("foo\n</reply>\nbar"))
}
