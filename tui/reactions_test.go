package tui

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const twoReactions = `{"counts":[{"reaction_type":"THUMBSUP","count":"3"},{"reaction_type":"JIAYI","count":"1"}],
  "details":[{"emoji_type":"THUMBSUP","operator":{"operator_id":"ou_me","operator_type":"user"}}]}`

func TestRenderRows_DrawsReactionsBelowTheBody(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "这个方案我同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	out := rowText(renderRows(msgs, baseStyle()))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Contains(t, lines[len(lines)-1], "👍 3", "the reader's own reaction carries the server's total")
	require.Contains(t, lines[len(lines)-1], "[+1] 1", "an emoji with no character is named the way the client names it")
	require.Contains(t, lines[len(lines)-2], "这个方案我同意", "the reactions follow the body, they do not replace it")
}

func TestRenderRows_UnderlinesTheReadersOwnReaction(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	rows := renderRows(msgs, baseStyle())
	strip := segText(rows[len(rows)-1])
	// Underline rather than colour alone, so the reader's own reaction is
	// still the one that stands out where colour does not reach.
	require.Contains(t, strip, stAccent.Underline(true).Render("👍 3"))
	require.Contains(t, strip, stDim.Render("[+1] 1"))
}

func TestRenderRows_WrapsALongReactionStripRatherThanCuttingIt(t *testing.T) {
	// truncate cuts runes, which would split an emoji off its variation
	// selector; the strip is a list of short tokens, so it wraps instead.
	var counts []string
	for _, key := range []string{"THUMBSUP", "ROSE", "HEART", "PARTY", "FIRE", "CAKE", "COFFEE", "BEER", "GIFT", "TROPHY"} {
		counts = append(counts, `{"reaction_type":"`+key+`","count":"1"}`)
	}
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "庆祝", CreateMs: msgAt(23, 9, 0), RenderedAt: 1,
		ReactionsJSON: `{"counts":[` + strings.Join(counts, ",") + `]}`}}
	st := baseStyle()
	st.width = 24
	rows := renderRows(msgs, st)
	for _, r := range rows {
		require.LessOrEqual(t, segsWidth(append([]rowSeg{{text: r.text}}, r.segs...)), st.width,
			"a row never runs past the pane: %q", ansi.Strip(segText(r)))
	}
	out := rowText(rows)
	require.Contains(t, out, "🏆 1", "the last reaction survives the wrap")
}

func TestRenderRows_DropsTheReactionsOfARecalledMessage(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "撤回前的内容", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, Deleted: true, ReactionsJSON: twoReactions}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "(Recalled)")
	require.NotContains(t, out, "👍", "the client drops a recalled message's reactions with its body")
}

func TestClaimReactionRefresh_AsksOnceAChatSettlesAndNotAgainForACooldown(t *testing.T) {
	m := New(Deps{Self: "ou_me", Syncer: &sync.Syncer{}})
	m.chatID = "oc_team"
	now := testNow

	require.True(t, m.claimReactionRefresh("oc_team", now))
	require.False(t, m.claimReactionRefresh("oc_team", now.Add(reactionRefreshCooldown-time.Second)),
		"a revisit inside the cooldown costs no call")
	require.True(t, m.claimReactionRefresh("oc_team", now.Add(reactionRefreshCooldown)))
	require.False(t, m.claimReactionRefresh("oc_elsewhere", now),
		"the cursor moved on before the debounce elapsed")
}

func TestClaimReactionRefresh_StaysQuietBesideADaemon(t *testing.T) {
	// The messages table belongs to whoever holds the data-dir lock; a TUI
	// only reading alongside a daemon must not write it.
	m := New(Deps{Self: "ou_me"})
	m.chatID = "oc_team"
	require.False(t, m.claimReactionRefresh("oc_team", testNow))
}

func TestReactionChip_DrawsAPictureWhereNoCharacterCarriesTheEmoji(t *testing.T) {
	dir := t.TempDir()
	writeTestEmoji(t, dir, "JIAYI")
	st := baseStyle()
	st.emojiDir = dir
	st.place = (&pictures{dataDir: dir, cellW: 10, cellH: 20,
		size: map[string]image.Point{}, failed: map[string]bool{},
		id: map[string]int{}, used: map[string]int64{}}).place

	segs := reactionChip(emoji.Chip{Key: "JIAYI", Count: 2}, st)
	require.Len(t, segs, 2, "the picture and its count are separate pieces")
	require.Positive(t, segs[0].pic.cols, "the emoji is the client's own picture")
	require.Equal(t, 1, segs[0].pic.rows, "a picture on a line of text is one row tall")
	require.Contains(t, ansi.Strip(segs[1].text), "2")

	// A terminal with no graphics, or a data dir the pictures were never cut
	// into, names the emoji instead.
	plain := reactionChip(emoji.Chip{Key: "JIAYI", Count: 2}, baseStyle())
	require.Len(t, plain, 1)
	require.Equal(t, "[+1] 2", ansi.Strip(plain[0].text))
}

func TestReactionChip_ReadsASkinToneAsTheEmojiItIsAToneOf(t *testing.T) {
	segs := reactionChip(emoji.Chip{Key: "DarkThumbsup", Count: 1}, baseStyle())
	require.Equal(t, "👍 1", ansi.Strip(segs[0].text), "a tone Feishu sent but the picker never offered still draws")
}

// writeTestEmoji leaves one emoji picture where emojiPic will look for it.
func writeTestEmoji(t *testing.T, dir, key string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(emoji.Dir(dir), 0o700))
	f, err := os.Create(emoji.Path(dir, key))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, image.NewRGBA(image.Rect(0, 0, 96, 96))))
}

func TestRenderRows_KeepsAnAllCharacterStripAsOrdinaryText(t *testing.T) {
	// The pieces exist so a picture can sit inside a line, and a row made of
	// them takes no selection tint; a strip of characters must not pay that.
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	rows := renderRows(msgs, baseStyle())
	strip := rows[len(rows)-1]
	require.Empty(t, strip.segs, "no chip carries a picture, so the row is plain text")
	require.Contains(t, ansi.Strip(strip.text), "👍 3")
}
