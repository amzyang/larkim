package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func mdText(t *testing.T, body string) string {
	t.Helper()
	return rowText(renderRows(postWith(body), baseStyle()))
}

func TestMdRows_HeadingsDrawByLevelWithoutTheirHashes(t *testing.T) {
	out := mdText(t, "# 一级\n## 二级\n### 三级\n\n正文")
	require.Contains(t, out, "一级")
	require.Contains(t, out, "二级")
	require.Contains(t, out, "三级")
	require.NotContains(t, out, "#", "the hashes are markup, not something anyone typed")

	// Level is what the Feishu client throws away, so it is the one thing
	// this list has to keep: h1/h2 carry the accent, h3 and below do not.
	rows := renderRows(postWith("# 一级\n### 三级"), baseStyle())
	var h1, h3 string
	for _, r := range rows {
		switch {
		case strings.Contains(ansi.Strip(r.text), "一级"):
			h1 = segText(r)
		case strings.Contains(ansi.Strip(r.text), "三级"):
			h3 = segText(r)
		}
	}
	require.NotEmpty(t, h1)
	require.NotEmpty(t, h3)
	require.NotEqual(t, ansi.Strip(h1), h1, "a heading is styled")
	require.NotEqual(t, h1, h3, "levels do not look alike")
}

func TestMdRows_ListsCarryMarkersAndIndent(t *testing.T) {
	out := mdText(t, "- 甲\n    - 乙\n- 丙")
	require.Contains(t, out, "• 甲")
	require.Contains(t, out, "◦ 乙", "a nested item takes a different marker")
	require.Contains(t, out, "• 丙")
	require.NotContains(t, out, "- 甲", "the dash is markup")

	ordered := mdText(t, "1. 甲\n2. 乙")
	require.Contains(t, ordered, "1. 甲")
	require.Contains(t, ordered, "2. 乙")
}

func TestMdRows_OrderedListKeepsItsStart(t *testing.T) {
	out := mdText(t, "3. 三\n4. 四")
	require.Contains(t, out, "3. 三")
	require.Contains(t, out, "4. 四")
}

func TestMdRows_BlockquoteGetsAGutter(t *testing.T) {
	out := mdText(t, "> 引用一句\n\n正文")
	require.Contains(t, out, "│ 引用一句")
	require.NotContains(t, out, "> 引用", "the angle bracket is markup")
}

func TestMdRows_ThematicBreakSpansThePane(t *testing.T) {
	rows := renderRows(postWith("上面\n\n---\n\n下面"), baseStyle())
	var rule string
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r.text), "───") {
			rule = ansi.Strip(r.text)
		}
	}
	require.NotEmpty(t, rule, "a rule is drawn, not left as three hyphens")
	require.NotContains(t, rowText(rows), "---")
}

func TestMdRows_TableRendersAsAGrid(t *testing.T) {
	out := mdText(t, "| 项 | 值 |\n|---|---|\n| 甲 | 1 |\n| 乙 | 2 |")
	require.Contains(t, out, "项")
	require.Contains(t, out, "│", "cells are parted by a border, not by pipes in the text")
	require.NotContains(t, out, "|---|", "the separator row is markup")

	// Every cell keeps its own box: a row of the grid is a row of the table,
	// and a column of it a column.
	require.Regexp(t, `甲\s+│\s+1`, out)
	require.Regexp(t, `乙\s+│\s+2`, out)
	for _, line := range strings.Split(out, "\n") {
		require.NotContains(t, line, "甲1")
		if strings.Contains(line, "甲") {
			require.NotContains(t, line, "乙", "each row is drawn on a line of its own: %q", line)
		}
	}
}

func TestMdRows_TableStaysInsideThePane(t *testing.T) {
	st := baseStyle()
	rows := renderRows(postWith("| 一个很长的列名 | 另一个很长的列名 | 第三个很长的列名 |\n|---|---|---|\n| 值值值值值 | 值值值值值 | 值值值值值 |"), st)
	for _, r := range rows {
		require.LessOrEqual(t, lipglossWidth(r.text), st.width, "a table never runs past the pane")
	}
}

func TestMdRows_CodeFenceStillHighlights(t *testing.T) {
	out := mdText(t, "```go\nx := 1\n```")
	require.Contains(t, out, codeRule+" 1 x := 1", "the existing code path still draws the block")
	require.NotContains(t, out, "```")
}

func TestMdRows_KeepsInlineImagesAndMentions(t *testing.T) {
	// The inline path is what carries pictures and mention styling, so the
	// block renderer must hand paragraphs to it untouched.
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a", MsgType: "post",
		Content:      "看 ![](img_shot) 和 <at user_id=\"ou_b\">李四</at>",
		MentionsJSON: `[{"key":"@_user_1","id":"ou_b","name":"李四"}]`,
		CreateMs:     msgAt(23, 9, 0), RenderedAt: 1}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "@李四", "a mention still reads as a name")
	require.NotContains(t, out, "<at", "the tag is not shown raw")
	require.NotContains(t, out, "img_shot", "an image reference becomes a picture, not text")
}

func TestMdRows_InlineRunsReachRenderInline(t *testing.T) {
	out := mdText(t, "看 **粗** 和 *斜* 和 ~~删~~ 和 [链接](https://example.com)")
	require.Contains(t, out, "粗")
	require.Contains(t, out, "斜")
	require.Contains(t, out, "删")
	require.Contains(t, out, "链接")
	require.NotContains(t, out, "**")
	require.NotContains(t, out, "https://example.com", "a link keeps its label, not its target")
}

func TestMdRows_NoTextIsEverDropped(t *testing.T) {
	// The one failure mode that matters: a construct the walker does not know
	// must still show its words rather than silently swallow them.
	body := "# 标题\n\n段落 `行内` 文字\n\n- 甲\n    - 乙\n\n> 引用\n\n```go\nx := 1\n```\n\n| 项 | 值 |\n|---|---|\n| 丙 | 1 |\n\n---\n\n末尾"
	out := mdText(t, body)
	for _, word := range []string{"标题", "段落", "行内", "文字", "甲", "乙", "引用", "x := 1", "项", "值", "丙", "末尾"} {
		require.Contains(t, out, word, "%q went missing", word)
	}
}

func TestMdRows_WrapsCJKByDisplayWidth(t *testing.T) {
	st := baseStyle()
	rows := renderRows(postWith(strings.Repeat("中文", 80)), st)
	for _, r := range rows {
		require.LessOrEqual(t, lipglossWidth(r.text), st.width)
	}
}

func TestBodyRows_TextMessagesStayLiteral(t *testing.T) {
	// A person typing "3 * 4 * 5" in a plain message means the asterisks, and
	// a lone "- 甲" is how people answer, not a list.
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a", MsgType: "text",
		Content: "- 甲\n3 * 4 * 5", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "- 甲", "a text message is not markdown")
	require.Contains(t, out, "3 * 4 * 5")
	require.NotContains(t, out, "• 甲")
}
