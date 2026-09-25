package ai

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTranscriptAndPrompt(t *testing.T) {
	tr := Transcript("项目协作群", []store.Message{
		{SenderID: "ou_me", SenderName: "林岚", Content: "上线了", CreateMs: 0},
		{SenderID: "ou_x", SenderName: "张三", ContentRaw: `{"text":"raw"}`, CreateMs: 60000},
		{SenderID: "ou_y", SenderName: "gone", Content: "x", Deleted: true},
	}, "ou_me")
	require.Contains(t, tr, "chat: 项目协作群")
	require.Contains(t, tr, "林岚 (me): 上线了")
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

// cardRaw is the raw content of a card whose body is a heading over a list —
// the shape whose blocks a rendered text runs together.
const cardRaw = `{"json_card":"{\"schema\":\"2.0\",\"header\":{\"tag\":\"card_header\",\"property\":{\"title\":{\"tag\":\"plain_text\",\"property\":{\"content\":\"\u53d1\u5e03\u62a5\u544a\"}}}},\"body\":{\"tag\":\"body\",\"property\":{\"elements\":[{\"tag\":\"markdown\",\"property\":{\"elements\":[{\"tag\":\"heading\",\"property\":{\"level\":1,\"elements\":[{\"tag\":\"plain_text\",\"property\":{\"content\":\"\u62a5\u8868\"}}]}},{\"tag\":\"list\",\"property\":{\"items\":[{\"type\":\"ul\",\"level\":0,\"elements\":[{\"tag\":\"plain_text\",\"property\":{\"content\":\"\u7532\"}}]}]}}]}}]}}}","json_attachment":{},"card_schema":2}`

func TestTranscript_ACardReadsAsItsOwnDocument(t *testing.T) {
	tr := Transcript("平台组", []store.Message{{SenderID: "ou_x", SenderName: "构建机器人", RenderedAt: 5,
		ContentRaw: cardRaw, Content: "<card title=\"发布报告\">\n# 报表- 甲\n</card>"}}, "ou_me")
	require.Contains(t, tr, "发布报告\n\n# 报表\n\n- 甲")
	require.NotContains(t, tr, "<card")
}
