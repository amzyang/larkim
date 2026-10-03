package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestPostText_SpellsEachElementTheWayItsReadersMatchIt(t *testing.T) {
	t.Parallel()
	// The @ runs, the picture references and the attachment tags are all read
	// back out of a rendering by matching these shapes.
	for _, tc := range []struct{ name, raw, want string }{
		{"a title leads the paragraphs",
			`{"title":"发布说明","content":[[{"tag":"text","text":"今天上线"}]]}`, "发布说明\n今天上线"},
		{"emphasis nests innermost first",
			`{"content":[[{"tag":"text","text":"重要","style":["bold","underline"]}]]}`, "<u>**重要**</u>"},
		{"a link keeps its target",
			`{"content":[[{"tag":"a","text":"看板","href":"https://example.com/b"}]]}`, "[看板](https://example.com/b)"},
		{"a bracket in a label is escaped",
			`{"content":[[{"tag":"a","text":"[1]","href":"https://example.com/b"}]]}`, `[\[1\]](https://example.com/b)`},
		{"a mention of somebody keeps their open id",
			`{"content":[[{"tag":"at","user_id":"ou_a","user_name":"张三"}]]}`, `<at user_id="ou_a">张三</at>`},
		{"a mention of everybody names nobody",
			`{"content":[[{"tag":"at","user_id":"all"}]]}`, `<at user_id="all"></at>`},
		{"a picture is named by its key",
			`{"content":[[{"tag":"img","image_key":"img_a"}]]}`, "![Image](img_a)"},
		{"a code block keeps its language",
			`{"content":[[{"tag":"code_block","language":"go","text":"x := 1"}]]}`, "```go\nx := 1\n```"},
		{"an emotion is a shortcode",
			`{"content":[[{"tag":"emotion","emoji_type":"OK"}]]}`, ":OK:"},
		{"content_v2 wins over content",
			`{"content":[[{"tag":"text","text":"旧"}]],"content_v2":[[{"tag":"text","text":"新"}]]}`, "新"},
		{"a locale-wrapped body reads the same",
			`{"zh_cn":{"content":[[{"tag":"md","text":"## 标题"}]]}}`, "## 标题"},
		{"an attachment zone is written under the words",
			`{"content":[[{"tag":"text","text":"见附件"}]],"files":[{"file_key":"file_a","file_name":"报告.pdf"}]}`,
			"见附件\n<file key=\"file_a\" name=\"报告.pdf\"/>"},
		{"a body with nothing in it is named by its kind",
			`{"content":[]}`, "[Rich text message]"},
		{"a body that will not parse", `not json`, "[Invalid rich text JSON]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, postText(tc.raw, ""))
		})
	}
}

func TestTick_APostAndACardAreRenderedWithoutACall(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	p := msg("om_post", "oc_a", clk.t.Add(-time.Minute), "")
	p.MsgType, p.Body.Content = "post", `{"content":[[{"tag":"text","text":"今天上线"}]]}`
	f.AddMessage(p)
	c := msg("om_card", "oc_a", clk.t.Add(-2*time.Minute), "")
	c.MsgType, c.Body.Content = "interactive", cardAttachment
	f.AddMessage(c)

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	got, err := s.Store.MessagesByIDs(ctx, []string{"om_post", "om_card"})
	require.NoError(t, err)
	require.Equal(t, "今天上线", got["om_post"].Content)
	require.NotZero(t, got["om_card"].RenderedAt, "a card larkim can read is rendered from its own JSON")
	require.Zero(t, callsTo(f, "render"))
}

func TestCardText_ACardWithNothingInItIsNamedByItsKind(t *testing.T) {
	t.Parallel()
	require.Equal(t, "[Card]", cardText(`{"json_card":"{}"}`))
	require.Equal(t, "[Card]", cardText(`not json`))
}
