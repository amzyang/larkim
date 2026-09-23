package tui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

func writePNG(t *testing.T, dir, name string) string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 8, 8))
	m.Set(0, 0, color.RGBA{R: 255, A: 255})
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
	c := store.Chat{ChatID: "oc_1", Name: "群", ChatMode: "group", AvatarPath: writePNG(t, dir, "a.png")}

	require.NotEmpty(t, k.prepare([]store.Chat{c}), "the picture is transmitted once")
	require.Empty(t, k.prepare([]store.Chat{c}), "and not again")

	top, bottom := k.cells(c)
	for _, line := range []string{top, bottom} {
		require.Equal(t, avatarWidth, lipgloss.Width(line), "the cells occupy exactly the avatar column")
		require.Equal(t, avatarWidth, strings.Count(line, string(kitty.Placeholder)))
	}
	require.NotEqual(t, top, bottom, "each line names its own row of the picture")
}

func TestKittyAvatars_FallsBackWhenThereIsNoPictureToDraw(t *testing.T) {
	dir := t.TempDir()
	k := newKittyAvatars(dir)
	for _, tc := range []struct {
		name string
		chat store.Chat
	}{
		{"no avatar at all", store.Chat{ChatID: "oc_1", Name: "群"}},
		{"peer has none", store.Chat{ChatID: "oc_2", Name: "人", ChatMode: "p2p", PeerAvatarPath: store.AvatarNone}},
		{"download gave up", store.Chat{ChatID: "oc_3", Name: "人", ChatMode: "p2p", PeerAvatarPath: store.AvatarFailed}},
		{"file is not an image", store.Chat{ChatID: "oc_4", Name: "群", AvatarPath: "missing.webp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Empty(t, k.prepare([]store.Chat{tc.chat}))
			top, bottom := k.cells(tc.chat)
			wantTop, wantBottom := avatarBlock(tc.chat)
			require.Equal(t, wantTop, top)
			require.Equal(t, wantBottom, bottom)
		})
	}
}

func TestKittyAvatars_DecodesABrokenFileOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.png"), []byte("not a png"), 0o644))
	k := newKittyAvatars(dir)
	c := store.Chat{ChatID: "oc_1", Name: "群", AvatarPath: "bad.png"}

	require.Empty(t, k.prepare([]store.Chat{c}))
	require.True(t, k.failed["oc_1"])
	require.NoError(t, os.Remove(filepath.Join(dir, "bad.png")))
	require.Empty(t, k.prepare([]store.Chat{c}), "the second pass never opens the file again")
}

func TestKittyAvatars_ReclaimsTheLeastRecentlyShownID(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png")
	k := newKittyAvatars(dir)

	chats := make([]store.Chat, kittyIDs)
	for i := range chats {
		chats[i] = store.Chat{ChatID: "oc_" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Name: "群", AvatarPath: name}
	}
	require.NotEmpty(t, k.prepare(chats))
	require.Len(t, k.id, kittyIDs, "the id space is full")
	evicted := chats[0].ChatID
	reused := k.id[evicted]

	// Show everything except the first, then something new: the first loses its id.
	require.Empty(t, k.prepare(chats[1:]))
	fresh := store.Chat{ChatID: "oc_new", Name: "群", AvatarPath: name}
	require.NotEmpty(t, k.prepare([]store.Chat{fresh}))

	require.NotContains(t, k.id, evicted)
	require.Equal(t, reused, k.id["oc_new"], "the freed id is handed straight on")
	require.Len(t, k.id, kittyIDs, "and the id space never grows")
}

func TestModelAvatarPrepare_OnlyCoversWhatIsOnScreen(t *testing.T) {
	dir := t.TempDir()
	name := writePNG(t, dir, "a.png")
	m := sized(130, 30)
	k := newKittyAvatars(dir)
	m.avatars = k
	for i := range m.chats {
		m.chats[i].AvatarPath = name
	}

	require.NotEmpty(t, m.avatarPrepare())
	require.Len(t, k.id, 2*m.chatListHeight(),
		"the viewport plus the screen below it, not every chat in the store")
	require.Less(t, len(k.id), len(m.chats))
}
