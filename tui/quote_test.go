package tui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestLoadMeta_LoadsTheQuotedParents covers the case the page cannot: the
// message a reply answers is older than the page it arrives on, so it has to
// be fetched by id, together with its sender's account suffix.
func TestLoadMeta_LoadsTheQuotedParents(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_a", Name: "李明", Email: "liming01@example.com"},
	}, 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_old", ChatID: "oc_a", CreateMs: 10, MessagePosition: 1, SenderID: "ou_a", SenderName: "李明", RawJSON: "{}"},
		{MessageID: "om_new", ChatID: "oc_a", CreateMs: 20, MessagePosition: 2, SenderID: "ou_b", SenderName: "唐婉", ReplyTo: "om_old", RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_old", "瞅一眼", "", "", 2))
	require.NoError(t, st.UpdateRendered(ctx, "om_new", "好的", "", "", 2))

	page, err := st.ListMessages(ctx, store.MessageQuery{ChatID: "oc_a", SinceMs: 15})
	require.NoError(t, err)
	require.Len(t, page, 1, "the parent is off the page")

	meta, err := loadMeta(ctx, st, page)
	require.NoError(t, err)
	require.Equal(t, "瞅一眼", meta.parents["om_old"].Content)
	require.Equal(t, "01", meta.suffix["ou_a"], "the quoted sender is named as the lists name them")

	st2 := msgStyle{width: 60, self: "ou_me", now: testNow, suffix: meta.suffix, parents: meta.parents}
	require.Contains(t, rowText(renderRows(page, st2)), "▏李明01: 瞅一眼")
}

func TestRenderRows_QuoteReadsRecalledAndUnrenderedParents(t *testing.T) {
	gone := store.Message{MessageID: "om_gone", SenderName: "孙琪", Content: "原文", Deleted: true, RenderedAt: 1}
	raw := store.Message{MessageID: "om_raw", SenderName: "孙琪", MsgType: "image", ContentRaw: `{"image_key":"img_1"}`}
	msgs := []store.Message{
		{MessageID: "om_a", SenderName: "李四", Content: "无关", CreateMs: msgAt(23, 9, 0), RenderedAt: 1},
		{MessageID: "om_b", SenderName: "沈知远", Content: "答一", ReplyTo: "om_gone", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
		{MessageID: "om_c", SenderName: "沈知远", Content: "答二", ReplyTo: "om_raw", CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_gone": gone, "om_raw": raw}
	out := ansi.Strip(rowText(renderRows(msgs, st)))
	require.Contains(t, out, "▏孙琪: (Recalled)")
	require.Contains(t, out, "▏孙琪: [图片]", "a parent still waiting for its rendering is named by its type")
}
