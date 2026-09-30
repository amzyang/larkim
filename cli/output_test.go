package cli

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestScrub_DropsControlCharactersAndKeepsLayout(t *testing.T) {
	// A sequence goes whole: taking only its ESC would print the payload as text.
	require.Equal(t, "hithere",
		scrub("hi\x1bP@kitty-cmd{\"cmd\":\"launch\"}\x1b\\there"))
	require.Equal(t, "one\ntwo\tthree", scrub("one\ntwo\tthree"),
		"newlines and tabs are layout the printers already count on")
	require.Equal(t, "平台组 张三 🎉", scrub("平台组 张三 🎉"))
	require.Equal(t, "ab", scrub("a\x7f\x9bb"), "DEL and a bare C1 byte go too")
}

func TestInline_KeepsAFieldOnItsOwnRow(t *testing.T) {
	require.Equal(t, "one two three", inline("one\ntwo\tthree"))
	require.Equal(t, "who", inline("who\x1b]0;pwned\x07"))
}

func TestOneLine_CountsCharactersItWillDraw(t *testing.T) {
	// The budget would otherwise be spent on an escape sequence, and the cut
	// could fall inside one.
	require.Equal(t, "abcde", oneLine("\x1b[31mabcde\x1b[0m", 5))
}

func TestWatchLine_StaysOneSafeLine(t *testing.T) {
	line := watchLine(store.Message{
		MessageID: "om_a", ChatID: "oc_a", CreateMs: 1,
		SenderName: "who\x1b]0;pwned\x07\nsecond", ContentRaw: `{"text":"x"}`,
		Content: "hi\x1b[2Jthere", RenderedAt: 5,
	})
	require.NotContains(t, line, "\x1b")
	require.NotContains(t, line, "\n")
	require.Contains(t, line, "hithere")
}
