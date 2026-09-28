package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestAttachText_SpellsEachKindTheWayItsReadersMatchIt(t *testing.T) {
	// gistBody, splitImages and the resource scan all read a rendering back by
	// matching these shapes, so they are lark-cli's to the character.
	for _, tc := range []struct{ name, msgType, raw, want string }{
		{"image", "image", `{"image_key":"img_a"}`, "[Image: img_a]"},
		{"image with no key", "image", `{}`, "[Image]"},
		{"file", "file", `{"file_key":"file_a","file_name":"报告.pdf"}`, `<file key="file_a" name="报告.pdf"/>`},
		{"file with no name", "file", `{"file_key":"file_a"}`, `<file key="file_a" name="file_a"/>`},
		{"file with no key", "file", `{}`, "[File]"},
		{"audio", "audio", `{"file_key":"file_a","duration":21000}`, `<audio key="file_a" duration="21s"/>`},
		{"audio with no duration", "audio", `{"file_key":"file_a"}`, `<audio key="file_a"/>`},
		{"audio with no key", "audio", `{"duration":21000}`, "[Voice: 21s]"},
		{"video", "media", `{"file_key":"file_a","file_name":"a.mp4","image_key":"img_c","duration":6961}`,
			`<video key="file_a" name="a.mp4" duration="7s" cover_image_key="img_c"/>`},
		{"video with no cover", "video", `{"file_key":"file_a","file_name":"a.mp4"}`,
			`<video key="file_a" name="a.mp4"/>`},
		{"video with no key", "media", `{}`, "[Video]"},
		{"a name carrying a quote", "file", `{"file_key":"file_a","file_name":"say \"hi\".txt"}`,
			`<file key="file_a" name="say \"hi\".txt"/>`},
		{"a body that will not parse", "image", `not json`, "[Invalid image JSON]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, attachText(tc.msgType, tc.raw))
		})
	}
}

func TestSeconds_RoundsToTheNearestSecond(t *testing.T) {
	require.Equal(t, "7s", seconds(6961))
	require.Equal(t, "21s", seconds(21000))
}

func TestTick_AnAttachmentIsRenderedWithoutACall(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	img := msg("om_img", "oc_a", clk.t.Add(-time.Minute), "")
	img.MsgType, img.Body.Content = "image", `{"image_key":"img_a"}`
	f.AddMessage(img)

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	got, err := s.Store.GetMessage(ctx, "om_img")
	require.NoError(t, err)
	require.Equal(t, "[Image: img_a]", got.Content)
	require.NotZero(t, got.RenderedAt)
	require.Zero(t, callsTo(f, "render"))
}
