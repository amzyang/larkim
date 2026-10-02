package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
)

const selfID = "ou_self"

// unreadPageDeps is one waiting chat holding the four things the page draws
// differently on a terminal that takes pictures: a sender with an avatar file,
// an image attachment, a reaction, and a merged forward the reader sent.
func unreadPageDeps(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	st, err := storetest.Open(t, filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()

	require.NoError(t, st.UpsertChats(ctx, []store.Chat{
		{ChatID: "oc_platform", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a"},
	}, 1))
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_a", Name: "张三", AvatarPath: writePNG(t, dir, "ou_a.png", 96, 96)},
		{OpenID: selfID, Name: "林岚"},
	}, 1))
	writeTestEmoji(t, dir, "JIAYI")

	said := func(id string, ms int64, kind string) store.Message {
		return store.Message{MessageID: id, ChatID: "oc_platform", MsgType: kind,
			SenderID: "ou_a", SenderType: "user", SenderName: "张三",
			ContentRaw: `{"text":"x"}`, CreateMs: ms, UpdateMs: ms, MessagePosition: ms}
	}
	forwarded := said("om_fwd", 300, "merge_forward")
	forwarded.SenderID, forwarded.SenderName = selfID, "林岚"
	_, err = st.UpsertMessages(ctx, []store.Message{
		said("om_react", 100, "text"), said("om_pic", 200, "post"), forwarded,
	}, 1)
	require.NoError(t, err)
	// The body a pane draws is the rendering, which lands in its own write.
	require.NoError(t, st.UpdateRendered(ctx, "om_react", "接口什么时候好",
		`{"counts":[{"reaction_type":"JIAYI","count":"3"}]}`, 1))
	require.NoError(t, st.UpdateRendered(ctx, "om_pic", "看这个\n![Image](img_a)", "", 1))

	require.NoError(t, st.AddPendingResources(ctx, []store.ResourceRef{
		{MessageID: "om_pic", FileKey: "img_a", Type: "image"},
	}))
	require.NoError(t, st.MarkResourceDone(ctx, "img_a", writePNG(t, dir, "img_a.png", 200, 200), 4))

	require.NoError(t, st.AddForwardRoots(ctx, []string{"om_fwd"}))
	require.NoError(t, st.SaveForwarded(ctx, "om_fwd", []store.Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_src", ChatID: "oc_peer", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", CreateMs: 10, ContentRaw: `{"text":"预算定了"}`},
	}, 300))

	unread := false
	for _, id := range []string{"om_react", "om_pic", "om_fwd"} {
		require.NoError(t, st.SetReadStatus(ctx, id, &unread, 100, 0))
	}
	return Deps{Store: st, DataDir: dir, Self: selfID}
}

func unreadScreen() UnreadScreen {
	return UnreadScreen{Width: 100, Height: 40, CellW: 10, CellH: 20, Dark: true, TTY: true, Graphics: true}
}

// The page is the TUI's pane: on a terminal that draws them, the pictures the
// pane draws are on it, not the stand-ins a plain terminal falls back to.
func TestUnreadPage_DrawsWhatTheTUIDraws(t *testing.T) {
	page, err := UnreadPage(t.Context(), unreadPageDeps(t), unreadScreen())
	require.NoError(t, err)

	require.Contains(t, page, "\x1b_G", "the images reach the terminal")
	require.Less(t, strings.Index(page, "\x1b_G"), strings.Index(page, "平台组"),
		"and they reach it before the cells that name them")
	require.Contains(t, page, string(kitty.Placeholder), "the rows name them back")
	plain := ansi.Strip(page)
	require.NotContains(t, plain, "[Image]", "an attachment is drawn, not labelled")
	require.NotContains(t, plain, "[+1]⋮", "a reaction with no character of its own wears its picture")
}

// Off a terminal nothing is transmitted: an escape sequence in a file is noise,
// and every row falls back to the stand-in it has always drawn.
func TestUnreadPage_KeepsThePagePlainOffATerminal(t *testing.T) {
	sc := unreadScreen()
	sc.TTY = false

	page, err := UnreadPage(t.Context(), unreadPageDeps(t), sc)
	require.NoError(t, err)

	require.NotContains(t, page, "\x1b_G")
	require.NotContains(t, page, string(kitty.Placeholder))
	plain := ansi.Strip(page)
	require.Contains(t, plain, "[Image]", "the attachment is named instead")
	require.Contains(t, plain, "[+1]⋮+3", "and so is the reaction")
}

// A bundle the reader forwarded is titled by their own name, which is not on
// the message: it is read from contacts, the way the panes read it.
func TestUnreadPage_NamesTheReaderOnTheirOwnForward(t *testing.T) {
	page, err := UnreadPage(t.Context(), unreadPageDeps(t), unreadScreen())
	require.NoError(t, err)

	require.Contains(t, ansi.Strip(page), "林岚 and 张三's Chat History")
}

// The page has no chats list beside it and no pane border, so it spends every
// column of the terminal on the messages.
func TestUnreadPage_SpansTheWholeTerminal(t *testing.T) {
	sc := unreadScreen()

	page, err := UnreadPage(t.Context(), unreadPageDeps(t), sc)
	require.NoError(t, err)

	var day string
	for line := range strings.SplitSeq(page, "\n") {
		if strings.Contains(line, "1970-01-01") {
			day = line
			break
		}
	}
	require.NotEmpty(t, day, "the page rules off its days")
	require.Equal(t, sc.Width, ansi.StringWidth(day))
}

// The page is written straight to the terminal, so a sender's escape sequence
// would be one the terminal obeys. Only the transmissions the placer makes
// before the rows carry an APC; nothing a message body holds reaches the
// terminal as a sequence.
func TestUnreadPage_DropsASendersEscapeSequences(t *testing.T) {
	d := unreadPageDeps(t)
	ctx := t.Context()
	require.NoError(t, d.Store.UpdateRendered(ctx, "om_react",
		"hi\x1bP@kitty-cmd{\"cmd\":\"launch\"}\x1b\\ and \x1b]52;c;cGF5bG9hZA==\x07 and \x1b[2J", "", 2))

	page, err := UnreadPage(ctx, d, unreadScreen())
	require.NoError(t, err)

	_, rows, ok := strings.Cut(page, string(kitty.Placeholder))
	require.True(t, ok, "the transmissions come first, then the rows")
	require.NotContains(t, rows, "\x1bP", "no kitty remote control")
	require.NotContains(t, rows, "\x1b]52", "no clipboard write")
	require.NotContains(t, rows, "\x1b[2J", "no screen clear")
	// A DCS carries its payload inside the sequence, so dropping the sequence
	// drops the payload with it; the prose around it is what stays.
	require.NotContains(t, ansi.Strip(page), "kitty-cmd")
	require.Contains(t, ansi.Strip(page), "hi")
}
