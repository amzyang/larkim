package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestRenderInline_KeepsLabelsAndDropsTargets(t *testing.T) {
	ms := mentionsIn("", "")
	require.Equal(t, "见 开放日详情 和 重点", ansi.Strip(renderInline("见 [开放日详情](https://example.com/x) 和 **重点**", ms)))
	require.Equal(t, "下划线", ansi.Strip(renderInline("<u>下划线</u>", ms)))
	require.Equal(t, "https://example.com/x", ansi.Strip(renderInline("[](https://example.com/x)", ms)),
		"a link with no label falls back to its target")
	require.Equal(t, "日志 [ObjectMapperUtils:82] 原样", ansi.Strip(renderInline("日志 [ObjectMapperUtils:82] 原样", ms)),
		"brackets that are not a link are text")
}

func TestRenderInline_StylesTheRunsAPostComesBackAs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"看 **粗体** 吧", "看 粗体 吧"},
		{"看 *斜体* 吧", "看 斜体 吧"},
		{"看 ~~删除~~ 吧", "看 删除 吧"},
		{"跑 `go test` 吧", "跑 go test 吧"},
		{"看 <u>下划线</u> 吧", "看 下划线 吧"},
	} {
		require.Equal(t, tc.want, ansi.Strip(renderInline(tc.in, mentionsIn("", ""))), "in %q", tc.in)
	}
}

func TestRenderInline_LeavesOrdinaryProseAlone(t *testing.T) {
	// A lone asterisk between spaces is arithmetic, not emphasis, and an
	// underscore is part of an identifier.
	for _, in := range []string{"3 * 4 * 5", "路径是 user_id_map", "评分 4*"} {
		require.Equal(t, in, ansi.Strip(renderInline(in, mentionsIn("", ""))), "in %q", in)
	}
}

func TestRenderInline_BoldWinsOverItalic(t *testing.T) {
	require.Equal(t, "粗体", ansi.Strip(renderInline("**粗体**", mentionsIn("", ""))),
		"a doubled asterisk is bold, not an italic wrapping an asterisk")
}

// dottedMark and markColour are the two parameters a searched-for word is
// drawn under: the dotted underline in the accent colour, which is also what
// a filter match on a name wears. They are matched apart because lipgloss
// folds them into whatever SGR the run already carried — a dim sender name
// opens the same run with its own colour in front.
const (
	dottedMark = "4:4m"
	markColour = "58;5;4"
)

func TestHitPositions_MarksEveryTermCaseInsensitively(t *testing.T) {
	require.Equal(t, []int{0, 1, 2}, hitPositions("Feishu", []string{"fei"}))
	require.Equal(t, []int{2, 3}, hitPositions("发布计划已定", []string{"计划"}),
		"positions are runes, not bytes")
	require.Nil(t, hitPositions("发布计划", nil), "no terms, no marks")
	require.Nil(t, hitPositions("发布计划", []string{"周四"}), "a term that is not there marks nothing")
}

func TestHitPositions_TermsThatOverlapMarkOnce(t *testing.T) {
	pos := hitPositions("abcd", []string{"abc", "bcd"})
	require.ElementsMatch(t, []int{0, 1, 2, 1, 2, 3}, pos,
		"each term reports its own runes; markName folds the repeats into one run")
	require.Equal(t, "a\x1b[4;58;5;4;4:4mb\x1b[m", markName("ab", hitPositions("ab", []string{"b", "b"}), lipgloss.NewStyle()),
		"a rune two terms both land on is drawn once")
}

func TestHitPositions_FindsEveryOccurrence(t *testing.T) {
	require.Equal(t, []int{0, 3}, hitPositions("a_ba", []string{"a"}))
}

func TestMarkName_UsesTheDottedUnderline(t *testing.T) {
	out := markName("发布计划", hitPositions("发布计划", []string{"计划"}), lipgloss.NewStyle())
	require.Contains(t, out, markColour)
	require.Contains(t, out, dottedMark,
		"a mark larkim put on the text is dotted, so it is not read as the text's own underline")
	require.Equal(t, "发布计划", ansi.Strip(out))
}

func TestRenderInline_NestedRunsCompose(t *testing.T) {
	// A rich-text element carries every style it was given at once, and the
	// markup that spells it back nests. Each layer has to reach the words
	// rather than stopping at the markup of the layer below.
	ms := mentionsIn("", "")
	for _, in := range []string{
		"~~<u>***abcd***</u>~~", "**<u>abcd</u>**", "~~**abcd**~~", "<u>**abcd**</u>", "***abcd***",
	} {
		require.Equal(t, "abcd", ansi.Strip(renderInline(in, ms)), "in %q", in)
	}

	all := renderInline("~~<u>***abcd***</u>~~", ms)
	want := lipgloss.NewStyle().Bold(true).Italic(true).Underline(true).Strikethrough(true).Render("abcd")
	require.Equal(t, want, all, "every style the nesting names reaches the words")
}

func TestRenderInline_ATripleAsteriskIsBoldAndItalic(t *testing.T) {
	require.Equal(t, lipgloss.NewStyle().Bold(true).Italic(true).Render("abcd"),
		renderInline("***abcd***", mentionsIn("", "")))
}
