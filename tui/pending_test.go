package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestPendingText_PrefersTheBodyItCanRead(t *testing.T) {
	for _, tc := range []struct {
		name, msgType, raw, want string
	}{
		{"text", "text", `{"text":"hi\nthere"}`, "hi\nthere"},
		{"text keeps mention placeholders", "text", `{"text":"@_user_1 hi"}`, "@_user_1 hi"},
		{"card", "interactive", `{"json_card":"{}"}`, "[卡片]"},
		{"image", "image", `{"image_key":"img_v3_1"}`, "[图片]"},
		{"malformed text falls back to the type", "text", "not json", "[text]"},
		{"empty text falls back to the type", "text", `{"text":""}`, "[text]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pendingText(tc.msgType, tc.raw))
		})
	}
}

func TestPendingSummary_IsOneLine(t *testing.T) {
	c := store.Chat{LastMsgType: "text", LastContentRaw: `{"text":"line1\nline2"}`}
	require.Equal(t, "line1 line2", pendingSummary(c))
}

func TestPendingText_FlattensARichTextBody(t *testing.T) {
	require.Equal(t, "abc\ndef", pendingText("text", `{"text":"<p>abc</p><p>def</p>"}`))
}

func TestPendingText_PostReadsItsBody(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"the title leads the paragraphs",
			`{"title":"发布说明","content":[[{"tag":"text","text":"今天上线"}],[{"tag":"text","text":"明天回滚"}]]}`,
			"发布说明\n今天上线\n明天回滚"},
		{"a link reads as its words",
			`{"title":"","content":[[{"tag":"text","text":"见 "},{"tag":"a","text":"看板","href":"https://example.com/b"}]]}`,
			"见 看板"},
		{"an at reads as the name",
			`{"title":"","content":[[{"tag":"at","user_id":"ou_a","user_name":"张三"},{"tag":"text","text":" 看一下"}]]}`,
			"@张三 看一下"},
		{"an emotion keeps the spelling expandEmoji knows",
			`{"title":"","content":[[{"tag":"emotion","emoji_type":"OK"}]]}`,
			"[OK]"},
		{"a code block reads as its code",
			`{"title":"","content":[[{"tag":"code_block","language":"GO","text":"x := 1"}]]}`,
			"x := 1"},
		{"pictures are left to the rendering",
			`{"title":"","content":[[{"tag":"text","text":"图："},{"tag":"img","image_key":"img_a"}]]}`,
			"图："},
		{"a picture-only post is named by its picture",
			`{"title":"","content":[[{"tag":"img","image_key":"img_a"}]]}`,
			"[图片]"},
		{"a locale-wrapped body reads the same",
			`{"zh_cn":{"title":"","content":[[{"tag":"md","text":"## 标题"}]]}}`,
			"## 标题"},
		{"a body that is not a post falls back to the type",
			`This message was sent from an unsupported client`,
			"[富文本]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pendingText("post", tc.raw))
		})
	}
}
