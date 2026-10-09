package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
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
	t.Parallel()
	rows := renderRows(postWith("```JSON\n{\"a\": 1}\n```"), baseStyle())
	out := rowText(rows)
	require.NotContains(t, out, "```", "the fence is chrome, not content")
	require.Contains(t, out, `{"a": 1}`)
	require.NotEqual(t, out, rawText(rows), "the code carries syntax colour")
}

func TestBodyRows_NumbersTheLinesOfACodeBlock(t *testing.T) {
	t.Parallel()
	out := rowText(renderRows(postWith("```JSON\n{\n}\n```"), baseStyle()))
	require.Contains(t, out, codeRule+" 1 {")
	require.Contains(t, out, codeRule+" 2 }")
}

func TestBodyRows_LeavesAnUnknownLanguageUncoloured(t *testing.T) {
	t.Parallel()
	rows := renderRows(postWith("```PLAIN_TEXT\nid,name\n1,张三\n```"), baseStyle())
	out := rowText(rows)
	require.NotContains(t, out, "```")
	require.Contains(t, out, codeRule+" 1 id,name")
	require.Contains(t, out, codeRule+" 2 1,张三")
	// The gutter is dim, the code itself carries nothing: Feishu's PLAIN_TEXT
	// names no language, and a guessed colour would be worse than none.
	require.Regexp(t, "\x1b\\[m1,张三 *\n", rawText(rows), "no escape is written around plain code")
}

// codeLines are the drawn code rows of a body, gutter and all, as written.
func codeLines(rows []msgRow) []string {
	var out []string
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r.text), codeRule) {
			out = append(out, r.text)
		}
	}
	return out
}

// copyZone is the one copy icon among rows and the row it hangs on.
func copyZone(t *testing.T, rows []msgRow) (z clickZone, at int) {
	t.Helper()
	at = -1
	for i, r := range rows {
		for _, c := range r.zones {
			if c.copy != "" {
				require.Equal(t, -1, at, "one icon per block")
				z, at = c, i
			}
		}
	}
	require.NotEqual(t, -1, at, "no copy icon in the body")
	return z, at
}

// iconUnder is what a stripped row draws under a zone.
func iconUnder(r msgRow, z clickZone) string {
	return trimCols(rowPlain(r), z.x0-r.lead.cols(), z.x1-r.lead.cols())
}

func TestBodyRows_WrapsALongCodeLineUnderItsNumber(t *testing.T) {
	t.Parallel()
	long := "SELECT " + strings.Repeat("column_name, ", 20) + "1"
	st := baseStyle()
	rows := renderRows(postWith("```SQL\n"+long+"\nFROM t\n```"), st)
	lines := codeLines(rows)
	require.Greater(t, len(lines), 2, "the long line wraps instead of being cut")
	require.NotContains(t, rowText(rows), "…", "nothing is cut off the tail")
	var first []string
	for i, l := range lines {
		require.LessOrEqual(t, lipglossWidth(l), st.inner(), "a code row never runs past the pane")
		l = ansi.Strip(l)
		switch {
		case i == len(lines)-1:
			require.Contains(t, l, codeRule+" 2 FROM t", "the next source line keeps its own number")
			continue
		case i == 0:
			require.Contains(t, l, codeRule+" 1 SELECT")
		default:
			require.Contains(t, l, codeRule+"   ", "a continuation row numbers nothing: %q", l)
		}
		// The gutter is the rule, a blank, the one digit and a blank.
		body := strings.ReplaceAll(ansi.TruncateLeft(l, 4, ""), codeCopyGlyph, "")
		first = append(first, strings.TrimRight(body, " "))
	}
	require.Equal(t, long, strings.Join(first, " "), "the rows put back together are the line")
}

func TestBodyRows_AWrappedCodeLineKeepsItsColour(t *testing.T) {
	t.Parallel()
	long := "x := \"" + strings.Repeat("abcdef ", 20) + "\""
	lines := codeLines(renderRows(postWith("```GO\n"+long+"\n```"), baseStyle()))
	require.Greater(t, len(lines), 1)
	for _, line := range lines[1:] {
		_, code, _ := strings.Cut(line, codeRule)
		require.Contains(t, code, "\x1b[", "the string's colour carries onto the row it wraps to: %q", line)
	}
}

func TestBodyRows_ACodeBlockCopiesFromItsTopRightIcon(t *testing.T) {
	t.Parallel()
	st := baseStyle()
	raw := `{"content":[[{"tag":"code_block","language":"go","text":"x := 1\ny := 2\n"}]]}`
	rows := renderRows(postRaw(raw), st)
	z, at := copyZone(t, rows)
	require.Equal(t, "x := 1\ny := 2", z.copy, "the icon copies the source, not the drawn rows")
	require.Contains(t, rowPlain(rows[at]), codeRule+" 1 ", "the icon sits on the block's first row")
	require.Equal(t, leadWidth+st.inner(), z.x1, "the icon is flush with the pane's right edge")
	require.Equal(t, strings.TrimSpace(codeCopyGlyph), iconUnder(rows[at], z), "the target covers the icon")
}

func TestMdRows_ACodeBlockInAListCopiesFromUnderItsIcon(t *testing.T) {
	t.Parallel()
	rows := renderRows(postWith("- 看这个\n\n  ```JSON\n  {}\n  ```"), baseStyle())
	z, at := copyZone(t, rows)
	require.Equal(t, "{}", z.copy)
	require.Equal(t, strings.TrimSpace(codeCopyGlyph), iconUnder(rows[at], z),
		"the target follows the block's indent: %q", rowPlain(rows[at]))
}

func TestOnClick_TheCodeIconPutsTheBlockOnTheClipboard(t *testing.T) {
	t.Parallel()
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = 100, 30
	m.chatID = "oc_a"
	m.chats = []store.Chat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	m.msgsBase = []store.Message{{MessageID: "om_1", ChatID: "oc_a", SenderID: "ou_a", SenderName: "张三",
		MsgType: "post", ContentRaw: `{"content":[[{"tag":"code_block","language":"go","text":"x := 1\n"}]]}`,
		CreateMs: msgAt(23, 9, 0)}}
	m.applyOutbox()
	m.layout()
	m.focus = paneMessages
	m.rebuildMessages()
	z, at := copyZone(t, m.msgRows)
	next, cmd := clickPane(m, z.x0, at)
	require.NotNil(t, cmd)
	require.Equal(t, tea.SetClipboard("x := 1")(), cmd())
	require.Equal(t, "code copied", next.(Model).notice)
}

func TestNarrowCodeBlock_DropsTheIconWithTheGutter(t *testing.T) {
	t.Parallel()
	lines, zones := codeRows("x := 1", "go", 8, false)
	require.Empty(t, zones, "a block too narrow for its gutter has no room for the icon either")
	require.NotContains(t, strings.Join(lines, ""), codeCopyGlyph)
}

func TestBodyRows_MidLineBackticksAreAnInlineSpanNotABlock(t *testing.T) {
	t.Parallel()
	// Backticks that do not open a line are an inline code span, which is
	// what CommonMark and Feishu both make of them. The text has to survive
	// either way: a body is never quietly shortened.
	out := rowText(renderRows(postWith("slow sql 647 millis SELECT```plain_text\n  m.id```"), baseStyle()))
	require.Contains(t, out, "slow sql 647 millis SELECT")
	require.Contains(t, out, "m.id")
	require.NotContains(t, out, codeRule, "an inline span is not framed as a block")
}

func TestBodyRows_ClosesAnUnterminatedFenceAtTheEnd(t *testing.T) {
	t.Parallel()
	out := rowText(renderRows(postWith("```BASH\nls -la\n"), baseStyle()))
	require.NotContains(t, out, "```")
	require.Contains(t, out, codeRule+" 1 ls -la")
}

func TestBodyRows_LeavesEmojiSpellingsInsideCodeAlone(t *testing.T) {
	t.Parallel()
	out := rowText(renderRows(postWith("```JSON\n{\"a\": \"[完成]\"}\n```"), baseStyle()))
	require.Contains(t, out, "[完成]", "code is code, not a message body")
}

func TestBodyRows_DropsTheBlankLineLarkLeavesBeforeTheClosingFence(t *testing.T) {
	t.Parallel()
	out := rowText(renderRows(postWith("```JSON\n{\n}\n\n```"), baseStyle()))
	require.NotContains(t, out, codeRule+" 3 ")
}

func TestBodyRows_KeepsTextAroundACodeBlock(t *testing.T) {
	out := rowText(renderRows(postWith("看下这个\n```JSON\n{}\n```\n谢谢:THANKS:"), baseStyle()))
	require.Contains(t, out, "看下这个")
	require.Contains(t, out, "谢谢:THANKS:", "the body around the block still renders as a body")
	require.Contains(t, out, codeRule+" 1 {}")
}

// countingLexers puts a recording lookup behind the memo for one test and
// reports, in order, the languages that reached chroma's registry. It swaps
// the package-level lexerFor, so its callers must not call t.Parallel.
func countingLexers(t *testing.T) func() []string {
	t.Helper()
	var asked []string
	prev := lexerFor
	t.Cleanup(func() { lexerFor = prev })
	lexerFor = memoLexer(func(lang string) chroma.Lexer {
		asked = append(asked, lang)
		return lexers.Get(lang)
	})
	return func() []string { return asked }
}

func TestHighlightCode_NeverAsksTheRegistryForAnUnlabelledBlock(t *testing.T) {
	asked := countingLexers(t)
	for range 3 {
		highlightCode("id,name\n1,张三", "", false)
	}
	require.Empty(t, asked(), "a fence naming no language has nothing to look up")
	// A named language does reach the registry through the same seam, so the
	// assertion above is one this test can fail.
	highlightCode("{}", "json", false)
	require.Equal(t, []string{"json"}, asked())
}

func TestHighlightCode_ResolvesEachLanguageOnce(t *testing.T) {
	asked := countingLexers(t)
	for range 3 {
		highlightCode("{}", "json", false)
		highlightCode("id,name", "plain_text", false)
	}
	require.Equal(t, []string{"json", "plain_text"}, asked(),
		"a language chroma refuses is refused once, not once per code block")
}

// lipglossWidth is the display width of a styled row, which is what the pane
// hands the terminal.
func lipglossWidth(s string) int { return ansi.StringWidth(s) }
