package sync

import (
	"context"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestRenderedText_UnwrapsAnHTMLTextBody(t *testing.T) {
	cases := []struct {
		name string
		in   larkcli.RenderedMessage
		want string
	}{
		{"one paragraph", larkcli.RenderedMessage{MsgType: "text", Content: "<p>abc</p>"}, "abc"},
		{"a paragraph per line", larkcli.RenderedMessage{MsgType: "text", Content: "<p>abc</p><p>def</p>"}, "abc\ndef"},
		{"a blank line survives", larkcli.RenderedMessage{MsgType: "text", Content: "<p>abc</p><p></p><p>def</p>"}, "abc\n\ndef"},
		{"an ampersand stays itself", larkcli.RenderedMessage{MsgType: "text", Content: "<p>x?a=1&current=2</p>"}, "x?a=1&current=2"},
		{"text outside a paragraph is literal", larkcli.RenderedMessage{MsgType: "text", Content: "see <p>abc</p>"}, "see <p>abc</p>"},
		{"a forwarded bundle unwraps each message it carries",
			larkcli.RenderedMessage{MsgType: "merge_forward", Content: "<forwarded_messages>\n[t] 张三:\n    <p>abc</p><p>def</p>\n[t] 李四:\n    plain"},
			"<forwarded_messages>\n[t] 张三:\n    abc\n    def\n[t] 李四:\n    plain"},
		{"a post keeps its rendering", larkcli.RenderedMessage{MsgType: "post", Content: "<p>abc</p>"}, "<p>abc</p>"},
		{"a plain body with no markup", larkcli.RenderedMessage{MsgType: "text", Content: "abc"}, "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, renderedText(c.in))
		})
	}
}

func TestTick_StoresAnEditedBodyAsPlainLines(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "group"}}
	f.AddMessage(msg("om_edit", "oc_a", now.Add(-time.Minute), "abc"))
	_, err := s.Tick(ctx)
	require.NoError(t, err)

	// The edit: Feishu hands the body back as one paragraph per line.
	clk.t = now.Add(time.Minute)
	edited := msg("om_edit", "oc_a", now.Add(-time.Minute), "<p>abc</p><p>def</p>")
	edited.UpdateTime = msAt(clk.t)
	edited.Updated = true
	f.AddMessage(edited)
	f.Rendered = map[string]larkcli.RenderedMessage{"om_edit": {MessageID: "om_edit", ChatID: "oc_a",
		MsgType: "text", Content: "<p>abc</p><p>def</p>"}}
	require.NoError(t, s.IngestIDs(ctx, []string{"om_edit"}))

	m, err := s.Store.GetMessage(ctx, "om_edit")
	require.NoError(t, err)
	require.Equal(t, "abc\ndef", m.Content)
	require.NotZero(t, m.EditedAt, "unwrapping the body does not cost the edited marker")
}
