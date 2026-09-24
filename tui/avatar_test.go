package tui

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
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

// writeFilledPNG writes a picture that is opaque everywhere, so a mask is the
// only thing that can make one of its pixels transparent.
func writeFilledPNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(m, m.Bounds(), image.NewUniform(color.RGBA{R: 0x20, G: 0x80, B: 0xF0, A: 0xFF}), image.Point{}, draw.Src)
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

	require.NotEmpty(t, k.prepare([]store.Chat{c}, nil), "the picture is transmitted once")
	require.Empty(t, k.prepare([]store.Chat{c}, nil), "and not again")

	top, bottom, _ := k.cells(c, 0)
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
			require.NotEmpty(t, k.prepare([]store.Chat{tc.chat}, nil), "a chat with no file still gets a drawn one")
			top, _, _ := k.cells(tc.chat, 0)
			require.Equal(t, avatarWidth, strings.Count(top, string(kitty.Placeholder)))
		})
	}
}

func TestKittyAvatars_TransmitsEachChatOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.png"), []byte("not a png"), 0o644))
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", AvatarPath: "bad.png"}

	require.NotEmpty(t, k.prepare([]store.Chat{c}, nil), "an unreadable file falls through to a drawn picture")
	require.NoError(t, os.Remove(filepath.Join(dir, "bad.png")))
	require.Empty(t, k.prepare([]store.Chat{c}, nil), "the second pass never opens the file again")
}

func TestKittyAvatars_ReclaimsTheLeastRecentlyShownID(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png", 8, 8)
	k := newKittyAvatars(dir)

	chats := make([]store.Chat, kittyIDs)
	for i := range chats {
		chats[i] = store.Chat{ChatID: "oc_" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Name: "群", AvatarPath: name}
	}
	require.NotEmpty(t, k.prepare(chats, nil))
	require.Len(t, k.id, kittyIDs, "the id space is full")
	evicted := chats[0].ChatID
	reused := k.id[evicted]

	// Show everything except the first, then something new: the first loses its id.
	require.Empty(t, k.prepare(chats[1:], nil))
	fresh := store.Chat{ChatID: "oc_new", Name: "群", AvatarPath: name}
	require.NotEmpty(t, k.prepare([]store.Chat{fresh}, nil))

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
		{"程序化养号", "程序"},
		{"【语言】示例问题及需求沟通群", "语言"},
		{"H程序化直播暖场", "H程"},
		{"李明", "李明"},
		{"孙琪", "孙琪"},
		{"- _ ·", ""},
		{"", ""},
	} {
		require.Equal(t, tc.want, initials(tc.name), "name %q", tc.name)
	}
}

func TestGlyphCell_LaysGlyphsOutByCount(t *testing.T) {
	const w, h = 80, 40 // an oblong box, as real terminal cells give
	require.Equal(t, image.Rect(0, 0, w, h), glyphCell(1, 0, w, h),
		"one glyph owns the whole box")

	require.Equal(t, image.Rect(0, 0, 40, h), glyphCell(2, 0, w, h))
	require.Equal(t, image.Rect(40, 0, w, h), glyphCell(2, 1, w, h),
		"two sit side by side")
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
	require.NotEmpty(t, k.prepare([]store.Chat{c}, nil))
	require.Empty(t, k.prepare([]store.Chat{c}, nil))

	require.True(t, k.setCellSize(9, 19))
	require.NotEmpty(t, k.prepare([]store.Chat{c}, nil),
		"pictures drawn for the old cell size would be resampled, so they are drawn again")
}

func TestGenerateAvatar_InksTheGlyphsOntoTheBackground(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	m := generateAvatar("程序化养号", 0, avatarPixels, avatarPixels)
	require.NotNil(t, m)
	require.Equal(t, image.Rect(0, 0, avatarPixels, avatarPixels), m.Bounds())

	// The glyphs are white on a coloured square, so count the fully white pixels.
	rgba := m
	white := 0
	for i := 0; i < len(rgba.Pix); i += 4 {
		if rgba.Pix[i] == 0xFF && rgba.Pix[i+1] == 0xFF && rgba.Pix[i+2] == 0xFF {
			white++
		}
	}
	require.Greater(t, white, 200, "the glyphs leave a visible amount of ink")

	plain := generateAvatar("", 0, avatarPixels, avatarPixels)
	for i := 0; i < len(plain.Pix); i += 4 {
		require.NotEqual(t, uint8(0xFF), plain.Pix[i], "a nameless chat gets a bare colour square")
	}
}

func TestMaskDisc_ClearsEverythingOutsideTheCircle(t *testing.T) {
	if avatarFont() == nil {
		t.Skip("no system font on this machine")
	}
	m := generateAvatar("程序化养号", 0, avatarPixels, avatarPixels)

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

	img := k.picture(c)
	require.NotNil(t, img)
	w, h := k.box()
	require.Equal(t, image.Rect(0, 0, w, h), img.Bounds())

	_, _, _, a := img.At(0, 0).RGBA()
	require.Zero(t, a, "the square the CDN serves is cut to the disc the client draws")
	_, _, _, a = img.At(w/2, h/2).RGBA()
	require.Equal(t, uint32(0xFFFF), a, "the picture itself is untouched")
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

	require.NotEmpty(t, k.prepare([]store.Chat{c}, unread))
	require.Empty(t, k.prepare([]store.Chat{c}, unread), "the same count needs no new picture")

	id := k.id["oc_1"]
	unread["oc_1"] = 3
	require.NotEmpty(t, k.prepare([]store.Chat{c}, unread), "the counter is part of the picture")
	require.Equal(t, id, k.id["oc_1"], "and a redraw keeps the id the cells already name")
}

func TestKittyAvatars_CellsClaimTheCountOnlyOnceTheyCarryIt(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", AvatarPath: writePNG(t, dir, "a.png", 8, 8)}

	_, _, badged := k.cells(c, 2)
	require.False(t, badged, "with no picture yet the row prints the number itself")

	require.NotEmpty(t, k.prepare([]store.Chat{c}, map[string]int64{"oc_1": 2}))
	_, _, badged = k.cells(c, 2)
	require.True(t, badged)

	_, _, badged = k.cells(c, 3)
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

	vis := m.visibleChats()
	for _, c := range vis[m.chatTop : m.chatTop+m.chatListHeight()] {
		require.Contains(t, k.id, c.ChatID, "a row on screen always has its picture")
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
	require.Equal(t, kittyIDs, transmitted(k.prepare(chats[4:4+kittyIDs], nil)))
	for top := 3; top >= 0; top-- {
		window := chats[top : top+kittyIDs]
		require.Equal(t, 1, transmitted(k.prepare(window, nil)),
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
