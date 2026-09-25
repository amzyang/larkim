package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestDraftRows_CountsWrappedLines(t *testing.T) {
	require.Equal(t, 1, draftRows("", 20))
	require.Equal(t, 1, draftRows("短", 20))
	require.Equal(t, 3, draftRows("one\ntwo\nthree", 20))
	// A paragraph typed without a newline is one logical line and three rows.
	require.Equal(t, 3, draftRows(strings.Repeat("a", 41), 20))
	// CJK is two columns a glyph, so ten of them fill a twenty-wide box.
	require.Equal(t, 2, draftRows(strings.Repeat("字", 11), 20))
	require.Equal(t, 1, draftRows("anything", 0), "a box with no width still owns a row")
}

func TestComposerHeight_GrowsWithTheDraftAndCapsAtTen(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, inputHeight, m.composerRows().input)

	m.input.SetValue(strings.Repeat("line\n", 5) + "line")
	require.Equal(t, 6, m.composerRows().input)

	m.input.SetValue(strings.Repeat("line\n", 40))
	require.Equal(t, composerMaxRows, m.composerRows().input)
}

func TestComposerHeight_GrowthStopsAtThePanesFloor(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue(strings.Repeat("line\n", 40))
	m.replan()

	// Room to spare: the draft grows to the cap.
	require.Equal(t, composerMaxRows, m.composerRows().input)

	// No room: growing would take rows the message panes need, so it does not
	// grow, and the composer is the box it has always been.
	m.height = minHeight
	require.Equal(t, inputHeight, m.composerRows().input)
	require.Zero(t, m.composerRows().preview)
}

func TestPreview_ShowsOnlyForPostAndImage(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)

	m.input.SetValue("好的")
	m.replan()
	m.layout()
	require.Zero(t, m.composerRows().preview, "a text draft already reads as what it will be")
	require.Empty(t, m.previewRows)

	m.input.SetValue("## 发布说明")
	m.replan()
	m.layout()
	require.NotZero(t, m.composerRows().preview)
	require.NotEmpty(t, m.previewRows)
}

func TestPreview_TogglesOffAndBackOn(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("## 发布说明")
	m.replan()
	m.layout()
	with := m.composerHeight()

	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m = mm.(Model)
	require.False(t, m.previewOpen)
	require.Zero(t, m.composerRows().preview)
	require.Less(t, m.composerHeight(), with)

	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m = mm.(Model)
	require.Equal(t, with, m.composerHeight())
}

func TestPreview_RendersThroughTheMessageBody(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("看 **这里**")
	m.replan()
	m.layout()

	require.Len(t, m.previewRows, 1)
	line, _ := m.rowLine(m.previewRows[0], m.width-2)
	require.Contains(t, ansi.Strip(line), "看 这里", "the preview draws the markup, not its delimiters")
}

func TestRenderInput_BoxIsExactlyAsTallAsItClaims(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	for _, draft := range []string{"好的", "## 发布说明", strings.Repeat("line\n", 6)} {
		m.input.SetValue(draft)
		m.replan()
		m.layout()
		require.Equal(t, m.composerHeight()+2, lipgloss.Height(m.renderInput()), "draft %q", draft)
	}
}

func TestPicturePrepare_ClaimsThePreviewFirst(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/shot.png": 2048})
	m.input.SetValue("![截图](~/shot.png)")
	m.replan()
	m.layout()

	require.NotEmpty(t, m.previewRows)
	// Without a graphics terminal there is nothing to hand over, but the rows
	// still have to be walked: a preview left out reserves cells the terminal
	// was never given.
	require.NotPanics(t, func() { m.picturePrepare() })
}

func TestOnInsertKey_GrowingTheDraftRelaysOutThePanes(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	before := m.bodyHeight()

	// One keystroke that turns a text draft into a post opens the preview,
	// which moves bodyHeight, so the panes have to be rebuilt in the same pass.
	m.input.SetValue("## 发布说明")
	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: '!', Text: "!"})
	m = mm.(Model)

	require.Equal(t, kindPost, m.draft.kind)
	require.NotZero(t, m.composerRows().preview)
	require.Less(t, m.bodyHeight(), before)
	require.Equal(t, m.composerRows().input, m.input.Height(), "the textarea was laid out again")
}

// typeInto drives the real key path a rune at a time, the way the reader does.
func typeInto(m Model, text string) Model {
	for _, r := range text {
		mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(Model)
	}
	return m
}

func TestOnInsertKey_PreviewTracksTheDraftWhileTyping(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = typeInto(mm.(Model), "## 发布说明")

	require.Equal(t, kindPost, m.draft.kind)
	require.NotEmpty(t, m.previewRows)
	line, _ := m.rowLine(m.previewRows[0], m.width-2)
	require.Contains(t, ansi.Strip(line), "发布说明", "the preview shows the draft as it now stands")
}

func TestStartInsert_PreviewIsPlannedBeforeItIsDrawn(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = typeInto(mm.(Model), "## 发布说明")

	// Leaving and re-entering insert mode keeps the draft, so the first frame
	// of the new session has to preview that draft and not the one before it.
	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)
	m.input.SetValue("## 周会纪要")
	mm, _ = m.startInsert(nil, false)
	m = mm.(Model)

	require.NotEmpty(t, m.previewRows)
	line, _ := m.rowLine(m.previewRows[0], m.width-2)
	require.Contains(t, ansi.Strip(line), "周会纪要")
}
