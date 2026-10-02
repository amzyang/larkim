package confirm

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testStyles() Styles {
	return Styles{
		Body:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
		Affirm: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")).Underline(true),
		Deny:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Punct:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
	}
}

func TestFormat_KeysAffirmUnderlinedAndDenyDim(t *testing.T) {
	st := testStyles()
	rendered := Format("recall this message? y/n", 80, st)
	require.Contains(t, rendered, st.Affirm.Render("y"))
	require.Contains(t, rendered, st.Deny.Render("n"))
	require.NotContains(t, rendered, st.Affirm.Render("n"))
}

func TestFormat_KeepsSuffixWhenTruncated(t *testing.T) {
	st := testStyles()
	long := "send to 平台组 · " + strings.Repeat("line ", 20) + Suffix
	s := ansi.Strip(Format(long, 28, st))
	require.True(t, strings.HasSuffix(s, "y/n"), "truncated confirm still ends with y/n: %q", s)
	require.Contains(t, s, "…")
}
