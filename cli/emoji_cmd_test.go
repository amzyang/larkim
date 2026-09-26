package cli

import (
	"slices"
	"testing"

	"github.com/amzyang/larkim/emoji"
	"github.com/stretchr/testify/require"
)

func emojiRow_(t *testing.T, rows []emojiRow, key string) emojiRow {
	t.Helper()
	i := slices.IndexFunc(rows, func(e emojiRow) bool { return e.Key == key })
	require.GreaterOrEqual(t, i, 0, "%s is not in the list", key)
	return rows[i]
}

func TestEmojiRows_LeadWithTheClientsOwnPanelOrder(t *testing.T) {
	rows := emojiRows("/data")
	require.NotEmpty(t, rows)
	require.True(t, slices.IsSortedFunc(rows, func(a, b emojiRow) int { return a.Order - b.Order }),
		"a picker with nothing typed falls back to this order")
}

func TestEmojiRows_SayWhatFeishuWillLetAnEmojiDo(t *testing.T) {
	rows := emojiRows("/data")

	ordinary := emojiRow_(t, rows, "THUMBSUP")
	require.True(t, ordinary.Reactable)
	require.False(t, ordinary.Delisted)

	foreign := emojiRow_(t, rows, "PursueUltimate")
	require.False(t, foreign.Reactable, "another tenant's emoji is refused as a reaction")
	require.False(t, foreign.Delisted, "but a message carries it fine")

	withdrawn := emojiRow_(t, rows, "AWESOME")
	require.False(t, withdrawn.Reactable)
	require.True(t, withdrawn.Delisted, "only its picture still reaches the other side")
}

func TestEmojiRows_CarryTheSearchTermsAndThePicturePath(t *testing.T) {
	e := emojiRow_(t, emojiRows("/data"), "THUMBSUP")
	require.Subset(t, e.Terms, []string{"赞", "zan", "z"}, "the name, its pinyin and its initial all reach it")
	require.Equal(t, emoji.Path("/data", "THUMBSUP"), e.Picture)
	require.Equal(t, "赞", e.ZH)
}

func TestEmojiRows_LeaveOutTheBareSpellingsAPickerCannotDraw(t *testing.T) {
	// glyphs.go carries spellings with no name to search by and no rectangle
	// to cut a picture from; a message can name one but a picker cannot list it.
	rows := emojiRows("/data")
	for _, e := range rows {
		require.NotEmpty(t, e.EN, "%s has no name", e.Key)
	}
	require.Less(t, len(rows), len(emoji.All()))
}

func TestEmojiFlags_NameTheOneThingThatIsTrue(t *testing.T) {
	require.Equal(t, "", emojiFlags(emojiRow{Reactable: true}))
	require.Equal(t, "no-reaction", emojiFlags(emojiRow{}))
	require.Equal(t, "delisted", emojiFlags(emojiRow{Delisted: true}))
}
