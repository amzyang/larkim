package ai

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTranscriptAndPrompt(t *testing.T) {
	tr := Transcript("FDEV", []store.Message{
		{SenderID: "ou_me", SenderName: "邹洋", Content: "上线了", CreateMs: 0},
		{SenderID: "ou_x", SenderName: "张三", ContentRaw: `{"text":"raw"}`, CreateMs: 60000},
		{SenderID: "ou_y", SenderName: "gone", Content: "x", Deleted: true},
	}, "ou_me")
	require.Contains(t, tr, "chat: FDEV")
	require.Contains(t, tr, "邹洋 (me): 上线了")
	require.Contains(t, tr, `张三: {"text":"raw"}`, "unrendered messages fall back to the raw body")
	require.NotContains(t, tr, "gone")

	p, draft := Prompt("summary")
	require.False(t, draft)
	require.Contains(t, p, "Summarize")
	p, draft = Prompt("draft 委婉拒绝")
	require.True(t, draft)
	require.Contains(t, p, "委婉拒绝")
	p, draft = Prompt("谁提到了发布时间？")
	require.False(t, draft)
	require.Equal(t, "谁提到了发布时间？", p)
}
