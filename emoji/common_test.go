package emoji

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommon_IsNeverOfferedAsAReaction(t *testing.T) {
	for _, e := range Common() {
		assert.False(t, e.Reactable(), e.Key)
		assert.NotEmpty(t, e.Glyph, e.Key, "an emoji with no character is one Feishu alone can draw")
	}
}

func TestCommon_StandsBesideFeishusOwnRatherThanOverIt(t *testing.T) {
	for _, e := range Common() {
		_, taken := ByKey(e.Key)
		assert.False(t, taken, e.Key, "a key Feishu already spells would shadow its own emoji")
	}
}

func TestComposerIndex_ReachesBothSetsAndTheReactionOneDoesNot(t *testing.T) {
	write, react := NewComposerIndex(), NewReactionIndex()

	require.Equal(t, "rocket", write.Search("rocket")[0].Emoji.Key)
	require.Equal(t, "THUMBSUP", write.Search("dianzan")[0].Emoji.Key)
	require.Equal(t, "THUMBSUP", react.Search("dianzan")[0].Emoji.Key)

	require.False(t, slices.ContainsFunc(react.Search("rocket"), func(h Hit) bool {
		return h.Emoji.Key == "rocket"
	}), "Feishu would refuse it, so the picker must not offer it")
}

func TestCommonTerms_ReachAnEmojiThroughPinyinAndAlias(t *testing.T) {
	ix := NewComposerIndex()
	for _, query := range []string{"huojian", "火箭", "shangxian", "launch"} {
		hits := ix.Search(query)
		require.NotEmpty(t, hits, query)
		assert.Equal(t, "rocket", hits[0].Emoji.Key, query)
	}
	// Initials that spell more than one emoji are answered by all of them, and
	// Feishu's own lead on its panel order: these are the extras, not a set
	// that outranks the client's.
	require.True(t, slices.ContainsFunc(ix.Search("hj"), func(h Hit) bool {
		return h.Emoji.Key == "rocket"
	}))
}

func TestRecent_TheTwoIndexesAreRememberedApart(t *testing.T) {
	dir := t.TempDir()
	write, react := NewComposerIndex(), NewReactionIndex()

	write.Use("rocket")
	react.Use("THUMBSUP")
	require.NoError(t, write.SaveRecent(dir))
	require.NoError(t, react.SaveRecent(dir))

	// Sharing one file would cost the composer its Unicode keys, which the
	// reaction index drops because it does not hold them.
	back := NewComposerIndex()
	back.LoadRecent(dir)
	require.Equal(t, []string{"ROCKET"}, back.Recent())

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 2)
	for _, f := range files {
		require.FileExists(t, filepath.Join(dir, f.Name()))
	}
}
