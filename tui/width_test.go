package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// keycap is the shape x/ansi measures two ways: ansi.Truncate reads the ASCII
// base as a character of its own and stops counting there, so the cluster
// costs one cell to a cut and two to everything else.
const keycap = "1️⃣"

func TestCut_AKeycapNeverOutgrowsItsBudget(t *testing.T) {
	s := keycap + strings.Repeat("字", 40)
	require.Equal(t, 2, ansi.StringWidth(keycap), "the terminal draws a keycap in two cells")
	for w := range 40 {
		require.LessOrEqual(t, ansi.StringWidth(cut(s, w)), w,
			"a cut to %d columns may not come back wider", w)
	}
}

func TestCut_ARunOfKeycapsKeepsWhatFits(t *testing.T) {
	s := strings.Repeat(keycap, 60)
	for w := 2; w <= 20; w++ {
		require.Equal(t, strings.Repeat(keycap, w/2), cut(s, w), "a cut to %d columns", w)
	}
}

func TestWrap_ARunOfKeycapsWiderThanTheRowEnds(t *testing.T) {
	s := strings.Repeat(keycap, 60)
	done := make(chan []string, 1)
	go func() { done <- wrap(s, 20) }()
	select {
	case lines := <-done:
		require.Len(t, lines, 6)
		for _, l := range lines {
			require.Equal(t, strings.Repeat(keycap, 10), l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wrap made no headway through the run")
	}
}

func TestWrap_ARowCarriesOnlyTheEscapesInForce(t *testing.T) {
	// lipgloss underlines a rune at a time, so a paragraph is an escape pair
	// per character; a row opening on all of them is a row the size of the
	// whole paragraph.
	s := lipgloss.NewStyle().Underline(true).Render(strings.Repeat("lorem ipsum ", 400))
	var text strings.Builder
	total := 0
	for _, l := range wrap(s, 40) {
		text.WriteString(ansi.Strip(l))
		total += len(l)
	}
	require.Equal(t, strings.Repeat("loremipsum", 400), strings.ReplaceAll(text.String(), " ", ""))
	require.Less(t, total, 2*len(s), "the rows together are about as big as what they draw")
}

func TestWrap_KeepsTextAnEscapeCutsAClusterOf(t *testing.T) {
	// Underlined a rune at a time, a keycap is a digit and two marks to the
	// width a cut measures, and one cluster to the break; measured the two
	// ways, the row after it lost its first letter.
	s := lipgloss.NewStyle().Underline(true).Render(keycap + "a ab")
	var rows []string
	for _, l := range wrap(s, 4) {
		require.Equal(t, 4, ansi.StringWidth(l))
		rows = append(rows, strings.TrimRight(ansi.Strip(l), " "))
	}
	require.Equal(t, []string{keycap + "a", "ab"}, rows)
}

func BenchmarkWrap_AParagraphWithNoBreakInIt(b *testing.B) {
	s := strings.Repeat("中文字符测试", 3334)
	for b.Loop() {
		wrap(s, 100)
	}
}

func TestCut_KeepsWhatAlreadyFits(t *testing.T) {
	s := keycap + "ab"
	require.Equal(t, s, cut(s, 4))
	require.Equal(t, s, cut(s, 99))
	require.Equal(t, "", cut(s, 0))
}

func TestCutLeft_NeverKeepsAColumnItWasAskedToDrop(t *testing.T) {
	s := keycap + "ab" + keycap
	full := ansi.StringWidth(s)
	for w := range full + 1 {
		require.LessOrEqual(t, ansi.StringWidth(cutLeft(s, w)), max(0, full-w),
			"dropping %d columns must leave no more than the rest", w)
	}
	// On a cluster boundary the rest is exact: this is the only case the
	// wrapping ever asks for, and a column lost there is a character lost.
	for _, w := range []int{0, 2, 3, 4, 6} {
		require.Equal(t, full-w, ansi.StringWidth(cutLeft(s, w)), "dropping %d columns", w)
	}
}

func TestFit_AKeycapLineStaysInsideThePane(t *testing.T) {
	s := keycap + strings.Repeat("字", 40)
	for w := 1; w < 40; w++ {
		require.Equal(t, w, lipgloss.Width(fit(s, w)), "fit to %d columns", w)
	}
}

func TestWrapSegs_AnUnbreakableRunWithAKeycapFitsTheRow(t *testing.T) {
	// A digit and the letters after it break nowhere, so the run reaches the
	// forced cut.
	segs := []rowSeg{{text: keycap + strings.Repeat("x", 120)}}
	for w := 4; w < 40; w++ {
		for i, row := range wrapSegs(segs, w) {
			got := 0
			for _, s := range row {
				got += ansi.StringWidth(s.text)
			}
			require.LessOrEqual(t, got, w, "row %d packed to %d columns", i, w)
		}
	}
}

// bodyModel is a model over a real store holding one chat whose only message
// is body, rendered.
func bodyModel(t *testing.T, msgType, body string) (Model, *store.Store) {
	t.Helper()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.EnsureChat(ctx, "oc_keycap", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_keycap", ChatID: "oc_keycap",
		MsgType: msgType, SenderID: "ou_a", SenderName: "张三",
		ContentRaw: `{"text":"x"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_keycap", body, "", 100))

	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 101, 59
	return m, st
}

func TestView_NoFrameLineOutgrowsTheTerminal(t *testing.T) {
	for _, c := range []struct{ name, msgType, body string }{
		// The run is one column past the body a 101-column terminal leaves
		// and has nowhere to break, so it reaches the cut that reads the
		// keycap short and hands the whole of it back. The link is what puts
		// the line on the piece-wise path, where a row is padded to the pane
		// rather than cut to it.
		{"a keycap in an unbreakable run", "text",
			keycap + strings.Repeat("x", 55) + " https://example.com/a"},
		// An ordered marker stands in columns the indent did not charge for,
		// so a numbered item carrying a link arrives a column over as well.
		{"a numbered item carrying a link", "post",
			"1. " + strings.Repeat("字", 30) + " https://example.com/a"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, st := bodyModel(t, c.msgType, c.body)
			m.chatID = "oc_keycap"
			m = arrive(t, m, st, "oc_keycap")

			require.Equal(t, m.messagesWidth(), lipgloss.Width(m.renderMessages(m.bodyHeight())),
				"the messages pane may not grow past its share of the terminal")
			for i, l := range strings.Split(m.View().Content, "\n") {
				require.LessOrEqual(t, lipgloss.Width(l), m.width, "frame line %d", i)
			}
		})
	}
}

func TestTruncate_ClosesTheColourItCutsThrough(t *testing.T) {
	s := stDim.Render("THUMBSUP") + " 赞"
	for n := 1; n <= lipgloss.Width(s); n++ {
		got := truncate(s, n)
		require.LessOrEqual(t, lipgloss.Width(got), n, "n=%d got=%q", n, got)
		// A run left open runs on into whatever is drawn beside it — the next
		// cell of the picker's grid. The last thing the cut leaves has to be
		// the reset that closes it.
		if i := strings.LastIndex(got, "\x1b["); i >= 0 {
			require.True(t, sgrReset.MatchString(got[i:]), "n=%d leaves a colour open: %q", n, got)
		}
	}
}

func TestTruncate_KeepsAGraphemeClusterWhole(t *testing.T) {
	// A keycap is an ASCII digit, a variation selector and U+20E3: two columns
	// the terminal draws as one shape, and half of it is not a shape at all.
	require.Equal(t, "…", truncate("1️⃣x", 2), "no room for the keycap and the mark both")
	require.Equal(t, "1️⃣…", truncate("1️⃣xy", 3))
}

func TestCut_ClosesAHyperlinkItCutThrough(t *testing.T) {
	line := hyperlink("https://example.com/x", "一个很长的标签")
	out := cut(line, 4)
	require.Equal(t, "一个", ansi.Strip(out))
	require.True(t, strings.HasSuffix(out, ansi.ResetHyperlink()),
		"a link left open runs on into every cell drawn after it")
}

func TestCut_LeavesAWholeHyperlinkAlone(t *testing.T) {
	line := hyperlink("https://example.com/x", "ab")
	require.Equal(t, line, cut(line, 10), "nothing was cut, so nothing needs closing")
	require.Equal(t, 1, strings.Count(cut(line, 10), ansi.ResetHyperlink()))
}

func TestCut_LeavesTextWithNoHyperlinkAlone(t *testing.T) {
	require.Equal(t, "ab", cut("abcd", 2))
}
