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
