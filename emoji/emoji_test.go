package emoji

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFold_FoldsTheSpellings(t *testing.T) {
	require.Equal(t, "ROSE", Fold("Lark_Emoji_Rose_0"))
	require.Equal(t, "ROSE", Fold("ROSE"))
	require.Equal(t, "STATUS_PRIVATEMESSAGE", Fold("Status_PrivateMessage"), "a name ending in a word keeps it")
	require.Equal(t, "18X", Fold("18X"))
}

func TestByKey_ResolvesEverySpelling(t *testing.T) {
	e, ok := ByKey("Lark_Emoji_ThumbsUp_0")
	require.True(t, ok)
	require.Equal(t, "THUMBSUP", e.Key)
	require.Equal(t, "👍", e.Glyph)
	require.Equal(t, "赞", e.ZH)

	_, ok = ByKey("lock")
	require.False(t, ok, "a card icon name is not an emoji")
}

func TestByName_ReadsTheNameAMessageCarries(t *testing.T) {
	zh, ok := ByName("赞")
	require.True(t, ok)
	require.Equal(t, "THUMBSUP", zh.Key)

	en, ok := ByName("Like")
	require.True(t, ok, "an English client writes the English name")
	require.Equal(t, "THUMBSUP", en.Key)

	key, ok := ByName("OK")
	require.True(t, ok, "a client with no name for it writes the key")
	require.Equal(t, "OK", key.Key)

	_, ok = ByName("图片")
	require.False(t, ok, "the stand-in the message list draws itself is not an emoji")
}

func TestTable_LeavesOutWhatAPickerMustNotOffer(t *testing.T) {
	tones := regexp.MustCompile(`^(MediumLight|MediumDark|Medium|Light|Dark)\p{L}`)
	year := regexp.MustCompile(`^\d{4}$`)
	for _, e := range table {
		assert.False(t, tones.MatchString(e.Key), "%s is a skin-tone variant of an emoji already listed", e.Key)
		assert.False(t, year.MatchString(e.Key), "%s is a new-year emoji of a year gone by", e.Key)
	}
}

// nonLetter is what a term drops, because no query carries it either.
var nonLetter = regexp.MustCompile(`[^a-z0-9]+`)

func TestTable_CarriesNameImageAndTermsForEveryEmoji(t *testing.T) {
	require.NotEmpty(t, table)
	for _, e := range table {
		assert.NotEmpty(t, e.ZH, "%s has no Chinese name", e.Key)
		assert.NotEmpty(t, e.EN, "%s has no English name", e.Key)
		assert.Positive(t, e.Rect[2], "%s names no rectangle in the sprite", e.Key)
		assert.Positive(t, e.Rect[3], "%s names a rectangle with no height", e.Key)
		assert.NotEmpty(t, e.Terms, "%s can never be searched for", e.Key)
		key := nonLetter.ReplaceAllString(strings.ToLower(e.Key), "")
		assert.Contains(t, e.Terms, key, "%s is not searchable by its own key", e.Key)
	}
}

func TestTerms_CarryPinyinAndItsInitials(t *testing.T) {
	e, ok := ByKey("THUMBSUP")
	require.True(t, ok)
	require.Subset(t, e.Terms, []string{"赞", "zan", "z"}, "the name, its pinyin and its initial all reach it")

	pursue, ok := ByKey("PursueUltimate")
	require.True(t, ok)
	require.Subset(t, pursue.Terms, []string{"zhuiqiujizhi", "zqjz"}, "a four-character name is reachable by four letters")
	require.True(t, pursue.NoReaction, "another tenant's culture emoji cannot be reacted with")
}

// A glyph for a key the client no longer ships is dead weight that nothing
// will ever look up, so the table is what bounds the hand-kept column.
func TestGlyphs_HoldNoKeyTheClientStoppedShipping(t *testing.T) {
	// Spellings Feishu sends in message text without offering them as emoji.
	beyondTable := map[string]bool{"FIGHTING": true, "GRIN": true, "OKHAND": true}
	inTable := map[string]bool{}
	for _, e := range table {
		inTable[Fold(e.Key)] = true
	}
	for key := range glyphs {
		assert.True(t, inTable[key] || beyondTable[key], "%s is in neither the client's table nor the spellings it sends", key)
	}
}

func TestSummary_ReadsTheCountFeishuSendsAsAString(t *testing.T) {
	chips := Summary(`{"counts":[{"reaction_type":"THUMBSUP","count":"3"},{"reaction_type":"OK","count":1}]}`, "")
	require.Equal(t, []Chip{{Key: "THUMBSUP", Count: 3}, {Key: "OK", Count: 1}}, chips)
}

func TestSummary_MarksTheReadersOwnReaction(t *testing.T) {
	const block = `{"counts":[{"reaction_type":"THUMBSUP","count":"2"},{"reaction_type":"ROSE","count":"1"}],
	  "details":[{"emoji_type":"THUMBSUP","operator":{"operator_id":"ou_a","operator_type":"user"}},
	             {"emoji_type":"ROSE","operator":{"operator_id":"ou_b","operator_type":"user"}}]}`
	require.Equal(t, []Chip{{Key: "THUMBSUP", Count: 2, Mine: true}, {Key: "ROSE", Count: 1}}, Summary(block, "ou_a"))
	require.Equal(t, []Chip{{Key: "THUMBSUP", Count: 2}, {Key: "ROSE", Count: 1}}, Summary(block, "ou_c"),
		"a reader who reacted to neither owns neither")
}

func TestSummary_YieldsNothingRatherThanAGuess(t *testing.T) {
	require.Nil(t, Summary("", "ou_a"), "a message with no reactions carries no block")
	require.Nil(t, Summary("not json", "ou_a"), "a block that does not decode is not guessed at")
	require.Empty(t, Summary(`{"counts":[{"reaction_type":"","count":"1"},{"reaction_type":"OK","count":"0"}]}`, "ou_a"),
		"a nameless or emptied count is no reaction")
}

func TestByKey_ReadsASkinToneAsTheEmojiItIsAToneOf(t *testing.T) {
	// The client's picker offers only the default tone, but Feishu takes and
	// sends every one of them, so a reaction arrives under a key the table
	// deliberately leaves out.
	for _, key := range []string{"DarkThumbsup", "MediumLightThumbsup", "LightFistBump", "MediumDarkApplaud"} {
		e, ok := ByKey(key)
		require.True(t, ok, "%s resolves to the emoji it is a tone of", key)
		require.NotEmpty(t, e.Glyph, "%s draws as %s", key, e.Key)
	}
	thumb, _ := ByKey("DarkThumbsup")
	require.Equal(t, "THUMBSUP", thumb.Key)
	applaud, _ := ByKey("MediumDarkApplaud")
	require.Equal(t, "APPLAUSE", applaud.Key, "the tone's key and the emoji's key are spelled differently")
}

func TestToneVariants_AllLandOnAnEmojiTheTableHolds(t *testing.T) {
	for variant, base := range toneVariants {
		_, ok := byKey()[base]
		assert.True(t, ok, "%s is a tone of %s, which is not in the table", variant, base)
	}
}

func TestGlyphs_LeaveTheUnfaithfulOnesToThePicture(t *testing.T) {
	// A near-miss character is worse than no character: it says the wrong
	// thing with full confidence, where a blank falls through to the client's
	// own picture, or failing that to the client's own name.
	for _, key := range []string{
		"SLIGHT", "WITTY", "SCOWL", "HUSKY", "LUCK", // the character means something else
		"OK", "OKR", "No", "DONE", "MinusOne", // Feishu draws a word, Unicode offers a shape
		"CheckMark", "CrossMark", "Music", "GeneralDoNotDisturb", // no colour of its own
	} {
		e, ok := ByKey(key)
		require.True(t, ok, "%s is not in the table", key)
		assert.Empty(t, e.Glyph, "%s (%s) has no faithful character", key, e.ZH)
		assert.Positive(t, e.Rect[2], "%s must have a picture to fall through to", key)
	}
}

func TestGlyphs_LetNoTwoEmojiWearTheSameCharacter(t *testing.T) {
	// Two emoji drawn the same way is a reaction strip that cannot say which
	// one a colleague chose.
	seen := map[string]Emoji{}
	for _, e := range All() {
		if e.Glyph == "" || !e.Reactable() {
			continue
		}
		was, clash := seen[e.Glyph]
		assert.False(t, clash, "%s (%s) and %s (%s) are both drawn %s",
			was.Key, was.ZH, e.Key, e.ZH, e.Glyph)
		seen[e.Glyph] = e
	}
}
