package cli

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// cardRaw is the raw content of a card whose body is a heading over a list —
// the shape whose blocks a rendered text runs together.
const cardRaw = `{"json_card":"{\"schema\":\"2.0\",\"header\":{\"tag\":\"card_header\",\"property\":{\"title\":{\"tag\":\"plain_text\",\"property\":{\"content\":\"\u53d1\u5e03\u62a5\u544a\"}}}},\"body\":{\"tag\":\"body\",\"property\":{\"elements\":[{\"tag\":\"markdown\",\"property\":{\"elements\":[{\"tag\":\"heading\",\"property\":{\"level\":1,\"elements\":[{\"tag\":\"plain_text\",\"property\":{\"content\":\"\u62a5\u8868\"}}]}},{\"tag\":\"list\",\"property\":{\"items\":[{\"type\":\"ul\",\"level\":0,\"elements\":[{\"tag\":\"plain_text\",\"property\":{\"content\":\"\u7532\"}}]}]}}]}}]}}}","json_attachment":{},"card_schema":2}`

func TestContentLabel_ACardReadsAsItsOwnDocument(t *testing.T) {
	m := store.Message{MsgType: "interactive", RenderedAt: 5, ContentRaw: cardRaw,
		Content: "<card title=\"发布报告\">\n# 报表- 甲\n</card>"}
	require.Equal(t, "发布报告\n\n# 报表\n\n- 甲", contentLabel(m))
}

func TestContentLabel_AnUnrenderedBodyIsStillTheStoredOne(t *testing.T) {
	require.Equal(t, `{"text":"hi"}`, contentLabel(store.Message{MsgType: "text", ContentRaw: `{"text":"hi"}`}))
	require.Equal(t, "hi", contentLabel(store.Message{MsgType: "text", ContentRaw: `{"text":"hi"}`, Content: "hi", RenderedAt: 5}))
}
