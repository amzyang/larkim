package tui

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
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
	require.Contains(t, lines[len(lines)-1], "👍 You +2", "the reader is named, and the two Feishu did not name are counted")
	require.Contains(t, lines[len(lines)-1], "[+1] +1", "an emoji with no character is named the way the client names it")
	require.Contains(t, lines[len(lines)-2], "这个方案我同意", "the reactions follow the body, they do not replace it")
}

func TestRenderRows_StylesEveryReactorAlike(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	rows := renderRows(msgs, baseStyle())
	strip := segText(rows[len(rows)-1])
	// 你 among the reactors is what marks a chip as the reader's own, so
	// nothing on the strip needs a colour of its own to say it.
	require.Contains(t, strip, stChip.Render("👍 You +2"))
	require.Contains(t, strip, stChip.Render("[+1] +1"))
	require.NotContains(t, strip, stAccent.Underline(true).Render("👍 You +2"))
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
	require.Contains(t, out, "🏆 +1", "the last reaction survives the wrap")
}

func TestRenderRows_DropsTheReactionsOfARecalledMessage(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "撤回前的内容", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, Deleted: true, ReactionsJSON: twoReactions}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "(Recalled)")
	require.NotContains(t, out, "👍", "the client drops a recalled message's reactions with its body")
}

func TestReactionChip_DrawsAPictureWhereNoCharacterCarriesTheEmoji(t *testing.T) {
	dir := t.TempDir()
	writeTestEmoji(t, dir, "JIAYI")
	st := baseStyle()
	st.dataDir = dir
	st.place = picturesIn(dir).place

	segs := reactionChip(emoji.Chip{Key: "JIAYI", Count: 2}, st)
	require.Len(t, segs, 3, "the cap, the picture and the rest of the chip are separate pieces")
	require.Equal(t, chipLeft, ansi.Strip(segs[0].text))
	require.Positive(t, segs[1].pic.cols, "the emoji is the client's own picture")
	require.Equal(t, 1, segs[1].pic.rows, "a picture on a line of text is one row tall")
	require.True(t, segs[1].pic.chip, "the cells it sits in carry the chip's tint")
	require.Equal(t, "+2"+chipRight, ansi.Strip(segs[2].text),
		"the reactors close the chip the picture opened, spaced off it by nothing but the slack "+
			"left over from rounding the picture's width up to the grid")

	// A terminal with no graphics, or a data dir the pictures were never cut
	// into, names the emoji instead — on the same chip.
	plain := reactionChip(emoji.Chip{Key: "JIAYI", Count: 2}, baseStyle())
	require.Len(t, plain, 1, "a chip of characters alone stays one piece")
	require.Equal(t, chipped("[+1] +2"), ansi.Strip(plain[0].text))
}

func TestReactionChip_ReadsASkinToneAsTheEmojiItIsAToneOf(t *testing.T) {
	segs := reactionChip(emoji.Chip{Key: "DarkThumbsup", Count: 1}, baseStyle())
	require.Equal(t, chipped("👍 +1"), ansi.Strip(segs[0].text),
		"a tone Feishu sent but the picker never offered still draws")
}

// picturesIn is a renderer for a terminal that draws pictures out of dir,
// with the cell size a query would otherwise report.
func picturesIn(dir string) *pictures {
	return &pictures{dataDir: dir, cellW: 10, cellH: 20,
		size: map[string]image.Point{}, failed: map[string]bool{},
		id: map[string]int{}, used: map[string]int64{}, drew: map[string]bool{}}
}

// writeTestEmoji leaves one emoji picture where emojiChip will look for it.
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
	require.Contains(t, ansi.Strip(strip.text), "👍 You +2")
}

// namedStyle is baseStyle with the contacts a chip's reactors are named from.
func namedStyle() msgStyle {
	st := baseStyle()
	st.people = map[string]string{"ou_a": "张三", "ou_b": "李四", "ou_c": "王五", "ou_d": "构建机器人"}
	return st
}

// chipped is how a chip of characters alone reads with the styling taken
// off: a cap either side of the text, which the caps are kept clear of.
func chipped(s string) string { return chipLeft + s + chipRight }

func reacted(key string, count int, ids ...string) emoji.Chip {
	return emoji.Chip{Key: key, Count: count, Operators: ids}
}

func TestReactionChip_NamesUpToThreeReactors(t *testing.T) {
	segs := reactionChip(reacted("THUMBSUP", 3, "ou_a", "ou_b", "ou_c"), namedStyle())
	require.Equal(t, chipped("👍 张三, 李四, 王五"), ansi.Strip(segs[0].text))
}

func TestReactionChip_CountsTheRestAsPlusN(t *testing.T) {
	segs := reactionChip(reacted("THUMBSUP", 9, "ou_a", "ou_b", "ou_c", "ou_d"), namedStyle())
	require.Equal(t, chipped("👍 张三, 李四, 王五 +6"), ansi.Strip(segs[0].text),
		"a fourth name costs more width than it tells, and the rest is a number")
}

func TestReactionChip_CallsTheReaderYou(t *testing.T) {
	segs := reactionChip(reacted("THUMBSUP", 2, "ou_me", "ou_a"), namedStyle())
	require.Equal(t, chipped("👍 You, 张三"), ansi.Strip(segs[0].text))
}

func TestReactionChip_LeavesAStrangerInThePlusN(t *testing.T) {
	// A raw open id on screen says nothing, so somebody the contacts table
	// has never seen is counted rather than named.
	segs := reactionChip(reacted("THUMBSUP", 2, "ou_stranger", "ou_a"), namedStyle())
	require.Equal(t, chipped("👍 张三 +1"), ansi.Strip(segs[0].text))

	none := reactionChip(reacted("THUMBSUP", 2, "ou_stranger"), namedStyle())
	require.Equal(t, chipped("👍 +2"), ansi.Strip(none[0].text), "nobody to name leaves the total alone")
}

func TestReactionChip_KeepsOneChipInsideThePane(t *testing.T) {
	// The strip wraps between chips but never cuts inside one, so a chip
	// crowded with names has to fit itself. A single width would pass on the
	// parity of the name's cells alone, so every width the pane can take is
	// asked, under a name that cuts evenly and one that cannot.
	for _, name := range []string{strings.Repeat("长", 20), strings.Repeat("a", 40)} {
		for w := 10; w <= 80; w++ {
			st := namedStyle()
			st.width = w
			st.people["ou_a"] = name
			segs := reactionChip(reacted("THUMBSUP", 1, "ou_a"), st)
			require.LessOrEqualf(t, segsWidth(segs), st.inner(), "width %d, name %q", w, name)
		}
	}
}

// chipZones is a strip's click targets in the order they are drawn, across
// however many rows the strip wrapped onto.
func chipZones(rows []msgRow) []clickZone {
	var out []clickZone
	for _, r := range rows {
		out = append(out, r.zones...)
	}
	return out
}

func TestReactionRows_MarksEveryChipAsAClickTarget(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "这个方案我同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	zs := chipZones(renderRows(msgs, baseStyle()))
	require.Len(t, zs, 2, "one target per chip, so a press reaches the emoji under it")
	require.Equal(t, []string{"THUMBSUP", "JIAYI"}, []string{zs[0].react, zs[1].react})
	require.Equal(t, leadWidth, zs[0].x0, "the strip opens where the sender's disc ends")
	require.Less(t, zs[0].x1, zs[1].x0, "the gap between chips belongs to neither")
	for _, z := range zs {
		require.True(t, z.live(), "a chip leads somewhere even carrying no url")
		require.True(t, z.hit(z.x0) && !z.hit(z.x1), "the range is half-open")
	}
}

func TestReactionRows_ZonesLandOnTheChipTheyName(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "这个方案我同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	rows := renderRows(msgs, baseStyle())
	line := len(rows) - 1
	strip := rows[line]
	// Sliced by columns, not runes: a zone is measured in cells, and every
	// emoji on the strip takes two of them for one rune. The zones count from
	// the row's left edge, the text from where the lead ends.
	drawn := ansi.Strip(strip.text)
	at := func(z clickZone) string {
		return cut(cutLeft(drawn, z.x0-strip.lead.cols()), z.x1-z.x0)
	}
	for _, tc := range []struct {
		x    int
		want string
	}{
		{strip.zones[0].x0, "👍 You +2"},
		{strip.zones[1].x0, "[+1] +1"},
	} {
		z, ok := zoneAt(rows, line, tc.x)
		require.True(t, ok, "column %d is inside a chip", tc.x)
		require.Contains(t, at(z), tc.want,
			"the target covers the chip it names, not its neighbour")
	}
	_, ok := zoneAt(rows, line, strip.zones[0].x1)
	require.False(t, ok, "the gap between two chips presses neither")
}

func TestReactionRows_WrapKeepsEachChipsTargetOnItsOwnRow(t *testing.T) {
	keys := []string{"THUMBSUP", "ROSE", "HEART", "PARTY", "FIRE", "CAKE", "COFFEE", "BEER", "GIFT", "TROPHY"}
	var counts []string
	for _, key := range keys {
		counts = append(counts, `{"reaction_type":"`+key+`","count":"1"}`)
	}
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "庆祝", CreateMs: msgAt(23, 9, 0), RenderedAt: 1,
		ReactionsJSON: `{"counts":[` + strings.Join(counts, ",") + `]}`}}
	st := baseStyle()
	st.width = 24
	rows := renderRows(msgs, st)
	var got []string
	for _, r := range rows {
		for _, z := range r.zones {
			got = append(got, z.react)
			require.GreaterOrEqual(t, z.x0, leadWidth, "a target never reaches into the lead")
			require.LessOrEqual(t, z.x1, st.width, "a target never runs past the pane")
		}
	}
	require.Equal(t, keys, got, "every chip is still pressable after the strip wrapped")
}

func TestReactionRows_DrawAPressAheadOfFeishusAnswer(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "这个方案我同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	st := baseStyle()
	st.reacts = map[string]map[string]bool{"om_a": {"JIAYI": true}}
	out := rowText(renderRows(msgs, st))
	require.Contains(t, out, "[+1] You +1", "the press draws before it is sent")
	require.Contains(t, out, "👍 You +2", "the chip beside it is left alone")
}

func TestReactionRows_ClosesAChipAPressEmptied(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1,
		ReactionsJSON: `{"counts":[{"reaction_type":"THUMBSUP","count":"1"}],
		  "details":[{"emoji_type":"THUMBSUP","operator":{"operator_id":"ou_me"}}]}`}}
	st := baseStyle()
	st.reacts = map[string]map[string]bool{"om_a": {"THUMBSUP": false}}
	rows := renderRows(msgs, st)
	require.Empty(t, chipZones(rows), "the strip went with the last reaction on it")
	require.NotContains(t, rowText(rows), "👍")
}

func TestSelectedZones_LeavesReactionChipsOut(t *testing.T) {
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = 120, 36
	m.chatID = "oc_team"
	m.msgsBase = []store.Message{{MessageID: "om_a", ChatID: "oc_team", SenderID: "ou_a", SenderName: "张三",
		Content: "这个方案我同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, ReactionsJSON: twoReactions}}
	m.applyOutbox()
	m.layout()
	m.focus, m.msgIdx = paneMessages, 0
	m.rebuildMessages()
	require.NotEmpty(t, chipZones(m.msgRows), "the strip is drawn, so there is something to leave out")
	require.Empty(t, m.selectedZones(), "o opens places; a chip is not one, and e reaches it instead")
}
