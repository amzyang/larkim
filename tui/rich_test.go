package tui

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// dataLiteral spells html the way `osascript -e 'the clipboard as «class
// HTML»'` prints it, which is the only form htmlMarkdown ever sees.
func dataLiteral(html string) string {
	return "«data HTML" + hex.EncodeToString([]byte(html)) + "»"
}

func TestOsaData_UnwrapsTheDataLiteral(t *testing.T) {
	got, err := osaData(dataLiteral("<p>好的</p>"))
	require.NoError(t, err)
	require.Equal(t, "<p>好的</p>", string(got))

	// osascript ends its output with a newline, which readClipboard already
	// trims, but the literal must survive one either way.
	got, err = osaData(dataLiteral("<b>x</b>") + "\n")
	require.NoError(t, err)
	require.Equal(t, "<b>x</b>", string(got))

	for _, bad := range []string{"", "hello", "«data HTML48656C6C6F", "«data HTML好»"} {
		_, err := osaData(bad)
		require.Error(t, err, bad)
	}
}

func TestHTMLMarkdown_KeepsWhatThePostCanCarry(t *testing.T) {
	md, err := htmlMarkdown(dataLiteral(
		`<h2>发布说明</h2>` +
			`<ul><li>修了<strong>同步</strong></li><li><del>回滚</del>见<a href="https://example.com/b">看板</a></li></ul>` +
			`<blockquote>下周再发</blockquote>` +
			`<pre><code>x := 1</code></pre>`))
	require.NoError(t, err)

	require.Contains(t, md, "## 发布说明")
	require.Contains(t, md, "- 修了**同步**")
	require.Contains(t, md, "~~回滚~~")
	require.Contains(t, md, "[看板](https://example.com/b)")
	require.Contains(t, md, "> 下周再发")
	require.Contains(t, md, "```")
	require.Contains(t, md, "x := 1")
	require.Equal(t, kindPost, classify(md))
}

func TestHTMLMarkdown_TableSurvivesAsGFM(t *testing.T) {
	md, err := htmlMarkdown(dataLiteral(
		`<table><thead><tr><th>项</th><th>值</th></tr></thead>` +
			`<tbody><tr><td>待认领</td><td>13</td></tr></tbody></table>`))
	require.NoError(t, err)

	require.Contains(t, md, "| 项")
	require.Contains(t, md, "| 待认领")
	require.Equal(t, kindPost, classify(md), "the composer reads it back as a table")
}

func TestHTMLMarkdown_ImageKeepsItsURL(t *testing.T) {
	md, err := htmlMarkdown(dataLiteral(`<p>看这个 <img src="https://example.com/chart.png"></p>`))
	require.NoError(t, err)

	require.Contains(t, md, "![](https://example.com/chart.png)")
	// resolveImage takes the URL from here and fetchRemote uploads it at send.
	p, err := draftFiles{}.planDraft(md)
	require.NoError(t, err)
	require.Equal(t, kindPost, p.kind)
	require.Equal(t, "https://example.com/chart.png", p.images[0].url)
}

func TestHTMLMarkdown_ReadsTheCharsetTheSourceDeclared(t *testing.T) {
	// 0xE9 is é in windows-1252 and not valid UTF-8 on its own.
	md, err := htmlMarkdown("«data HTML" +
		hex.EncodeToString([]byte("<meta charset=\"windows-1252\"><h2>caf")) + "e9" +
		hex.EncodeToString([]byte("</h2>")) + "»")
	require.NoError(t, err)
	require.Equal(t, "## café", md)
}

func TestPickPaste_FormattingWins(t *testing.T) {
	md, err := htmlMarkdown(dataLiteral(`<h2>发布说明</h2><p>今天上线</p>`))
	require.NoError(t, err)

	require.Equal(t, md, pickPaste(md, "发布说明\n今天上线"))
}

func TestPickPaste_PlainLookingHTMLKeepsTheTextFlavour(t *testing.T) {
	// An editor that copies with syntax highlighting — VS Code does, by
	// default — puts styled spans on the pasteboard for what is only code.
	md, err := htmlMarkdown(dataLiteral(
		`<div><span style="color:#001080">read_state</span><span>.local_read_at</span></div>` +
			`<div><span>ORDER BY create_ms, message_position</span></div>`))
	require.NoError(t, err)
	require.Contains(t, md, `\_`, "the converter escaped what was never markdown")

	text := "read_state.local_read_at\nORDER BY create_ms, message_position"
	require.Equal(t, text, pickPaste(md, text), "the plain flavour goes in untouched")
}

func TestPickPaste_PlainFlavourIsNeverTrimmed(t *testing.T) {
	require.Equal(t, "  好的 \n", pickPaste("好的", "  好的 \n"))
}
