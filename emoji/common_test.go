package emoji

import (
	"cmp"
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
		// Feishu draws its own emoji from a sprite sheet; an extra has no
		// rectangle in it, so text is the only way it reaches the screen.
		assert.NotEmpty(t, cmp.Or(e.Glyph, e.Insert), e.Key,
			"an extra with neither a character nor its ASCII is one nothing can draw")
		assert.True(t, e.Glyph == "" || e.Insert == "", e.Key,
			"an entry is drawn or written, not both")
	}
}

func TestCommon_StandsBesideFeishusOwnRatherThanOverIt(t *testing.T) {
	for _, e := range Common() {
		_, taken := ByKey(e.Key)
		assert.False(t, taken, e.Key, "a key Feishu already spells would shadow its own emoji")
	}
}

func TestCommon_EveryExtraIsNamedInBothLanguages(t *testing.T) {
	// A half-filled entry costs nothing at build time and everything at the
	// popup, where an emoji is reached by its name or not at all.
	for _, e := range Common() {
		assert.NotEmpty(t, e.ZH, e.Key)
		assert.NotEmpty(t, e.EN, e.Key)
	}
}

func TestCommon_KeepsOneVariantOfACharacter(t *testing.T) {
	// A skin tone or a gendered spelling is the same emoji again: the bare
	// person and the unmodified hand are the forms that go on the list.
	for _, e := range Common() {
		for _, r := range e.Glyph {
			assert.NotContains(t, []rune{0x1F3FB, 0x1F3FC, 0x1F3FD, 0x1F3FE, 0x1F3FF}, r,
				"%s carries a skin tone", e.Key)
		}
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

func TestCommonTerms_ReachTheWorkingVocabularyThisChatIsWrittenIn(t *testing.T) {
	ix := NewComposerIndex()
	for query, want := range map[string]string{
		"paiqi":    "calendar",
		"排期":       "calendar",
		"hotfix":   "bandage",
		"xiaoqu":   "school",
		"chongpao": "repeat",
		"📆":        "calendar",
	} {
		hits := ix.Search(query)
		require.NotEmpty(t, hits, query)
		assert.Equal(t, want, hits[0].Emoji.Key, query)
	}
}

func TestCommon_ReachesAnEmoticonByName(t *testing.T) {
	// The face is not a term — nobody types it — so the words are the whole of
	// how one is found.
	ix := NewComposerIndex()
	for query, want := range map[string]string{
		"xianzhuo":    "tableflip",
		"掀桌":          "tableflip",
		"tableflip":   "tableflip",
		"fuzhuo":      "unflip",
		"disapproval": "disapproval",
	} {
		hits := ix.Search(query)
		require.NotEmpty(t, hits, query)
		assert.Equal(t, want, hits[0].Emoji.Key, query)
	}
}

func TestCommon_YanwenziReachesTheEmoticonsTogether(t *testing.T) {
	// Looking for one of these is usually looking for the style, not the
	// gesture, and no single word covers the four of them.
	var keys []string
	for _, h := range NewComposerIndex().Search("颜文字") {
		keys = append(keys, h.Emoji.Key)
	}
	assert.ElementsMatch(t, []string{"shrug_text", "tableflip", "unflip", "disapproval"}, keys)
}

func TestCommon_FeishusOwnGestureLeadsTheEmoticonOfTheSameName(t *testing.T) {
	// 摊手 is 耸肩摊手 to Feishu, which draws it and takes it as a reaction;
	// the typed one is the second answer, not a competing first.
	hits := NewComposerIndex().Search("摊手")
	require.GreaterOrEqual(t, len(hits), 2)
	assert.Equal(t, "Shrug", hits[0].Emoji.Key)
	assert.True(t, slices.ContainsFunc(hits, func(h Hit) bool { return h.Emoji.Key == "shrug_text" }))
}

func TestCommon_AnEmoticonKeyDoesNotShadowFeishusOwn(t *testing.T) {
	// Feishu draws 🤷 as SHRUG, so the ASCII one cannot be keyed "shrug"
	// without taking that name away from the emoji a reader can react with.
	shrug, ok := ByKey("SHRUG")
	require.True(t, ok)
	assert.Equal(t, "🤷", shrug.Glyph)
	assert.Empty(t, shrug.Insert, "Feishu's own is drawn, not written")
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
