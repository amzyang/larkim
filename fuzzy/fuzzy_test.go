package fuzzy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerms_SpellsNameQuanpinAndInitials(t *testing.T) {
	require.Equal(t, []string{"平台组", "pingtaizu", "ptz"}, Terms("平台组"))
}

func TestTerms_KeepsRunesWithoutAReading(t *testing.T) {
	// The V and the 5 have no pinyin, so they stay where they are and the
	// term remains the whole name rather than its Chinese part.
	require.Equal(t, []string{"V5项目", "v5xiangmu", "v5xm"}, Terms("V5项目"))
}

func TestTerms_LeavesALatinNameAlone(t *testing.T) {
	require.Equal(t, []string{"build-bot"}, Terms("build-bot"))
}

func TestTerms_DropsTheEmptyName(t *testing.T) {
	require.Empty(t, Terms(""))
	require.Empty(t, Terms("   "))
}

func TestMatcher_BestPicksHighestScoringTerm(t *testing.T) {
	mt := NewMatcher()
	terms := Chars([]string{"平台组", "pingtaizu", "ptz"})

	i, score, pos := mt.Best(terms, "ptz")
	require.Positive(t, score)
	require.Equal(t, 2, i, "the initials term is the one ptz belongs to")
	require.Equal(t, []int{0, 1, 2}, pos)

	i, score, _ = mt.Best(terms, "台组")
	require.Positive(t, score)
	require.Equal(t, 0, i)
}

func TestMatcher_BestReportsNoScoreWhenNothingMatches(t *testing.T) {
	mt := NewMatcher()
	_, score, _ := mt.Best(Chars([]string{"平台组", "pingtaizu", "ptz"}), "zzzz")
	require.Zero(t, score)
}

func TestMatcher_SmartCaseRespectsACapital(t *testing.T) {
	mt := NewMatcher()
	terms := Chars([]string{"build-bot"})

	_, lower, _ := mt.Best(terms, "bot")
	require.Positive(t, lower, "a lowercase query asks about neither case")

	_, upper, _ := mt.Best(terms, "BOT")
	require.Zero(t, upper, "a capital in the query means that capital")
}

func TestMatcher_BestHandlesAnEmptyTermList(t *testing.T) {
	mt := NewMatcher()
	i, score, pos := NewMatcher().Best(nil, "x")
	require.Equal(t, -1, i)
	require.Zero(t, score)
	require.Nil(t, pos)
	_ = mt
}

func TestMatch_ChineseQueryMatchesARunNotASubsequence(t *testing.T) {
	ix := NewIndex()
	pos, ok := ix.Match("oc_a", "部门年会邀请函", "年会")
	require.True(t, ok)
	require.Equal(t, []int{2, 3}, pos)

	// The 年 and the 会 of this name are eleven runes apart: a subsequence
	// match would take it, and a reader looking for 年会 would not.
	_, ok = ix.Match("oc_b", "2026年高途必赢新春启动会", "年会")
	require.False(t, ok)
}

func TestMatch_LatinQueryKeepsTheSubsequenceMatch(t *testing.T) {
	ix := NewIndex()
	_, ok := ix.Match("oc_a", "平台组", "ptz")
	require.True(t, ok, "pinyin initials are a subsequence of the spelling")
}

func TestMatch_ChineseQueryIsCaseFoldedAndAnchoredAnywhere(t *testing.T) {
	ix := NewIndex()
	pos, ok := ix.Match("oc_a", "AIGC市场自营", "市场")
	require.True(t, ok)
	require.Equal(t, []int{4, 5}, pos)
}

func TestHasCJK(t *testing.T) {
	require.True(t, HasCJK("年会"))
	require.True(t, HasCJK("v5项目"))
	require.False(t, HasCJK("ptz"))
	require.False(t, HasCJK(""))
}
