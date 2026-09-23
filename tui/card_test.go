package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const weeklyCard = `<card title="设备版本周报 · 最新 1211">
「兜底」
**待认领账号**：13 个，待升级设备 13 台
当前最低可用版本 1211，低于它的设备将被诊断为不可用
---
通知编号 NTE_36284412765929600
[知道了] [查看清单(内网)](https://test-factory.beiwanglu.cc/accounts?deviceHealth=outdated)
</card>`

func cardLines(src string, width int) string {
	c, ok := parseCard(src)
	if !ok {
		return ""
	}
	var lines []string
	for _, r := range renderCard(c, width, mentionsIn("", "")) {
		lines = append(lines, r.text)
	}
	return ansi.Strip(strings.Join(lines, "\n"))
}

func TestParseCard_ReadsTheHeaderApartFromTheBody(t *testing.T) {
	c, ok := parseCard(weeklyCard)
	require.True(t, ok)
	require.Equal(t, "设备版本周报 · 最新 1211", c.title)
	require.Equal(t, "「兜底」", c.tags)
	require.Equal(t, "**待认领账号**：13 个，待升级设备 13 台", c.body[0])

	c, ok = parseCard(`<card title="A" subtitle="B">
x
</card>`)
	require.True(t, ok)
	require.Equal(t, "B", c.subtitle)

	_, ok = parseCard("plain text that mentions <card> somewhere")
	require.False(t, ok, "only the DSL parses as a card")
}

func TestRenderCard_FramesTheCardAndDropsTheMarkup(t *testing.T) {
	out := cardLines(weeklyCard, 50)
	for _, line := range strings.Split(out, "\n") {
		require.True(t, strings.HasPrefix(line, cardRule+" "), "every line carries the card's edge: %q", line)
	}
	require.Contains(t, out, "设备版本周报")
	require.Contains(t, out, "「兜底」", "the header tag rides with the title")
	require.Contains(t, out, "待认领账号：13 个", "bold markers are styling, not text")
	require.NotContains(t, out, "**")
	require.Contains(t, out, "[ 知道了 ] [ 查看清单(内网) ]", "actions read as buttons")
	require.NotContains(t, out, "https://", "a button shows its label, not its target")
	require.Contains(t, out, "─────", "the divider is drawn")
}

func TestCardUnescape_RestoresTheTitleAttribute(t *testing.T) {
	c, ok := parseCard(`<card title="say \"hi\"\nagain">
x
</card>`)
	require.True(t, ok)
	require.Equal(t, "say \"hi\"\nagain", c.title)
}

func TestRenderCard_EmptyBodySaysSo(t *testing.T) {
	require.Contains(t, cardLines("<card>\n</card>", 40), "(empty card)")
}
