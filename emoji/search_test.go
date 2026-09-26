package emoji

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func first(t *testing.T, hits []Hit) Emoji {
	t.Helper()
	require.NotEmpty(t, hits)
	return hits[0].Emoji
}

func TestSearch_ReachesAnEmojiByPinyinAndByItsInitials(t *testing.T) {
	ix := NewReactionIndex()
	require.Equal(t, "THUMBSUP", first(t, ix.Search("zan")).Key)
	require.Equal(t, "THUMBSUP", first(t, ix.Search("dz")).Key, "点赞 by its initials")
	require.Equal(t, "MUSCLE", first(t, ix.Search("jiayou")).Key)
	require.Equal(t, "ROSE", first(t, ix.Search("玫瑰")).Key, "the Chinese name itself still works")
	require.Equal(t, "LGTM", first(t, ix.Search("lgtm")).Key, "so does the English one")
}

func TestSearch_ReachesAnEnglishNamedEmojiByItsChineseName(t *testing.T) {
	hits := NewReactionIndex().Search("赞")
	require.Equal(t, "THUMBSUP", first(t, hits).Key)
	require.Equal(t, "Like", hits[0].Emoji.Name(), "found by the Chinese name, shown by the English one")
	require.Equal(t, "赞", hits[0].Term, "and the cell says which spelling answered")
}

func TestSearch_NamesNoTermWhenNothingWasTyped(t *testing.T) {
	hits := NewReactionIndex().Search("")
	require.NotEmpty(t, hits)
	require.Empty(t, hits[0].Term, "nothing answered a query nobody made")
	require.Empty(t, hits[0].Positions)
}

func TestSearch_MarksWhereTheQueryLanded(t *testing.T) {
	hits := NewReactionIndex().Search("zan")
	require.Equal(t, "zan", hits[0].Term)
	require.Equal(t, []int{0, 1, 2}, hits[0].Positions, "the picker underlines what matched")
}

func TestSearch_ReadsCaseTheWayFzfDoes(t *testing.T) {
	ix := NewReactionIndex()
	require.NotEmpty(t, ix.Search("okr"), "a lowercase query asks about neither case")
	require.NotEmpty(t, ix.Search("OKR"), "a query with a capital means that capital")
	require.Empty(t, ix.Search("ROSE"), "and so does not reach a term spelled in lowercase")
}

func TestSearch_OffersNothingFeishuWouldRefuse(t *testing.T) {
	ix := NewReactionIndex()
	require.Less(t, ix.Len(), len(All()))
	for _, h := range ix.Search("") {
		require.True(t, h.Emoji.Offerable(), "%s is not an emoji the client names", h.Emoji.Key)
	}
	require.False(t, first(t, ix.Search("zhuiqiujizhi")).Reactable(),
		"another tenant's culture emoji is offered, but as a picture rather than a reaction")
	require.Empty(t, ix.Search("fighting"), "a spelling the client never offers is not offered here")
}

func TestSearch_PutsTheMostUsedFirstWhenNothingIsTyped(t *testing.T) {
	ix := NewReactionIndex()
	require.Equal(t, "OK", first(t, ix.Search("")).Key, "the client's own panel order, until something is used")

	ix.Use("ROSE")
	ix.Use("Coffee")
	hits := ix.Search("")
	require.Equal(t, "Coffee", hits[0].Emoji.Key, "between two used once, the later one")
	require.Equal(t, "ROSE", hits[1].Emoji.Key)

	ix.Use("ROSE")
	require.Equal(t, "ROSE", first(t, ix.Search("")).Key, "using it again moves it up, not into the list twice")
	require.Equal(t, []string{"ROSE", "COFFEE"}, ix.Used())
}

func TestUse_RanksByHowOftenRatherThanHowLately(t *testing.T) {
	// The point of the band: one reach for something rare does not unseat the
	// emoji the reader works out of.
	ix := NewReactionIndex()
	for range 3 {
		ix.Use("THUMBSUP")
	}
	ix.Use("ROSE")
	require.Equal(t, []string{"THUMBSUP", "ROSE"}, ix.Used())
	require.Equal(t, "THUMBSUP", first(t, ix.Search("")).Key)

	// Only once it has been reached for as often does it take the front, and
	// then it is the later of the two.
	for range 2 {
		ix.Use("ROSE")
	}
	require.Equal(t, []string{"ROSE", "THUMBSUP"}, ix.Used())
}

func TestSetUsed_DropsWhatTheTableNoLongerHolds(t *testing.T) {
	// The list outlives the table: an emoji can leave Feishu's set, and a
	// remembered key that no longer resolves must not sort ahead of real ones.
	ix := NewReactionIndex()
	ix.setUsed([]usage{{"ROSE", 3}, {"EMOJI_THAT_LEFT", 9}, {"Lark_Emoji_Coffee_0", 1}})
	require.Equal(t, []string{"ROSE", "COFFEE"}, ix.Used(), "and a folded spelling lands on the same entry")
}

func TestUse_KeepsTheListToWhatAPersonCanHoldInMind(t *testing.T) {
	ix := NewReactionIndex()
	for _, h := range ix.Search("")[:MaxUsed+5] {
		ix.Use(h.Emoji.Key)
	}
	require.Len(t, ix.Used(), MaxUsed)
}

func TestUsed_SurvivesBetweenRuns(t *testing.T) {
	dir := t.TempDir()
	ix := NewReactionIndex()
	ix.Use("ROSE")
	ix.Use("ROSE")
	ix.Use("Coffee")
	require.NoError(t, ix.SaveUsed(dir))

	next := NewReactionIndex()
	next.LoadUsed(dir)
	require.Equal(t, []string{"ROSE", "COFFEE"}, next.Used(), "counts and all, not just the order")
	next.Use("LEMON")
	require.Equal(t, []string{"ROSE", "LEMON", "COFFEE"}, next.Used(),
		"a count that came back from the file still outranks a first use")
}

func TestLoadUsed_LeavesTheListEmptyRatherThanFailing(t *testing.T) {
	// The list is derived data; a first run has no file and a corrupt one is
	// worth no more than a first run.
	ix := NewReactionIndex()
	ix.LoadUsed(t.TempDir())
	require.Empty(t, ix.Used())

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, reactionUsedFile), []byte("not json"), 0o600))
	ix.LoadUsed(dir)
	require.Empty(t, ix.Used())
}
