package larkcli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFake_SendEscapesTheContentJSON(t *testing.T) {
	f := NewFake()
	sent, err := f.Send(context.Background(), Target{ChatID: "oc_quiet"}, Text("他说\"好\"\n然后走了"), "cli_c")
	require.NoError(t, err)

	var body struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal([]byte(f.Messages[sent.MessageID].Body.Content), &body))
	require.Equal(t, "他说\"好\"\n然后走了", body.Text)
}

func TestFake_SendMarkdownStoresAPostBody(t *testing.T) {
	f := NewFake()
	sent, err := f.Send(context.Background(), Target{ChatID: "oc_quiet"}, Markdown("## 发布说明\n\n- 修复了 A"), "cli_c")
	require.NoError(t, err)

	m := f.Messages[sent.MessageID]
	require.Equal(t, "post", m.MsgType)
	require.Equal(t,
		`{"zh_cn":{"content":[[{"tag":"md","text":"## 发布说明\n\n- 修复了 A"}]]}}`,
		m.Body.Content)
	// The md tag passes through unrendered, so ingest brings the draft back.
	require.Equal(t, "## 发布说明\n\n- 修复了 A", f.Rendered[sent.MessageID].Content)
}

func TestFake_SendImageStoresAnImageBody(t *testing.T) {
	f := NewFake()
	sent, err := f.Send(context.Background(), Target{UserID: "ou_a"}, Image("img_shot"), "cli_c")
	require.NoError(t, err)

	m := f.Messages[sent.MessageID]
	require.Equal(t, "image", m.MsgType)
	require.Equal(t, `{"image_key":"img_shot"}`, m.Body.Content)
	require.Equal(t, "[Image: img_shot]", f.Rendered[sent.MessageID].Content)
	require.Equal(t, "oc_p2p_ou_a", sent.ChatID)
}

func TestFake_SendRecordsEveryBodyBesideItsKey(t *testing.T) {
	f := NewFake()
	_, err := f.Send(context.Background(), Target{ChatID: "oc_quiet"}, Text("好的"), "cli_c")
	require.NoError(t, err)
	_, err = f.Send(context.Background(), Target{ChatID: "oc_quiet"}, Markdown("## hi"), "cli_d")
	require.NoError(t, err)

	require.Equal(t, []Outgoing{Text("好的"), Markdown("## hi")}, f.Sent)
	require.Equal(t, []string{"cli_c", "cli_d"}, f.SentKeys)
}

func TestFake_ReplyInThreadHangsOffItsParent(t *testing.T) {
	f := NewFake()
	f.AddMessage(RawMessage{MessageID: "om_elsewhere", ChatID: "oc_quiet", MsgType: "text"})

	sent, err := f.Reply(context.Background(), "om_elsewhere", Markdown("- a\n- b"), true, "cli_c")
	require.NoError(t, err)

	m := f.Messages[sent.MessageID]
	require.Equal(t, "post", m.MsgType)
	require.Equal(t, "om_elsewhere", m.ParentID)
	require.Equal(t, "omt_om_elsewhere", m.ThreadID)
	require.Equal(t, int64(-1), int64(m.MessagePosition))
}

func TestFake_UploadImageRecordsThePath(t *testing.T) {
	f := NewFake()
	key, err := f.UploadImage(context.Background(), "/Users/linlan/Desktop/shot.png")
	require.NoError(t, err)
	require.Equal(t, "img_fake_1", key)

	key, err = f.UploadImage(context.Background(), "/Users/linlan/Desktop/other.png")
	require.NoError(t, err)
	require.Equal(t, "img_fake_2", key)
	require.Equal(t, []string{"/Users/linlan/Desktop/shot.png", "/Users/linlan/Desktop/other.png"}, f.Uploads)
}

func TestFake_UploadImageHonoursInjectedErrors(t *testing.T) {
	f := NewFake()
	f.Err = &Error{ExitCode: ExitAPI, Type: "api", Message: "nope"}
	_, err := f.UploadImage(context.Background(), "/Users/linlan/Desktop/shot.png")
	require.ErrorContains(t, err, "nope")
}
