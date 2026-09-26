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

func TestName_IsTheOneTheClientShows(t *testing.T) {
	e, ok := ByKey("THUMBSUP")
	require.True(t, ok)
	require.Equal(t, "Like", e.Name(), "the client here runs in English")

	bare, ok := ByKey("OKHAND")
	require.True(t, ok)
	require.Equal(t, "OKHAND", bare.Name(), "a spelling the client never names stands for itself")
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

// The client withdrew these; Feishu answers a reaction with them "reaction
// type is invalid". They stay in the sprite because an old message still draws
// them, and stay in the picker because choosing one sends it as a picture.
func TestReactable_LeavesOutTheEmojiTheClientWithdrew(t *testing.T) {
	ix := NewReactionIndex()
	for key, zh := range map[string]string{"ATTENTION": "来看我", "WELLDONE": "V5", "FOLLOWME": "互粉",
		"DETERGENT": "去污粉", "AWESOME": "666", "GOODJOB": "给力"} {
		e, ok := ByKey(key)
		require.True(t, ok, "%s is still in the sprite, so a chip carrying it still draws", key)
		require.Equal(t, zh, e.ZH)
		require.False(t, e.Reactable(), "%s was withdrawn", key)
		require.True(t, e.Offerable(), "%s is still reachable, as a picture", key)
		require.Contains(t, keysOf(ix.Search(zh)), key, "the picker finds %s by name", key)
	}
}

func keysOf(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Emoji.Key)
	}
	return out
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
	require.Equal(t, []Chip{{Key: "THUMBSUP", Count: 2, Mine: true, Operators: []string{"ou_a"}},
		{Key: "ROSE", Count: 1, Operators: []string{"ou_b"}}}, Summary(block, "ou_a"))
	require.Equal(t, []Chip{{Key: "THUMBSUP", Count: 2, Operators: []string{"ou_a"}},
		{Key: "ROSE", Count: 1, Operators: []string{"ou_b"}}}, Summary(block, "ou_c"),
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

func TestSummary_OrdersEmojiByWhenEachWasFirstUsed(t *testing.T) {
	// Feishu sorts the totals alphabetically. The client lists an emoji from
	// when it was first put on the message, which is an order that never
	// reshuffles as the counts move.
	const block = `{"counts":[{"reaction_type":"DONE","count":"1"},{"reaction_type":"Get","count":"1"},
	    {"reaction_type":"OK","count":"1"},{"reaction_type":"THUMBSUP","count":"1"}],
	  "details":[{"emoji_type":"THUMBSUP","action_time":"1790173022","operator":{"operator_id":"ou_a"}},
	             {"emoji_type":"DONE","action_time":"1790155046","operator":{"operator_id":"ou_a"}},
	             {"emoji_type":"Get","action_time":"1790155044","operator":{"operator_id":"ou_a"}},
	             {"emoji_type":"OK","action_time":"1790155041","operator":{"operator_id":"ou_a"}}]}`
	var keys []string
	for _, c := range Summary(block, "") {
		keys = append(keys, c.Key)
	}
	require.Equal(t, []string{"OK", "Get", "DONE", "THUMBSUP"}, keys)
}

func TestSummary_KeepsAnEmojiWithNoDetailsLast(t *testing.T) {
	// The block carries one page of details, so a busy emoji's reactors can
	// all fall past it and leave nothing to sort it by.
	const block = `{"counts":[{"reaction_type":"ROSE","count":"40"},{"reaction_type":"OK","count":"1"}],
	  "details":[{"emoji_type":"OK","action_time":"1790155041","operator":{"operator_id":"ou_a"}}]}`
	chips := Summary(block, "")
	require.Equal(t, []string{"OK", "ROSE"}, []string{chips[0].Key, chips[1].Key})
	require.Empty(t, chips[1].Operators, "nobody is named for it, and the total is all there is")
}

func TestSummary_NamesTheReactorsEarliestFirst(t *testing.T) {
	// Three reacted, two are on the page: the count stays the server's.
	const block = `{"counts":[{"reaction_type":"OK","count":"3"}],
	  "details":[{"emoji_type":"OK","action_time":"1790155046","operator":{"operator_id":"ou_b"}},
	             {"emoji_type":"OK","action_time":"1790155041","operator":{"operator_id":"ou_me"}}]}`
	require.Equal(t, []Chip{{Key: "OK", Count: 3, Mine: true, Operators: []string{"ou_me", "ou_b"}}},
		Summary(block, "ou_me"))
}

func TestDelisted_TellsAWithdrawnEmojiFromAnotherTenantsCultureOne(t *testing.T) {
	withdrawn, ok := ByKey("AWESOME")
	require.True(t, ok)
	require.True(t, withdrawn.Delisted, "666 is gone from the client, so only its picture still travels")
	require.False(t, withdrawn.Reactable())

	foreign, ok := ByKey("PursueUltimate")
	require.True(t, ok)
	require.False(t, foreign.Delisted, "another tenant's emoji is refused as a reaction but goes inside a message")
	require.False(t, foreign.Reactable())

	ordinary, ok := ByKey("THUMBSUP")
	require.True(t, ok)
	require.False(t, ordinary.Delisted)
	require.True(t, ordinary.Reactable())
}

func TestDelisted_CoversEveryEmojiTheClientHasWithdrawn(t *testing.T) {
	var got []string
	for _, e := range All() {
		if e.Delisted {
			got = append(got, e.Key)
		}
	}
	require.ElementsMatch(t, []string{"ATTENTION", "WELLDONE", "FOLLOWME", "DETERGENT", "AWESOME", "GOODJOB"}, got)
}
