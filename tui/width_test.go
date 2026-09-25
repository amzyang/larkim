package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
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
	// No spaces to break at, so the run reaches the forced cut.
	segs := []rowSeg{{text: keycap + strings.Repeat("字", 60)}}
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
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_keycap", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_keycap", ChatID: "oc_keycap",
		MsgType: msgType, SenderID: "ou_a", SenderName: "张三",
		ContentRaw: `{"text":"x"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_keycap", body, "", "", 100))

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
			keycap + strings.Repeat("字", 27) + "x https://example.com/a"},
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
