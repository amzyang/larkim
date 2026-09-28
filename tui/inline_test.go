package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// drawingStyle is a render context that can draw the emoji pictures, with the
// two the tests use already cut into its data dir.
func drawingStyle(t *testing.T) msgStyle {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"DONE", "GET", "JIAYI"} {
		writeTestEmoji(t, dir, key)
	}
	st := baseStyle()
	st.dataDir = dir
	st.place = picturesIn(dir).place
	return st
}

func bodyOf(content string, st msgStyle) []msgRow {
	return renderRows([]store.Message{{MessageID: "om_1", SenderID: "ou_a", SenderName: "张三",
		MsgType: "text", Content: content, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}, st)
}

func picsIn(rows []msgRow) int {
	n := 0
	for _, r := range rows {
		for _, s := range r.segs {
			if s.pic.cols > 0 {
				n++
			}
		}
	}
	return n
}

func TestBodyRows_DrawsAnEmojiWithNoGlyphAsAPicture(t *testing.T) {
	rows := bodyOf("abc[完成]def[了解]", drawingStyle(t))
	require.Equal(t, 2, picsIn(rows), "both emoji are the client's own pictures")
	out := rowText(rows)
	require.NotContains(t, out, "[完成]")
	require.NotContains(t, out, "[了解]")
	require.Contains(t, out, "abc")
	require.Contains(t, out, "def")
}

func TestBodyRows_DrawsAPostShortcodeAsAPicture(t *testing.T) {
	rows := bodyOf("abc:Get:", drawingStyle(t))
	require.Equal(t, 1, picsIn(rows))
	require.NotContains(t, rowText(rows), ":Get:")
}

func TestBodyRows_KeepsTheSpellingWhereNoPictureWasCut(t *testing.T) {
	rows := bodyOf("abc[完成]", baseStyle())
	require.Zero(t, picsIn(rows), "a terminal without graphics draws no picture")
	require.Contains(t, rowText(rows), "[完成]")
}

func TestBodyRows_LeavesAnEmojiWithAGlyphAsText(t *testing.T) {
	rows := bodyOf("谢谢[双手合十]", drawingStyle(t))
	require.Zero(t, picsIn(rows), "a character carries this one, so no picture is placed")
	require.Contains(t, rowText(rows), "谢谢🙏")
}

func TestBodyRows_WrapsALongLineWithoutBreakingThePicture(t *testing.T) {
	st := drawingStyle(t)
	rows := bodyOf(strings.Repeat("排期已经确认", 12)+"[完成]"+strings.Repeat("收到", 12), st)
	require.Equal(t, 1, picsIn(rows))
	for _, r := range rows {
		w := ansi.StringWidth(r.text)
		if len(r.segs) > 0 {
			w = segsWidth(r.segs)
		}
		require.LessOrEqual(t, w, st.width, "no row runs past the pane")
	}
}

func TestBodyRows_LeavesALinkLabelAlone(t *testing.T) {
	rows := bodyOf("看这里 [了解](https://example.com/x) 谢谢", drawingStyle(t))
	require.Zero(t, picsIn(rows), "a link label is not an emoji")
	require.Contains(t, rowText(rows), "了解")
}

func TestBodyRows_TintsABodyRowThatCarriesAnEmojiPicture(t *testing.T) {
	rows := bodyOf("abc[完成]def", drawingStyle(t))
	var body msgRow
	for _, r := range rows {
		if len(r.segs) > 0 {
			body = r
		}
	}
	require.NotEmpty(t, body.segs)
	require.True(t, body.tinted, "a selection still colours a body line, picture and all")
}

func TestReactionRows_StayUntinted(t *testing.T) {
	st := drawingStyle(t)
	rows := renderRows([]store.Message{{MessageID: "om_a", SenderID: "ou_a", SenderName: "张三",
		Content: "同意", CreateMs: msgAt(23, 9, 0), RenderedAt: 1,
		ReactionsJSON: `{"counts":[{"reaction_type":"JIAYI","count":"2"}]}`}}, st)
	for _, r := range rows {
		if len(r.segs) > 0 {
			require.False(t, r.tinted, "a chip carries a tint of its own")
		}
	}
}

func TestHyperlink_NamesTheTargetAndClosesAfterIt(t *testing.T) {
	out := hyperlink("https://example.com/x", "详情")
	require.True(t, strings.HasPrefix(out, "\x1b]8;id="))
	require.Contains(t, out, ";https://example.com/x\a详情")
	require.True(t, strings.HasSuffix(out, ansi.ResetHyperlink()))
	require.Equal(t, "详情", ansi.Strip(out))
	require.Equal(t, "详情", hyperlink("", "详情"), "no target, no link")
}

func TestHyperlink_TheHalvesOfAWrappedLinkShareOneName(t *testing.T) {
	head, tail := hyperlink("https://example.com/x", "上"), hyperlink("https://example.com/x", "下")
	id, _, _ := strings.Cut(strings.TrimPrefix(head, "\x1b]8;"), ";")
	require.Contains(t, tail, id, "the terminal hovers the two fragments as the one link they are")
	require.NotContains(t, hyperlink("https://example.com/y", "别处"), id)
}

func TestFileURL_EscapesWhatAPathMayHoldAndAURLMayNot(t *testing.T) {
	require.Equal(t, "file:///Users/linlan/a%20b/%E6%8A%A5%E5%91%8A.pdf",
		fileURL("/Users/linlan/a b/报告.pdf"))
	require.Empty(t, fileURL(""), "nothing downloaded, nowhere to lead")
}

func TestJoinSegs_LinksOnlyTheSegmentsThatLeadSomewhere(t *testing.T) {
	segs := []rowSeg{{text: "见 "}, {text: stLink.Render("详情"), urls: []string{"https://example.com/x"}}}
	out := Model{}.joinSegs(segs, 20)
	require.Contains(t, out, ";https://example.com/x\a")
	require.Equal(t, 1, strings.Count(out, "\x1b]8;id="), "the plain run is not a link")
	require.Equal(t, 1, strings.Count(out, ansi.ResetHyperlink()))
	require.Equal(t, "见 详情"+strings.Repeat(" ", 13), ansi.Strip(out))
}

func TestJoinSegs_ClosesALinkTheRowWidthCutThrough(t *testing.T) {
	segs := []rowSeg{{text: stLink.Render("很长的标签"), urls: []string{"https://example.com/x"}}}
	out := Model{}.joinSegs(segs, 4)
	require.True(t, strings.HasSuffix(out, ansi.ResetHyperlink()))
	require.Equal(t, "很长", ansi.Strip(out))
}

func TestWrapSegs_FillsTheRowWithCJKRatherThanOrphaningWhatCameBefore(t *testing.T) {
	// CJK breaks nowhere, so a run that will not fit whole is cut where the
	// row ends. Moving it down instead would leave the piece before it — a
	// list marker, a mention — alone on a row of its own.
	rows := wrapSegs([]rowSeg{{text: "1. "}, {text: strings.Repeat("中", 40)}}, 20)
	require.Greater(t, len(rows), 1, "the run did not wrap; the test proves nothing")
	require.Equal(t, 2, len(rows[0]), "the marker keeps company on its row")
	for i, row := range rows {
		require.LessOrEqual(t, segsWidth(row), 20, "row %d runs past the width", i)
	}
	var got string
	for _, row := range rows {
		for _, s := range row {
			got += s.text
		}
	}
	require.Equal(t, "1. "+strings.Repeat("中", 40), got, "nothing was dropped in the cutting")
}

func TestWrapSegs_ALongWordStillMovesToARowOfItsOwn(t *testing.T) {
	// A word that would fit on an empty row is not cut to fill this one.
	rows := wrapSegs([]rowSeg{{text: "ab "}, {text: "supercalifragilistic"}}, 22)
	require.Len(t, rows, 2)
	require.Equal(t, "supercalifragilistic", rows[1][0].text)
}
