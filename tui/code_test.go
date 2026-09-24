package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// postWith is a rendered rich-text message, which is how a code block reaches
// the list: lark-cli writes the fence lark's code_block element became.
func postWith(content string) []store.Message {
	return []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a",
		MsgType: "post", Content: content, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
}

func rawText(rows []msgRow) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(segText(r))
		b.WriteString("\n")
	}
	return b.String()
}

func TestBodyRows_HighlightsAFencedCodeBlock(t *testing.T) {
	rows := renderRows(postWith("```JSON\n{\"a\": 1}\n```"), baseStyle())
	out := rowText(rows)
	require.NotContains(t, out, "```", "the fence is chrome, not content")
	require.Contains(t, out, `{"a": 1}`)
	require.NotEqual(t, out, rawText(rows), "the code carries syntax colour")
}

func TestBodyRows_NumbersTheLinesOfACodeBlock(t *testing.T) {
	out := rowText(renderRows(postWith("```JSON\n{\n}\n```"), baseStyle()))
	require.Contains(t, out, codeRule+" 1 {")
	require.Contains(t, out, codeRule+" 2 }")
}

func TestBodyRows_LeavesAnUnknownLanguageUncoloured(t *testing.T) {
	rows := renderRows(postWith("```PLAIN_TEXT\nid,name\n1,张三\n```"), baseStyle())
	out := rowText(rows)
	require.NotContains(t, out, "```")
	require.Contains(t, out, codeRule+" 1 id,name")
	require.Contains(t, out, codeRule+" 2 1,张三")
	// The gutter is dim, the code itself carries nothing: Feishu's PLAIN_TEXT
	// names no language, and a guessed colour would be worse than none.
	require.Contains(t, rawText(rows), "id,name\n", "no escape is written around plain code")
}

func TestBodyRows_TruncatesALongCodeLineRatherThanWrappingIt(t *testing.T) {
	long := "SELECT " + strings.Repeat("column_name, ", 60) + "1"
	st := baseStyle()
	rows := renderRows(postWith("```SQL\n"+long+"\n```"), st)
	code := 0
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r.text), codeRule) {
			code++
			require.LessOrEqual(t, lipglossWidth(r.text), st.width, "a code row never runs past the pane")
		}
	}
	require.Equal(t, 1, code, "one row per code line, however long the line is")
	require.Contains(t, rowText(rows), "…", "the tail is cut, not wrapped")
}

func TestBodyRows_LeavesACardCodeFenceAlone(t *testing.T) {
	// lark-cli glues a card's code block into the middle of a line, with no
	// newline either side; a fence that is not a line of its own is not one.
	out := rowText(renderRows(postWith("slow sql 647 millis SELECT```plain_text\n  m.id```"), baseStyle()))
	require.Contains(t, out, "```plain_text")
}

func TestBodyRows_ClosesAnUnterminatedFenceAtTheEnd(t *testing.T) {
	out := rowText(renderRows(postWith("```BASH\nls -la\n"), baseStyle()))
	require.NotContains(t, out, "```")
	require.Contains(t, out, codeRule+" 1 ls -la")
}

func TestBodyRows_LeavesEmojiSpellingsInsideCodeAlone(t *testing.T) {
	out := rowText(renderRows(postWith("```JSON\n{\"a\": \"[完成]\"}\n```"), baseStyle()))
	require.Contains(t, out, "[完成]", "code is code, not a message body")
}

func TestBodyRows_DropsTheBlankLineLarkLeavesBeforeTheClosingFence(t *testing.T) {
	out := rowText(renderRows(postWith("```JSON\n{\n}\n\n```"), baseStyle()))
	require.NotContains(t, out, codeRule+" 3 ")
}

func TestBodyRows_KeepsTextAroundACodeBlock(t *testing.T) {
	out := rowText(renderRows(postWith("看下这个\n```JSON\n{}\n```\n谢谢[THANKS]"), baseStyle()))
	require.Contains(t, out, "看下这个")
	require.Contains(t, out, "谢谢🙏", "the body around the block still renders as a body")
	require.Contains(t, out, codeRule+" 1 {}")
}

// lipglossWidth is the display width of a styled row, which is what the pane
// hands the terminal.
func lipglossWidth(s string) int { return ansi.StringWidth(s) }
