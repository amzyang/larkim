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
		require.True(t, h.Emoji.Reactable(), "%s cannot be put on a message", h.Emoji.Key)
	}
	require.Empty(t, ix.Search("zhuiqiujizhi"), "another tenant's culture emoji is not offered")
	require.Empty(t, ix.Search("fighting"), "a spelling the client never offers is not either")
}

func TestSearch_PutsTheLastUsedFirstWhenNothingIsTyped(t *testing.T) {
	ix := NewReactionIndex()
	require.Equal(t, "OK", first(t, ix.Search("")).Key, "the client's own panel order, until something is used")

	ix.Use("ROSE")
	ix.Use("Coffee")
	hits := ix.Search("")
	require.Equal(t, "Coffee", hits[0].Emoji.Key, "the newest first")
	require.Equal(t, "ROSE", hits[1].Emoji.Key)

	ix.Use("ROSE")
	require.Equal(t, "ROSE", first(t, ix.Search("")).Key, "using it again moves it back to the front, not into the list twice")
	require.Equal(t, []string{"ROSE", "COFFEE"}, ix.Recent())
}

func TestSetRecent_DropsWhatTheTableNoLongerHolds(t *testing.T) {
	// The list outlives the table: an emoji can leave Feishu's set, and a
	// remembered key that no longer resolves must not sort ahead of real ones.
	ix := NewReactionIndex()
	ix.SetRecent([]string{"ROSE", "EMOJI_THAT_LEFT", "Lark_Emoji_Coffee_0"})
	require.Equal(t, []string{"ROSE", "COFFEE"}, ix.Recent(), "and a folded spelling lands on the same entry")
}

func TestUse_KeepsTheListToWhatAPersonCanHoldInMind(t *testing.T) {
	ix := NewReactionIndex()
	for _, h := range ix.Search("")[:MaxRecent+5] {
		ix.Use(h.Emoji.Key)
	}
	require.Len(t, ix.Recent(), MaxRecent)
}

func TestRecent_SurvivesBetweenRuns(t *testing.T) {
	dir := t.TempDir()
	ix := NewReactionIndex()
	ix.Use("ROSE")
	ix.Use("Coffee")
	require.NoError(t, ix.SaveRecent(dir))

	next := NewReactionIndex()
	next.LoadRecent(dir)
	require.Equal(t, []string{"COFFEE", "ROSE"}, next.Recent())
}

func TestLoadRecent_LeavesTheListEmptyRatherThanFailing(t *testing.T) {
	// The list is derived data; a first run has no file and a corrupt one is
	// worth no more than a first run.
	ix := NewReactionIndex()
	ix.LoadRecent(t.TempDir())
	require.Empty(t, ix.Recent())

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, reactionRecentFile), []byte("not json"), 0o600))
	ix.LoadRecent(dir)
	require.Empty(t, ix.Recent())
}
