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

func TestRenderInline_StylesTheRunsAPostComesBackAs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"看 **粗体** 吧", "看 粗体 吧"},
		{"看 *斜体* 吧", "看 斜体 吧"},
		{"看 ~~删除~~ 吧", "看 删除 吧"},
		{"跑 `go test` 吧", "跑 go test 吧"},
		{"看 <u>下划线</u> 吧", "看 下划线 吧"},
	} {
		require.Equal(t, tc.want, ansi.Strip(renderInline(tc.in, mentionsIn("", ""))), "in %q", tc.in)
	}
}

func TestRenderInline_LeavesOrdinaryProseAlone(t *testing.T) {
	// A lone asterisk between spaces is arithmetic, not emphasis, and an
	// underscore is part of an identifier.
	for _, in := range []string{"3 * 4 * 5", "路径是 user_id_map", "评分 4*"} {
		require.Equal(t, in, ansi.Strip(renderInline(in, mentionsIn("", ""))), "in %q", in)
	}
}

func TestRenderInline_BoldWinsOverItalic(t *testing.T) {
	require.Equal(t, "粗体", ansi.Strip(renderInline("**粗体**", mentionsIn("", ""))),
		"a doubled asterisk is bold, not an italic wrapping an asterisk")
}
