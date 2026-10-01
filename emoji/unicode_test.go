package emoji

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnicode_IsNeverOfferedAsAReaction(t *testing.T) {
	require.NotEmpty(t, Unicode())
	for _, e := range Unicode() {
		assert.False(t, e.Reactable(), e.Key)
		assert.NotEmpty(t, e.Glyph, e.Key)
		assert.NotEmpty(t, e.EN, e.Key)
	}
}

func TestUnicode_StandsBesideFeishusOwnRatherThanOverIt(t *testing.T) {
	for _, e := range Unicode() {
		_, taken := ByKey(e.Key)
		assert.False(t, taken, e.Key, "a key Feishu already spells would shadow its own emoji")
	}
}

func TestUnicode_LeavesACharacterFeishuDrawsToFeishu(t *testing.T) {
	// 👋 is Feishu's WAVE, which can also be a reaction; its tones are not
	// Feishu's, so they stay.
	keys := map[string]bool{}
	for _, e := range Unicode() {
		keys[e.Key] = true
	}
	assert.False(t, keys["waving_hand"])
	assert.True(t, keys["waving_hand_medium_skin_tone"])
}

func TestUnicode_KeysAreDistinctOnceFolded(t *testing.T) {
	seen := map[string]string{}
	for _, e := range Unicode() {
		k := Fold(e.Key)
		require.NotContains(t, seen, k, "%s and %s fold to one key", seen[k], e.Key)
		seen[k] = e.Key
	}
}

func TestUnicode_CarriesChineseNamesForTheBaseForms(t *testing.T) {
	rocket := find(t, Unicode(), "rocket")
	assert.Equal(t, "火箭", rocket.ZH)
	assert.Equal(t, "🚀", rocket.Glyph)
}

func TestComposerIndex_ReachesBothSetsAndTheReactionOneDoesNot(t *testing.T) {
	write, react := NewComposerIndex(), NewReactionIndex()

	for _, query := range []string{"rocket", "huojian", "火箭", "🚀"} {
		require.Equal(t, "rocket", first(t, write.Search(query)).Key, query)
	}
	require.Equal(t, "THUMBSUP", first(t, write.Search("dianzan")).Key)
	require.Equal(t, "THUMBSUP", first(t, react.Search("dianzan")).Key)
	require.False(t, slices.ContainsFunc(react.Search("rocket"), func(h Hit) bool {
		return h.Emoji.Key == "rocket"
	}), "Feishu would refuse it, so the picker must not offer it")
}

func TestComposerIndex_ABaseFormLeadsItsTones(t *testing.T) {
	hits := NewComposerIndex().Search("raisedhand")
	require.NotEmpty(t, hits)
	assert.Equal(t, "raised_hand", hits[0].Emoji.Key)
}

func TestComposerIndex_FeishuLeadsOnItsOwnNames(t *testing.T) {
	// Thousands of Unicode rows sit behind Feishu's couple of hundred; a
	// name Feishu answers must still be answered by Feishu first.
	ix := NewComposerIndex()
	for query, want := range map[string]string{
		"dianzan": "THUMBSUP",
		"guzhang": "APPLAUSE",
		"gz":      "APPLAUSE",
		"smile":   "SMILE",
		"meigui":  "ROSE",
	} {
		got := first(t, ix.Search(query))
		assert.Equal(t, want, got.Key, query)
	}
}

func TestComposerIndex_AnEmptyQueryOpensOnFeishusPanel(t *testing.T) {
	hits := NewComposerIndex().Search("")
	require.NotEmpty(t, hits)
	assert.True(t, hits[0].Emoji.Reactable(), hits[0].Emoji.Key)
}

func TestUsed_TheTwoIndexesAreRememberedApart(t *testing.T) {
	dir := t.TempDir()
	write, react := NewComposerIndex(), NewReactionIndex()

	write.Use("rocket")
	react.Use("THUMBSUP")
	require.NoError(t, write.SaveUsed(dir))
	require.NoError(t, react.SaveUsed(dir))

	// Sharing one file would cost the composer its Unicode keys, which the
	// reaction index drops because it does not hold them.
	back := NewComposerIndex()
	back.LoadUsed(dir)
	require.Equal(t, []string{"ROCKET"}, back.Used())

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 2)
	for _, f := range files {
		require.FileExists(t, filepath.Join(dir, f.Name()))
	}
}

func find(t *testing.T, all []Emoji, key string) Emoji {
	t.Helper()
	i := slices.IndexFunc(all, func(e Emoji) bool { return e.Key == key })
	require.GreaterOrEqual(t, i, 0, key)
	return all[i]
}
