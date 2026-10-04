package keyhint

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testStyles() Styles {
	return Styles{
		Key:  lipgloss.NewStyle().Bold(true),
		Desc: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
}

func TestMacKeys(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Ctrl+f", "⌃F"},
		{"Ctrl+v / Cmd+v", "⌃V / ⌘V"},
		{"Shift+Enter", "⇧↩"},
		{"^r", "⌃R"},
		{"Tab/Shift+Tab", "⇥/⇧⇥"},
		{"j/k", "j/k"},
		{"click", "click"},
		{"Ctrl+d/Ctrl+u", "⌃D/⌃U"},
		{"enter", "↩"},
		{"esc", "⎋"},
		{"Tab/^n/^p", "⇥/⌃N/⌃P"},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, MacKeys(tc.in), "MacKeys(%q)", tc.in)
	}
}

func TestRenderDesc(t *testing.T) {
	got := ansi.Strip(RenderDesc(Binding{Keys: "Ctrl+f", Desc: "search"}, testStyles()))
	require.Contains(t, got, "⌃F")
	require.Contains(t, got, "search")
}

func TestPrefixBar(t *testing.T) {
	got := ansi.Strip(PrefixBar("y", []Binding{
		{Keys: "y", Desc: "id"},
		{Keys: "r", Desc: "json"},
		{Keys: "c", Desc: "content"},
	}, 80, testStyles()))
	require.Contains(t, got, "y")
	require.Contains(t, got, "id")
	require.Contains(t, got, "json")
	require.Contains(t, got, "content")
}

func TestCombined_BarMatchesPrefixBar(t *testing.T) {
	st := testStyles()
	c := Combined{
		Prefix: "g",
		Choices: []Binding{{Keys: "g", Desc: "top"}},
	}
	require.Equal(t,
		PrefixBar("g", c.Choices, 40, st),
		c.Bar(40, st),
	)
}

func TestHintBar_TruncatesToWidth(t *testing.T) {
	bar := HintBar([]Binding{
		{Keys: "Enter", Desc: "send"},
		{Keys: "Shift+Enter", Desc: "newline"},
	}, 8, testStyles())
	require.LessOrEqual(t, lipgloss.Width(bar), 8)
}

func TestPack_reservesHelpAtEnd(t *testing.T) {
	pool := append([]Binding{
		{Keys: "r", Desc: "reply"},
		{Keys: "t", Desc: "thread"},
		{Keys: "e", Desc: "react"},
		{Keys: "o", Desc: "open"},
		{Keys: "n", Desc: "unread"},
		{Keys: "v", Desc: "select"},
		{Keys: "y", Desc: "copy"},
	}, Binding{Keys: "?", Desc: "help"})
	got := Pack(pool, 120, "  ", testStyles())
	require.Contains(t, strings.ToLower(ansi.Strip(got)), "help")
	require.Contains(t, ansi.Strip(got), "?")
}
