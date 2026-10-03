package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

func TestSafeLine_KeepsWhatTheRendererWrote(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
	}{
		{"colour", "\x1b[31m张三\x1b[0m: 你好"},
		{"truecolour", "\x1b[38;2;255;0;0mred\x1b[m"},
		{"reset", "a\x1b[mb"},
		{"link", "\x1b]8;;https://example.com\x07docs\x1b]8;;\x07"},
		{"placeholder", string(kitty.Placeholder) + "̅̅"},
		{"plain", "看这个 平台组 — Enter opens the chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.line, safeLine(tc.line))
		})
	}
}

func TestSafeLine_DropsEverythingElse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"kitty remote control", "hi\x1bP@kitty-cmd{\"cmd\":\"launch\"}\x1b\\there", "hithere"},
		{"picture transmission", "hi\x1b_Ga=T,f=100;AAAA\x1b\\there", "hithere"},
		{"clipboard write", "hi\x1b]52;c;cGF5bG9hZA==\x07there", "hithere"},
		{"window title", "hi\x1b]0;pwned\x07there", "hithere"},
		{"cursor motion", "hi\x1b[2;3Hthere", "hithere"},
		{"screen clear", "hi\x1b[2Jthere", "hithere"},
		{"private sgr form", "hi\x1b[>4;2mthere", "hithere"},
		{"c0 byte", "hi\x07\x08there", "hithere"},
		{"delete", "hi\x7fthere", "hithere"},
		// ESC t is a whole two-byte sequence, so the t goes with it. A terminal
		// reading this line loses the t as well.
		{"bare escape", "hi\x1bthere", "hihere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, safeLine(tc.line))
		})
	}
}

// A lead byte with no rune behind it would let the byte after it through as one
// of its continuation bytes, and Feishu carries whatever a sender typed.
func TestSafeLine_TakesNoEscapeThroughMalformedUTF8(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"hi\xe4\x1b[2Jthere",              // a 3-byte lead, then an escape
		"hi\xf0\x9f\x1bP@kitty-cmd\x1b\\", // two bytes of four, then a DCS
		"hi\xc3there",                     // a 2-byte lead over ASCII
	} {
		require.NotContains(t, safeLine(line), "\x1b", line)
	}
	require.Contains(t, safeLine("hi\xe4\x1b[2Jthere"), "there",
		"the text past the malformed rune is still drawn")
}

// A line carrying no escape at all is returned as it came, and a multi-byte
// rune is not mistaken for sequence bytes on the way through the filter.
func TestSafeLine_LeavesTextAlone(t *testing.T) {
	t.Parallel()
	require.Equal(t, "平台组 张三 🎉", safeLine("平台组 张三 🎉"))
	require.Equal(t, "平台组\x1b[31m张三\x1b[0m", safeLine("平台组\x1b[31m张三\x1b[0m"))
	require.Equal(t, "平台组张三", safeLine("平台组\x1b[2J张三"))
}
