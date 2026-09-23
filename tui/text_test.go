package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestRenderInline_KeepsLabelsAndDropsTargets(t *testing.T) {
	ms := mentionsIn("", "")
	require.Equal(t, "见 开放日详情 和 重点", ansi.Strip(renderInline("见 [开放日详情](https://example.com/x) 和 **重点**", ms)))
	require.Equal(t, "下划线", ansi.Strip(renderInline("<u>下划线</u>", ms)))
	require.Equal(t, "https://example.com/x", ansi.Strip(renderInline("[](https://example.com/x)", ms)),
		"a link with no label falls back to its target")
	require.Equal(t, "日志 [ObjectMapperUtils:82] 原样", ansi.Strip(renderInline("日志 [ObjectMapperUtils:82] 原样", ms)),
		"brackets that are not a link are text")
}
