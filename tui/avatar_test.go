package tui

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/draw"
)

func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	m.Set(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(filepath.Join(dir, name))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, m))
	return name
}

// filledAvatar is the colour writeFilledPNG paints, distinct from anything a
// stand-in draws so a test can tell the file's picture from a drawn one.
var filledAvatar = color.RGBA{R: 0x20, G: 0x80, B: 0xF0, A: 0xFF}

// writeFilledPNG writes a picture that is opaque everywhere, so a mask is the
// only thing that can make one of its pixels transparent.
func writeFilledPNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(m, m.Bounds(), image.NewUniform(filledAvatar), image.Point{}, draw.Src)
	f, err := os.Create(filepath.Join(dir, name))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, m))
	return name
}

func TestNewAvatars_FallsBackWhenTheTerminalCannotShowPictures(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	require.IsType(t, &kittyAvatars{}, newAvatars("/data", env(map[string]string{"KITTY_WINDOW_ID": "1"})))
	require.IsType(t, &kittyAvatars{}, newAvatars("/data", env(map[string]string{"TERM": "xterm-kitty"})))
	require.IsType(t, textAvatars{}, newAvatars("/data", env(map[string]string{"TERM": "xterm-256color"})))
	require.IsType(t, textAvatars{}, newAvatars("", env(map[string]string{"KITTY_WINDOW_ID": "1"})),
		"without a data dir there is no file to draw")
}

func TestKittyAvatars_PlaceholderCellsSpanTheAvatarColumn(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", ChatMode: "group", AvatarPath: writePNG(t, dir, "a.png", 8, 8)}

	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), nil), "the picture is transmitted once")
	require.Empty(t, k.prepare(rowsOf([]store.Chat{c}), nil), "and not again")

	top, bottom, _ := k.cells(listRow{chat: c}, 0)
	for _, line := range []string{top, bottom} {
		require.Equal(t, avatarWidth, lipgloss.Width(line), "the cells occupy exactly the avatar column")
		require.Equal(t, avatarWidth, strings.Count(line, string(kitty.Placeholder)))
	}
	require.NotEqual(t, top, bottom, "each line names its own row of the picture")
}

func TestKittyAvatars_DrawsAPictureWhenThereIsNoFile(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	for _, tc := range []struct {
		name string
		chat store.Chat
	}{
		{"no avatar at all", store.Chat{ChatID: "oc_1", Name: "程序化养号"}},
		{"peer has none", store.Chat{ChatID: "oc_2", Name: "孙琪", ChatMode: "p2p", PeerAvatarPath: store.AvatarNone}},
		{"download gave up", store.Chat{ChatID: "oc_3", Name: "苏雯", ChatMode: "p2p", PeerAvatarPath: store.AvatarFailed}},
		{"file is not an image", store.Chat{ChatID: "oc_4", Name: "示例告警群", AvatarPath: "missing.webp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{tc.chat}), nil), "a chat with no file still gets a drawn one")
			top, _, _ := k.cells(listRow{chat: tc.chat}, 0)
			require.Equal(t, avatarWidth, strings.Count(top, string(kitty.Placeholder)))
		})
	}
}

func TestKittyAvatars_TransmitsEachChatOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.png"), []byte("not a png"), 0o644))
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", AvatarPath: "bad.png"}

	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), nil), "an unreadable file falls through to a drawn picture")
	require.NoError(t, os.Remove(filepath.Join(dir, "bad.png")))
	require.Empty(t, k.prepare(rowsOf([]store.Chat{c}), nil), "the second pass never opens the file again")
}

func TestKittyAvatars_ReclaimsTheLeastRecentlyShownID(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	k := newKittyAvatars(dir)

	chats := make([]store.Chat, kittyIDs)
	for i := range chats {
		chats[i] = store.Chat{ChatID: "oc_" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Name: "群", AvatarPath: name}
	}
	require.NotEmpty(t, k.prepare(rowsOf(chats), nil))
	require.Len(t, k.id, kittyIDs, "the id space is full")
	evicted := chats[0].ChatID
	reused := k.id[evicted]

	// Show everything except the first, then something new: the first loses its id.
	require.Empty(t, k.prepare(rowsOf(chats[1:]), nil))
	fresh := store.Chat{ChatID: "oc_new", Name: "群", AvatarPath: name}
	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{fresh}), nil))

	require.NotContains(t, k.id, evicted)
	require.Equal(t, reused, k.id["oc_new"], "the freed id is handed straight on")
	require.Len(t, k.id, kittyIDs, "and the id space never grows")
}

func TestModelAvatarPrepare_OnlyCoversWhatIsOnScreen(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	m := sized(130, 30)
	k := newKittyAvatars(dir)
	m.avatars = k
	for i := range m.chats {
		m.chats[i].AvatarPath = name
	}

	require.NotEmpty(t, m.avatarPrepare())
	require.Len(t, k.id, 3*m.chatListHeight(),
		"the viewport and a screen either side, not every chat in the store")
	require.Less(t, len(k.id), len(m.chats))
}

func TestInitials_SkipsTheDecorationGroupNamesOpenWith(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"程序化养号", "程序化养"},
		{"【语言】示例问题及需求沟通群", "语言示例"},
		{"H程序化直播暖场", "H程序化"},
		{"李明", "李明"},
		{"孙琪", "孙琪"},
		{"- _ ·", ""},
		{"", ""},
	} {
		require.Equal(t, tc.want, initials(tc.name), "name %q", tc.name)
	}
}

func TestGlyphCell_PacksTheGlyphsIntoACentredBlock(t *testing.T) {
	const w, h, em = 80, 40, 12 // an oblong box, as real terminal cells give
	require.Equal(t, image.Rect(34, 14, 46, 26), glyphCell(1, 0, w, h, em),
		"one glyph sits in the middle, not spread over the whole box")

	require.Equal(t, image.Rect(28, 14, 40, 26), glyphCell(2, 0, w, h, em))
	require.Equal(t, image.Rect(40, 14, 52, 26), glyphCell(2, 1, w, h, em),
		"two sit side by side, touching, astride the centre")

	require.Equal(t, image.Rect(28, 8, 40, 20), glyphCell(4, 0, w, h, em))
	require.Equal(t, image.Rect(40, 8, 52, 20), glyphCell(4, 1, w, h, em))
	require.Equal(t, image.Rect(28, 20, 40, 32), glyphCell(4, 2, w, h, em))
	require.Equal(t, image.Rect(40, 20, 52, 32), glyphCell(4, 3, w, h, em),
		"four fall into a 2x2 block, in reading order")

	require.Equal(t, image.Rect(34, 20, 46, 32), glyphCell(3, 2, w, h, em),
		"a row the grid leaves short is centred")
}

func TestKittyAvatars_DrawsAtTheCellSizeTheTerminalReports(t *testing.T) {
	k := newKittyAvatars(t.TempDir())
	w, h := k.box()
	require.Equal(t, avatarPixels, w, "a square guess until the terminal says")
	require.Equal(t, avatarPixels, h)

	require.True(t, k.setCellSize(9, 19))
	w, h = k.box()
	require.Equal(t, avatarWidth*9, w, "then exactly the pixels the cells occupy")
	require.Equal(t, chatRowHeight*19, h)

	require.False(t, k.setCellSize(9, 19), "the same size is not a change")
	require.False(t, k.setCellSize(0, 19), "nor is a nonsense one")
}

func TestKittyAvatars_ANewCellSizeRedrawsEverything(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "程序化养号"}
	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), nil))
	require.Empty(t, k.prepare(rowsOf([]store.Chat{c}), nil))

	require.True(t, k.setCellSize(9, 19))
	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), nil),
		"pictures drawn for the old cell size would be resampled, so they are drawn again")
}

// pixAt reads one pixel as the premultiplied bytes the picture stores, so a
// test can compare it against the palette entry that painted it.
func pixAt(m *image.RGBA, x, y int) color.RGBA {
	i := m.PixOffset(x, y)
	return color.RGBA{R: m.Pix[i], G: m.Pix[i+1], B: m.Pix[i+2], A: m.Pix[i+3]}
}

// countPix is how many pixels carry exactly c.
func countPix(m *image.RGBA, c color.RGBA) int {
	n := 0
	for i := 0; i < len(m.Pix); i += 4 {
		if m.Pix[i] == c.R && m.Pix[i+1] == c.G && m.Pix[i+2] == c.B && m.Pix[i+3] == c.A {
			n++
		}
	}
	return n
}

// bodyPixel is a point inside the disc but clear of both the glyph block and
// the rim, so it shows whatever colour fills the avatar.
func bodyPixel(side int) (int, int) { return side/2 + side*35/100, side / 2 }

func TestGenerateAvatar_FillsTheDiscAndInksItInWhite(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	accent := generatedPalette[0]
	m := generateAvatar("程序化养号", 0, avatarPixels, avatarPixels, false)
	require.NotNil(t, m)
	require.Equal(t, image.Rect(0, 0, avatarPixels, avatarPixels), m.Bounds())

	x, y := bodyPixel(avatarPixels)
	require.Equal(t, accent, pixAt(m, x, y), "the filled style carries the accent as its body")
	require.Greater(t, countPix(m, avatarWhite), 200, "the glyphs leave a visible amount of ink")

	plain := generateAvatar("", 0, avatarPixels, avatarPixels, false)
	require.Zero(t, countPix(plain, avatarWhite), "a nameless chat gets a bare colour disc")
}

func TestGenerateAvatar_OutlinesAWhiteDiscInTheAccentItInksWith(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	accent := generatedPalette[0]
	m := generateAvatar("程序化养号", 0, avatarPixels, avatarPixels, true)
	require.NotNil(t, m)

	x, y := bodyPixel(avatarPixels)
	require.Equal(t, avatarWhite, pixAt(m, x, y), "the outlined style carries a white body")
	require.Greater(t, countPix(m, accent), 200, "the glyphs are inked in the accent")
	require.Equal(t, accent, pixAt(m, avatarPixels/2, 1), "and so is the rim")

	plain := generateAvatar("", 0, avatarPixels, avatarPixels, true)
	require.Greater(t, countPix(plain, accent), 0, "a nameless chat still gets its rim")
	require.Equal(t, avatarWhite, pixAt(plain, avatarPixels/2, avatarPixels/2),
		"with nothing inside it")
}

func TestMaskDisc_ClearsEverythingOutsideTheCircle(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	m := generateAvatar("程序化养号", 0, avatarPixels, avatarPixels, false)

	for _, p := range []image.Point{{X: 0, Y: 0}, {X: avatarPixels - 1, Y: 0},
		{X: 0, Y: avatarPixels - 1}, {X: avatarPixels - 1, Y: avatarPixels - 1}} {
		_, _, _, a := m.At(p.X, p.Y).RGBA()
		require.Zero(t, a, "corner %v shows the terminal through", p)
	}

	_, _, _, a := m.At(avatarPixels/2, avatarPixels/2).RGBA()
	require.Equal(t, uint32(0xFFFF), a, "the middle stays opaque")

	// A tenth in along the diagonal is outside the disc but inside a rounded
	// square, which is what tells the two shapes apart.
	_, _, _, a = m.At(avatarPixels/10, avatarPixels/10).RGBA()
	require.Zero(t, a, "the shape is a disc, not a square with soft corners")

	_, _, _, a = m.At(avatarPixels/4, avatarPixels/4).RGBA()
	require.Equal(t, uint32(0xFFFF), a, "everything within the radius is kept")

	_, _, _, a = m.At(avatarPixels/2, 2).RGBA()
	require.Equal(t, uint32(0xFFFF), a, "the disc runs to the edges of its box")

	rim := 0
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] > 0 && m.Pix[i] < 0xFF {
			rim++
		}
	}
	require.Greater(t, rim, 0, "the rim is anti-aliased rather than stepped")

	// Premultiplied alpha: no channel may exceed the alpha it is scaled by.
	for i := 0; i < len(m.Pix); i += 4 {
		require.LessOrEqual(t, m.Pix[i], m.Pix[i+3], "red exceeds alpha at %d", i)
		require.LessOrEqual(t, m.Pix[i+1], m.Pix[i+3], "green exceeds alpha at %d", i)
		require.LessOrEqual(t, m.Pix[i+2], m.Pix[i+3], "blue exceeds alpha at %d", i)
	}
}

func TestKittyAvatars_MasksTheFileAvatarToADisc(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", ChatMode: "group",
		AvatarPath: writeFilledPNG(t, dir, "a.png", 64, 64)}

	img := k.picture(listRow{chat: c})
	require.NotNil(t, img)
	w, h := k.box()
	require.Equal(t, image.Rect(0, 0, w, h), img.Bounds())

	_, _, _, a := img.At(0, 0).RGBA()
	require.Zero(t, a, "the square the CDN serves is cut to the disc the client draws")
	_, _, _, a = img.At(w/2, h/2).RGBA()
	require.Equal(t, uint32(0xFFFF), a, "the picture itself is untouched")
}

func TestKittyAvatars_OutlinesAGroupAndFillsAPerson(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	k := newKittyAvatars(t.TempDir())
	w, h := k.box()
	x, y := bodyPixel(min(w, h))

	group := k.picture(listRow{chat: store.Chat{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}})
	require.Equal(t, avatarWhite, pixAt(group, x, y),
		"a group without a picture takes the outlined style, as the client draws it")

	person := k.picture(listRow{chat: store.Chat{ChatID: "oc_a", Name: "林岚", ChatMode: "p2p"}})
	require.Equal(t, generatedPalette[int(idHash("oc_a"))%len(generatedPalette)], pixAt(person, x, y),
		"a person takes the filled one, so a stand-in never reads as the wrong kind of chat")
}

func TestBadgeLabel_CapsAtNinetyNinePlus(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{{0, ""}, {-1, ""}, {1, "1"}, {12, "12"}, {99, "99"}, {100, "99+"}, {5000, "99+"}} {
		require.Equal(t, tc.want, badgeLabel(tc.n), "count %d", tc.n)
	}
}

func TestDrawBadge_StampsTheTopRightInTheColourTheChatCallsFor(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	// The pixels four by two cells occupy on the machine this runs on.
	const w, h = 68, 72
	blue := color.RGBA{B: 0xFF, A: 0xFF}
	stamped := func(n int64, muted bool) (*image.RGBA, bool) {
		m := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(m.Pix); i += 4 {
			m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = blue.R, blue.G, blue.B, blue.A
		}
		return m, drawBadge(m, n, muted)
	}

	// Three pixels in from the right edge, level with the counter's middle:
	// inside the disc, clear of the digits.
	inside := image.Pt(w-3, int(math.Round(math.Min(w, h)*badgeHeight))/2)
	m, drawn := stamped(3, false)
	require.True(t, drawn)
	require.Equal(t, badgeRed, m.RGBAAt(inside.X, inside.Y))
	require.Equal(t, blue, m.RGBAAt(w/2, h/2), "the picture under it keeps its middle")
	cleared := 0
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] == 0 {
			cleared++
		}
	}
	require.Greater(t, cleared, 0, "a ring is cleared so the counter reads against any picture")

	muted, _ := stamped(3, true)
	require.Equal(t, badgeGrey, muted.RGBAAt(inside.X, inside.Y), "do-not-disturb gets the quiet colour")

	none, drawn := stamped(0, false)
	require.False(t, drawn)
	require.Equal(t, blue, none.RGBAAt(inside.X, inside.Y), "nothing unread leaves the picture alone")
}

func TestKittyAvatars_RedrawsWhenTheUnreadCountMoves(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", AvatarPath: writePNG(t, dir, "a.png", 8, 8)}
	unread := map[string]int64{"oc_1": 2}

	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), unread))
	require.Empty(t, k.prepare(rowsOf([]store.Chat{c}), unread), "the same count needs no new picture")

	id := k.id["oc_1"]
	unread["oc_1"] = 3
	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), unread), "the counter is part of the picture")
	require.Equal(t, id, k.id["oc_1"], "and a redraw keeps the id the cells already name")
}

func TestKittyAvatars_CellsClaimTheCountOnlyOnceTheyCarryIt(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", AvatarPath: writePNG(t, dir, "a.png", 8, 8)}

	_, _, badged := k.cells(listRow{chat: c}, 2)
	require.False(t, badged, "with no picture yet the row prints the number itself")

	require.NotEmpty(t, k.prepare(rowsOf([]store.Chat{c}), map[string]int64{"oc_1": 2}))
	_, _, badged = k.cells(listRow{chat: c}, 2)
	require.True(t, badged)

	_, _, badged = k.cells(listRow{chat: c}, 3)
	require.False(t, badged, "a count the picture has not caught up with is printed too")
}

// tallModel is the chat list on a terminal high enough that the viewport plus
// a screen either side outgrows the id space, which is where the redraw used
// to stop settling.
func tallModel(t *testing.T) (Model, *kittyAvatars) {
	t.Helper()
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	m := sized(106, 59)
	k := newKittyAvatars(dir)
	m.avatars = k
	m.chats = nil
	for i := range 745 {
		m.chats = append(m.chats, store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: "群", ChatMode: "group", AvatarPath: name})
	}
	m.chatIdx, m.chatTop = 40, 30
	return m, k
}

func TestModelAvatarPrepare_SettlesOnATallTerminal(t *testing.T) {
	m, k := tallModel(t)

	require.NotEmpty(t, m.avatarPrepare(), "the first pass fills the window")
	require.Empty(t, m.avatarPrepare(), "a pass that changes nothing sends nothing")
	require.LessOrEqual(t, len(k.id), kittyIDs, "the window never asks for more ids than there are")
}

func TestModelAvatarPrepare_CoversEveryRowOnScreen(t *testing.T) {
	m, k := tallModel(t)
	m.avatarPrepare()

	vis := m.visibleRows()
	for _, r := range vis[m.chatTop : m.chatTop+m.chatListHeight()] {
		require.Contains(t, k.id, r.chatID(), "a row on screen always has its picture")
	}
}

func TestKittyAvatars_EvictsOnlyWhatLeftTheWindow(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	k := newKittyAvatars(dir)
	chats := make([]store.Chat, kittyIDs+4)
	for i := range chats {
		chats[i] = store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: "群", AvatarPath: name}
	}

	// Scroll up one chat at a time: the chat that entered is reached before the
	// live ones, so a tie on the eviction clock takes a neighbour instead of
	// the chat that left, and that neighbour has to be sent all over again.
	require.Equal(t, kittyIDs, transmitted(k.prepare(rowsOf(chats[4:4+kittyIDs]), nil)))
	for top := 3; top >= 0; top-- {
		window := chats[top : top+kittyIDs]
		require.Equal(t, 1, transmitted(k.prepare(rowsOf(window), nil)),
			"one chat entered the window, so one picture is sent")
		for _, c := range window {
			require.Contains(t, k.id, c.ChatID, "everything still in the window keeps its picture")
		}
	}
}

// transmitted counts the pictures a prepare pass handed the terminal.
func transmitted(seq string) int { return strings.Count(seq, "\x1b_G") }

func TestUpdate_DoesNotAnswerItsOwnRawSequence(t *testing.T) {
	m, _ := tallModel(t)

	_, cmd := m.Update(tea.RawMsg{})
	require.Nil(t, cmd, "the sequence Update wrote comes back here; answering it would feed the loop")
}

func TestFitImage_PadsTheRoundingInsteadOfStretchingIntoIt(t *testing.T) {
	// Two stacked bands, so a picture stretched to fill the box shows it: the
	// seam between them moves.
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := range 4 {
		src.Set(x, 0, red)
		src.Set(x, 1, blue)
	}
	dst := fitImage(src, image.Pt(4, 2), image.Pt(4, 4), draw.NearestNeighbor)

	require.Equal(t, image.Rect(0, 0, 4, 4), dst.Bounds(), "the canvas is the box the cells reserved")
	require.Equal(t, red, dst.At(0, 0), "the picture starts at the top left")
	require.Equal(t, blue, dst.At(3, 1), "with its bands where the source put them")
	require.Equal(t, blank, dst.At(0, 2), "and the rounding below it stays transparent")
	require.Equal(t, blank, dst.At(3, 3))
}

func TestUpdate_AResizeAsksForTheCellSizeAgain(t *testing.T) {
	m, k := tallModel(t)
	m.pics = testPictures(t)

	_, cmd := m.update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.NotNil(t, cmd, "a grid that resized may have resized because the font did")
	require.Equal(t, tea.RawMsg{Msg: ansi.WindowOp(ansi.RequestCellSizeWinOp)}, cmd(),
		"the terminal reports its cell size only when asked")

	k.setCellSize(10, 20)
	m.pics.id["live"] = picIDBase
	_, cmd = m.update(uv.CellSizeEvent{Width: 10, Height: 20})
	require.Nil(t, cmd)
	require.Contains(t, m.pics.id, "live", "an answer that repeats what they hold drops nothing")

	m.update(uv.CellSizeEvent{Width: 9, Height: 19})
	require.NotContains(t, m.pics.id, "live", "a cell size that moved drops what was drawn for the old grid")
}

func TestKittyAvatars_DrawsAP2PPeerInTheColourTheirMessagesTake(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	k := newKittyAvatars(t.TempDir())
	w, h := k.box()
	x, y := bodyPixel(min(w, h))

	c := store.Chat{ChatID: "oc_quiet", Name: "构建机器人", ChatMode: "p2p", P2PTargetID: "ou_a"}
	require.Equal(t, generatedPalette[int(idHash("ou_a"))%len(generatedPalette)], pixAt(k.picture(listRow{chat: c}), x, y),
		"the row and the sender's message blocks stand for one peer, so both take one colour")

	top, _, _ := textAvatars{}.cells(listRow{chat: c}, 0)
	require.Equal(t, avatarBlock("ou_a", c.Name, avatarWidth), top,
		"the colour block the terminal falls back to seeds from the same peer")
}

// threadRowOf is a thread of the chat, as the list hands one to the column.
func threadRowOf(c store.Chat, threadID string, unread int64) listRow {
	return listRow{chat: c, thread: store.ThreadFeed{ThreadID: threadID, ChatID: c.ChatID, Unread: unread}}
}

func TestTextAvatars_AThreadRowCarriesTheGlyph(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group"}

	top, bottom, _ := textAvatars{}.cells(threadRowOf(c, "omt_x", 0), 0)

	require.Contains(t, ansi.Strip(top), threadGlyph)
	require.Equal(t, avatarWidth, lipgloss.Width(top), "the glyph takes a column of the block, not one beside it")
	require.Equal(t, avatarWidth, lipgloss.Width(bottom))
}

// The mark is the client's own, and the chat rides its corner.
func TestThreadAvatar_PutsTheChatOnTheMarksCorner(t *testing.T) {
	mark := threadMark()
	require.NotNil(t, mark, "the mark ships with the binary")
	badge := image.NewRGBA(image.Rect(0, 0, 30, 30))
	draw.Draw(badge, badge.Bounds(), image.NewUniform(color.RGBA{R: 0xFF, A: 0xFF}), image.Point{}, draw.Src)
	maskDisc(badge)

	m := threadAvatar(mark, badge, 72, 76)

	require.Equal(t, image.Rect(0, 0, 72, 76), m.Bounds())
	side := badge.Bounds().Dx()
	gap := max(1, int(float64(side)*threadBadgeGap))
	cx, cy := 72-gap-side/2, 76-gap-side/2
	r, _, _, a := m.At(cx, cy).RGBA()
	require.EqualValues(t, 0xFFFF, a, "the chat's picture is opaque where its middle lands")
	require.EqualValues(t, 0xFFFF, r, "and it is the chat's own picture there")
	_, _, _, cut := m.At(cx-side/2-gap/2-1, cy).RGBA()
	require.Zero(t, cut, "a gap is punched around it so it reads as its own disc")
}

func TestThreadAvatar_WithoutAPictureTheMarkStandsAlone(t *testing.T) {
	m := threadAvatar(threadMark(), nil, 72, 76)

	require.Equal(t, image.Rect(0, 0, 72, 76), m.Bounds())
	ring := 0
	for x := range 72 {
		if _, _, _, a := m.At(x, 6).RGBA(); a > 0 {
			ring++
		}
	}
	require.NotZero(t, ring, "the ring is still drawn")
}

// A chat and a thread inside it are two rows carrying two different counters,
// so they cannot share one picture.
func TestKittyAvatars_AThreadKeepsItsOwnPicture(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group", AvatarPath: writeFilledPNG(t, dir, "a.png", 8, 8)}
	rows := []listRow{{chat: c}, threadRowOf(c, "omt_x", 2)}

	require.NotEmpty(t, k.prepare(rows, map[string]int64{"oc_1": 5}))
	require.Len(t, k.id, 2, "two rows, two pictures")
	require.NotEqual(t, k.id["oc_1"], k.id["omt_x"])
	require.EqualValues(t, 5, k.badge["oc_1"])
	require.EqualValues(t, 2, k.badge["omt_x"], "the thread's own replies are what its counter says")

	require.Empty(t, k.prepare(rows, map[string]int64{"oc_1": 5}), "neither is drawn again")
}

func TestKittyAvatars_ReusesTheCompositeWhenTheCountMoves(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group",
		AvatarPath: writeFilledPNG(t, dir, "a.png", 640, 640)}
	rows := rowsOf([]store.Chat{c})
	unread := map[string]int64{"oc_1": 1}

	require.NotEmpty(t, k.prepare(rows, unread))
	// With the file gone, a pass that decoded again would fall through to the
	// drawn stand-in; the cached composite is the only way back to this picture.
	require.NoError(t, os.Remove(filepath.Join(dir, "a.png")))

	unread["oc_1"] = 2
	require.NotEmpty(t, k.prepare(rows, unread), "the new counter is stamped on the kept picture")
	require.EqualValues(t, 2, k.badge["oc_1"])

	w, h := k.box()
	require.Equal(t, filledAvatar, pixAt(k.pix[pixKey(rows[0])], w/2, h/2),
		"and that picture is still the file's, not a stand-in")
}

func TestKittyAvatars_TheCounterDoesNotSpoilTheCompositeUnderIt(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group",
		AvatarPath: writeFilledPNG(t, dir, "a.png", 64, 64)}
	rows := rowsOf([]store.Chat{c})

	k.prepare(rows, map[string]int64{"oc_1": 1})
	clean := slices.Clone(k.pix[pixKey(rows[0])].Pix)

	k.prepare(rows, map[string]int64{"oc_1": 99})
	require.Equal(t, clean, k.pix[pixKey(rows[0])].Pix, "the counter is stamped on a copy")
}

func TestPixKey_MissesWhenWhatIsDrawnChanges(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group", AvatarPath: "a.png"}
	base := pixKey(listRow{chat: c})

	arrived := c
	arrived.AvatarPath = "b.png"
	require.NotEqual(t, base, pixKey(listRow{chat: arrived}), "an avatar that downloaded later is a new picture")

	renamed := c
	renamed.Name = "项目协作群"
	require.NotEqual(t, base, pixKey(listRow{chat: renamed}), "a stand-in spells the name it was drawn from")

	require.NotEqual(t, base, pixKey(threadRowOf(c, "omt_x", 0)),
		"a thread wears the mark, so it is not the chat's own picture")

	require.Equal(t, base, pixKey(listRow{chat: c}), "and nothing else moves it")
}

func TestKittyAvatars_EvictsTheLeastRecentlyDrawnComposite(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	k := newKittyAvatars(dir)
	chat := func(i int) store.Chat {
		return store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: "平台组", ChatMode: "group", AvatarPath: name}
	}

	first := pixKey(listRow{chat: chat(0)})
	for i := range avatarPixCache {
		k.prepare(rowsOf([]store.Chat{chat(i)}), nil)
	}
	require.Len(t, k.pix, avatarPixCache, "the cache fills")
	require.Contains(t, k.pix, first)

	k.prepare(rowsOf([]store.Chat{chat(avatarPixCache)}), nil)
	require.Len(t, k.pix, avatarPixCache, "and never grows past its bound")
	require.NotContains(t, k.pix, first, "the oldest composite is the one that goes")
}

func TestKittyAvatars_ANewCellSizeDropsTheComposites(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "平台组", ChatMode: "group", AvatarPath: writePNG(t, dir, "a.png", 8, 8)}
	k.prepare(rowsOf([]store.Chat{c}), nil)
	require.NotEmpty(t, k.pix)

	require.True(t, k.setCellSize(9, 19))
	require.Empty(t, k.pix, "a composite drawn for the old cell size would be resampled")
	require.Empty(t, k.pixUsed)
}

// BenchmarkKittyAvatarsPrepare_CountChurn is the pass a chat list does when a
// message lands: one row's counter moved and the rest are unchanged.
func BenchmarkKittyAvatarsPrepare_CountChurn(b *testing.B) {
	dir := b.TempDir()
	name := "a.png"
	m := image.NewRGBA(image.Rect(0, 0, 640, 640))
	draw.Draw(m, m.Bounds(), image.NewUniform(filledAvatar), image.Point{}, draw.Src)
	f, err := os.Create(filepath.Join(dir, name))
	require.NoError(b, err)
	require.NoError(b, png.Encode(f, m))
	require.NoError(b, f.Close())

	k := newKittyAvatars(dir)
	k.setCellSize(9, 19)
	chats := make([]store.Chat, 30)
	for i := range chats {
		chats[i] = store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: "平台组", ChatMode: "group", AvatarPath: name}
	}
	rows := rowsOf(chats)
	unread := map[string]int64{}
	k.prepare(rows, unread)

	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		unread["oc_0"] = int64(i%98) + 1
		k.prepare(rows, unread)
	}
}
