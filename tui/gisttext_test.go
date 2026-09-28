package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGistBody_NamesEveryElementWhereItWasWritten(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a picture keeps its place among the words",
			"a\n![Image](img_a)\nb", "a\n[Image]\nb"},
		{"an image message is the reference itself",
			"[Image: img_a]", "[Image]"},
		{"a clip is named rather than keyed",
			"看这个 [Media: file_b]", "看这个 [Video]"},
		{"a file is named by the file",
			"周报\n<file key=\"file_b\" name=\"report.pdf\"/>", "周报\n[File] report.pdf"},
		{"a file with no name is named by its kind",
			"<file key=\"file_b\"/>", "[File]"},
		{"a folder's listing collapses to the folder",
			"<folder key=\"file_b\" name=\"assets\">\n<file key=\"file_c\" name=\"a.png\"/>\n</folder>", "[Folder] assets"},
		{"markup reads as the words it wraps",
			"**要紧** 的 [看板](https://example.com/b)", "要紧 的 看板"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, gistBody(tc.body))
		})
	}
}

// The chat row splits the line at its mentions to colour the one that reaches
// the reader, so the spellings it matches on have to survive this pass.
func TestGistBody_LeavesMentionsForTheRowToColour(t *testing.T) {
	require.Equal(t, `<at user_id="ou_a">张三</at> 看下`, gistBody(`<at user_id="ou_a">张三</at> 看下`))
	require.Equal(t, "@_all 看下", gistBody("@_all 看下"))
}

func TestPlainAt_SpellsAMentionForALineWithNoStyling(t *testing.T) {
	require.Equal(t, "@张三 看下", plainAt(`<at user_id="ou_a">张三</at> 看下`))
	require.Equal(t, "@All 看下", plainAt(`<at user_id="all"></at> 看下`))
	require.Equal(t, "@All 看下", plainAt("@_all 看下"))
	require.Equal(t, "@ou_a 看下", plainAt(`<at user_id="ou_a"></at> 看下`),
		"a tag that carries no name still names somebody")
}
