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
