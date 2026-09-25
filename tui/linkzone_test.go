package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// postOf renders one rich-text body, which is the path a link travels: a text
// message is literal, so its brackets are not markup.
func postOf(content string, st msgStyle) []msgRow {
	return renderRows([]store.Message{{MessageID: "om_1", SenderID: "ou_a", SenderName: "张三",
		MsgType: "post", Content: content, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}, st)
}

func TestBodyRows_ALinkOpensWhereItsLabelIsDrawn(t *testing.T) {
	rows := postOf("看这里 [了解详情](https://example.com/x) 谢谢", baseStyle())
	zones := rowZones(rows)
	require.Len(t, zones, 1)
	require.Equal(t, []string{"https://example.com/x"}, zones[0].urls)
	require.Equal(t, "了解详情", zones[0].label, "the chooser names a link by what the body shows")

	row, ok := zoneRow(rows)
	require.True(t, ok)
	line := ansi.Strip(rowSegText(row))
	require.Equal(t, "了解详情", trimCols(line, zones[0].x0-row.lead.cols(), zones[0].x1-row.lead.cols()),
		"the target covers the label and nothing beside it: %q", line)
}

func TestBodyRows_EveryLinkOnALineIsItsOwnTarget(t *testing.T) {
	rows := postOf("[甲](https://example.com/a) 和 [乙](https://example.com/b)", baseStyle())
	zones := rowZones(rows)
	require.Len(t, zones, 2)
	require.Equal(t, []string{"https://example.com/a"}, zones[0].urls)
	require.Equal(t, []string{"https://example.com/b"}, zones[1].urls)
	require.Less(t, zones[0].x1, zones[1].x0, "the text between them is no target")
}

func TestBodyRows_ALinkWrappedAcrossRowsOpensFromEitherHalf(t *testing.T) {
	st := baseStyle()
	st.width = 28
	label := strings.Repeat("very long label ", 4)
	rows := postOf("["+label+"](https://example.com/x)", st)
	zones := rowZones(rows)
	require.Greater(t, len(zones), 1, "the label did not wrap; the test proves nothing")
	for _, z := range zones {
		require.Equal(t, []string{"https://example.com/x"}, z.urls,
			"both halves of a wrapped label lead to the same place")
		require.LessOrEqual(t, z.x1, leadWidth+st.inner(), "a target never runs past the pane")
	}
}

func TestBodyRows_ALinkInAListSitsUnderTheMarker(t *testing.T) {
	rows := postOf("- [报表](https://example.com/r)", baseStyle())
	row, ok := zoneRow(rows)
	require.True(t, ok, "a link inside a list item is still a target")
	line := ansi.Strip(rowSegText(row))
	require.Contains(t, line, "• ", "the list marker survives the piecewise path")
	z := firstZone(row)
	require.Equal(t, "报表", trimCols(line, z.x0-row.lead.cols(), z.x1-row.lead.cols()),
		"the marker shifted the label, and the target moved with it: %q", line)
}

func TestBodyRows_AnEmojiPictureAndALinkShareALine(t *testing.T) {
	rows := postOf(":DONE: [了解](https://example.com/x)", drawingStyle(t))
	require.Equal(t, 1, picsIn(rows), "the emoji is still drawn as a picture")
	zones := rowZones(rows)
	require.Len(t, zones, 1)
	require.Equal(t, []string{"https://example.com/x"}, zones[0].urls)
	require.Greater(t, zones[0].x0, leadWidth, "the picture pushed the label right")
}

func TestBodyRows_AnEmptyTargetIsNoLink(t *testing.T) {
	rows := postOf("[了解]() 谢谢", baseStyle())
	require.Empty(t, rowZones(rows), "a link with nowhere to go is only a label")
	require.Contains(t, rowText(rows), "了解")
}

func TestBodyRows_AWrittenOutURLIsATargetToo(t *testing.T) {
	rows := bodyOf("看这个 https://example.com/x 谢谢", baseStyle())
	zones := rowZones(rows)
	require.Len(t, zones, 1, "the client makes a written-out URL pressable, so larkim finds it too")
	require.Equal(t, []string{"https://example.com/x"}, zones[0].urls)
}

func TestBodyRows_ASentenceMarkAfterAURLIsNotPartOfIt(t *testing.T) {
	for _, body := range []string{"见 https://example.com/x。", "见 https://example.com/x.", "见 (https://example.com/x)"} {
		zones := rowZones(bodyOf(body, baseStyle()))
		require.Len(t, zones, 1, body)
		require.Equal(t, []string{"https://example.com/x"}, zones[0].urls, body)
	}
}

func TestBodyRows_ASpelledLinkDoesNotAlsoOpenItsTarget(t *testing.T) {
	rows := postOf("[了解](https://example.com/x)", baseStyle())
	zones := rowZones(rows)
	require.Len(t, zones, 1, "the label and the URL behind it are one target, not two")
	require.Equal(t, "了解", zones[0].label)
}

// rowSegText is what a row draws, whether it holds a string or the pieces the
// inline path cuts it into.
func rowSegText(r msgRow) string {
	if len(r.segs) == 0 {
		return r.text
	}
	var b strings.Builder
	for _, s := range r.segs {
		if s.pic.cols > 0 {
			b.WriteString(strings.Repeat(" ", s.pic.cols))
			continue
		}
		b.WriteString(s.text)
	}
	return b.String()
}

// trimCols is the half-open column range [x0, x1) of a stripped line, which
// is what a zone claims.
func trimCols(line string, x0, x1 int) string {
	return strings.TrimSpace(ansi.Truncate(ansi.TruncateLeft(line, x0, ""), x1-x0, ""))
}
