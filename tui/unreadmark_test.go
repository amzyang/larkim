package tui

import (
	"image"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"

	"github.com/amzyang/larkim/store"
)

func TestUnreadMark_IsTheBubbleOnNothing(t *testing.T) {
	m := unreadMark(40, 40)

	alphaAt := func(x, y int) uint8 { return m.Pix[m.PixOffset(x, y)+3] }

	require.EqualValues(t, 0, alphaAt(20, 20), "the middle is the terminal's own background")
	require.EqualValues(t, 0, alphaAt(0, 0), "and so is the corner outside the bubble")
	require.EqualValues(t, 0xFF, alphaAt(2, 20), "the band itself is drawn")
	require.EqualValues(t, 0xFF, alphaAt(20, 2))
}

func TestUnreadMark_BandIsTheClientsProportion(t *testing.T) {
	m := unreadMark(100, 100)
	// One crossing of the band, along the row through the middle. The count
	// runs a half-pixel over the share either side, that being the edge the
	// anti-aliasing covers.
	drawn := 0
	for x := range 50 {
		if m.Pix[m.PixOffset(x, 50)+3] > 0 {
			drawn++
		}
	}
	require.InDelta(t, 100*(markOuter-markInner)/markSpan, drawn, 1)
}

func TestUnreadMark_CarriesTheDotInItsGap(t *testing.T) {
	m := unreadMark(100, 100)

	alphaAt := func(x, y int) uint8 { return m.Pix[m.PixOffset(x, y)+3] }

	require.EqualValues(t, 0xFF, alphaAt(85, 19), "the dot is ink")
	require.EqualValues(t, 0, alphaAt(94, 41),
		"and the band is cut away beside it, which is what tells the glyph from a plain ring")
	require.EqualValues(t, 0xFF, alphaAt(11, 95), "the tail runs past the circle at the lower left")
}

func TestTextAvatars_TheUnreadRowTakesTheRing(t *testing.T) {
	row := listRow{chat: store.Chat{ChatID: unreadFeedRowID}}
	top, bottom, badged := textAvatars{}.cells(row, 0)

	require.Contains(t, ansi.Strip(top), unreadGlyph)
	require.Equal(t, avatarWidth, lipgloss.Width(top))
	require.Equal(t, strings.Repeat(" ", avatarWidth), bottom)
	require.False(t, badged, "the mark carries no counter")
	require.NotContains(t, ansi.Strip(top), "?",
		"no colour block: the row has neither an id to shade it nor a name to letter it")
}

func TestKittyAvatars_TheUnreadRowGetsAPicture(t *testing.T) {
	k := newKittyAvatars(t.TempDir())
	row := listRow{chat: store.Chat{ChatID: unreadFeedRowID}}

	require.NotEmpty(t, k.prepare([]listRow{row}, nil))
	require.Contains(t, k.id, unreadFeedRowID)
	require.Empty(t, k.prepare([]listRow{row}, nil), "and it is never redrawn: it carries no count to move")

	top, _, _ := k.cells(row, 0)
	require.Equal(t, avatarWidth, strings.Count(top, string(kitty.Placeholder)))
}

func TestKittyAvatars_TheUnreadRowsPictureIsNotAChats(t *testing.T) {
	feed := listRow{chat: store.Chat{ChatID: unreadFeedRowID}}
	chat := listRow{chat: store.Chat{ChatID: "oc_platform", Name: "平台组", ChatMode: "group"}}
	require.NotEqual(t, pixKey(feed), pixKey(chat))

	k := newKittyAvatars(t.TempDir())
	require.Equal(t, image.Pt(avatarPixels, avatarPixels), k.picture(feed).Bounds().Max)
}

func TestModelAvatarPrepare_ReachesTheUnreadRow(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	m := sized(130, 30)
	k := newKittyAvatars(dir)
	m.avatars = k
	for i := range m.chats {
		m.chats[i].AvatarPath = name
	}
	m.chatTop = 0

	require.NotEmpty(t, m.avatarPrepare())
	require.Contains(t, k.id, unreadFeedRowID, "the first row on screen has its picture like any other")
}
