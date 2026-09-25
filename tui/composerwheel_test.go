package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// posting puts the composer in insert mode holding a markdown post long enough
// to overflow both the preview band and the writing area.
func posting(m Model, items int) Model {
	m.mode, m.previewOpen = modeInsert, true
	var b strings.Builder
	b.WriteString("# 发布计划\n")
	for i := range items {
		fmt.Fprintf(&b, "- 第 %d 条\n", i)
	}
	m.input.SetValue(b.String())
	m.input.Focus()
	m.replan()
	m.layout()
	// textarea only learns how far it can scroll when something renders it:
	// its viewport is handed the wrapped draft inside View. A running app has
	// always drawn a frame before a wheel event reaches it.
	m.renderInput()
	return m
}

// composerY is the screen row the given band of the composer starts on.
func composerY(m Model, want composerBand) int {
	for row := range m.composerHeight() {
		if m.composerBand(row) == want {
			return m.bodyHeight() + 3 + row
		}
	}
	return -1
}

func wheelAt(m Model, y int, b tea.MouseButton, n int) Model {
	for range n {
		mm, _ := m.onWheel(tea.Mouse{Button: b, X: 10, Y: y})
		m = mm.(Model)
	}
	return m
}

func TestComposerBand_ResolvesPreviewRuleAndInput(t *testing.T) {
	m := posting(sized(120, 40), 20)
	m.setReply(&m.msgs[0], false)
	r := m.composerRows()
	require.Positive(t, r.preview, "the fixture has to draw a preview")
	require.Positive(t, r.quote, "and a quote, so composerAbove over-fills the box")

	above := m.composerAbove(m.width - 2)
	clip := max(0, len(above)+r.input+r.badge-r.total())
	require.Positive(t, clip, "the preview's rule row is unbudgeted, so the box clips")

	for row := range r.preview - clip {
		require.Equal(t, bandPreview, m.composerBand(row), "row %d", row)
	}
	require.Equal(t, bandOther, m.composerBand(r.preview-clip), "the rule under the preview is not the preview")

	first := len(above) - clip
	require.Equal(t, bandOther, m.composerBand(first-1), "the quote is not the writing area")
	for row := first; row < first+r.input; row++ {
		require.Equal(t, bandInput, m.composerBand(row), "row %d", row)
	}
	require.Equal(t, bandOther, m.composerBand(first+r.input), "the badge is not the writing area")

	// cursorAt counts the same rows; if the two ever disagree the caret lands
	// on a row the wheel thinks belongs to something else.
	require.Equal(t, first, m.cursorAt().Position.Y-(m.bodyHeight()+3)-m.input.Cursor().Y)
}

func TestWheel_PreviewScrollsPastPreviewMaxRows(t *testing.T) {
	m := posting(sized(120, 40), 20)
	require.Greater(t, len(m.previewRows), m.composerRows().preview,
		"the fixture has to render more rows than the band shows")
	head, _ := m.rowLine(m.previewRows[0], m.width-2)
	require.Contains(t, m.composerAbove(m.width - 2)[0], head)

	m = wheelAt(m, composerY(m, bandPreview), tea.MouseWheelDown, 1)

	require.Equal(t, 3, m.previewTop)
	want, _ := m.rowLine(m.previewRows[3], m.width-2)
	require.Contains(t, m.composerAbove(m.width - 2)[0], want, "the band now starts on row 3")
}

func TestWheel_PreviewClampsAtBothEnds(t *testing.T) {
	m := posting(sized(120, 40), 20)
	y := composerY(m, bandPreview)

	m = wheelAt(m, y, tea.MouseWheelUp, 2)
	require.Equal(t, 0, m.previewTop, "the wheel cannot scroll above the first row")

	m = wheelAt(m, y, tea.MouseWheelDown, 40)
	require.Equal(t, m.previewBottom(), m.previewTop, "nor past the last")
	require.Len(t, m.composerAbove(m.width - 2)[:m.composerRows().preview], m.composerRows().preview,
		"the band stays full at the bottom")
}

func TestPreview_TopSurvivesTyping(t *testing.T) {
	m := posting(sized(120, 40), 20)
	m = wheelAt(m, composerY(m, bandPreview), tea.MouseWheelDown, 2)
	top := m.previewTop
	require.Positive(t, top)

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: '好', Text: "好"})
	m = mm.(Model)

	require.Equal(t, top, m.previewTop, "a keystroke must not undo a wheel scroll")
}

func TestPreview_TopClampsWhenTheDraftShrinks(t *testing.T) {
	m := posting(sized(120, 40), 20)
	m = wheelAt(m, composerY(m, bandPreview), tea.MouseWheelDown, 10)
	require.Positive(t, m.previewTop)

	m = posting(m, 2)

	require.Equal(t, m.previewBottom(), m.previewTop)
	require.NotPanics(t, func() { m.composerAbove(m.width - 2) })
}

func TestPreview_TopGoesBackToTheTopWhenThePreviewCloses(t *testing.T) {
	m := posting(sized(120, 40), 20)
	m = wheelAt(m, composerY(m, bandPreview), tea.MouseWheelDown, 2)
	require.Positive(t, m.previewTop)

	m.previewOpen = false
	m.layout()

	require.Equal(t, 0, m.previewTop, "a preview with no rows has nothing to be scrolled to")
}

func TestWheel_WritingAreaMovesTheCaret(t *testing.T) {
	m := posting(sized(120, 40), 30)
	m.input.MoveToEnd()
	require.Positive(t, m.input.ScrollYOffset(), "the draft has to overflow the writing area")
	line, off := m.input.Line(), m.input.ScrollYOffset()
	y := composerY(m, bandInput)

	m = wheelAt(m, y, tea.MouseWheelUp, 1)
	require.Equal(t, line-wheelStep, m.input.Line(), "a notch moves the caret three lines")

	m = wheelAt(m, y, tea.MouseWheelUp, 4)
	require.Less(t, m.input.ScrollYOffset(), off, "and the draft follows it past the top of the box")
	require.LessOrEqual(t, m.input.ScrollYOffset(), m.input.Line(), "the caret stays on screen")
}

func TestWheel_WritingAreaIgnoredOutsideInsert(t *testing.T) {
	m := posting(sized(120, 40), 30)
	m.input.MoveToEnd()
	y := composerY(m, bandInput)
	line := m.input.Line()

	m.mode = modeNormal
	m = wheelAt(m, y, tea.MouseWheelUp, 3)

	require.Equal(t, line, m.input.Line(), "the box belongs to another widget outside insert mode")
}

func TestWheel_ComposerQuoteAndBadgeScrollNothing(t *testing.T) {
	m := posting(sized(120, 40), 20)
	m.setReply(&m.msgs[0], false)
	m = wheelAt(m, composerY(m, bandPreview), tea.MouseWheelDown, 2)
	top, line, msgTop := m.previewTop, m.input.Line(), m.msgTop

	for row := range m.composerHeight() {
		if m.composerBand(row) != bandOther {
			continue
		}
		m = wheelAt(m, m.bodyHeight()+3+row, tea.MouseWheelDown, 1)
	}

	require.Equal(t, top, m.previewTop)
	require.Equal(t, line, m.input.Line())
	require.Equal(t, msgTop, m.msgTop, "and the wheel does not fall through to the panes above")
}

func TestPicturePrepare_ClaimsOnlyTheVisiblePreviewRows(t *testing.T) {
	m := posting(sized(106, 59), 4)
	p := testPictures(t)
	m.pics, m.msgRows = p, nil
	for i := range 12 {
		m.previewRows = append(m.previewRows,
			msgRow{pic: p.place(writePNG(t, p.dataDir, fmt.Sprintf("p%d.png", i), 10, 20), 30, 20)})
	}
	band := m.composerRows().preview
	require.Positive(t, band)
	require.Greater(t, len(m.previewRows), band)
	last := m.previewRows[len(m.previewRows)-1].pic

	m.picturePrepare()
	require.Empty(t, p.cells(last, 0), "a row below the band was never handed to the terminal")

	m.previewTop = m.previewBottom()
	m.picturePrepare()
	require.NotEmpty(t, p.cells(last, 0), "scrolling the band onto it claims it")
}
