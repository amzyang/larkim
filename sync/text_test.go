package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTextLocal_PutsTheNamesBackWhereThePlaceholdersStand(t *testing.T) {
	t.Parallel()
	// Feishu leaves a mention as @_user_n in the body and the name in the
	// message's mention list, which is the one thing a text body does not say
	// for itself.
	got := textLocal(store.PendingLocalMessage{MsgType: "text",
		ContentRaw:   `{"text":"@_user_1 和 @_user_2 看下发布计划"}`,
		MentionsJSON: `[{"id":"ou_a","key":"@_user_1","name":"张三"},{"id":"ou_b","key":"@_user_2","name":"李四"}]`})
	require.Equal(t, "@张三 和 @李四 看下发布计划", got)
}

func TestResolveMentions_TakesTheLongestKeyFirst(t *testing.T) {
	t.Parallel()
	// @_user_1 is a prefix of @_user_10, so replacing in the order the list
	// arrives in would leave a stray 0 behind.
	ms := `[{"id":"ou_a","key":"@_user_1","name":"张三"},{"id":"ou_b","key":"@_user_10","name":"王五"}]`
	require.Equal(t, "@王五 和 @张三", ResolveMentions("@_user_10 和 @_user_1", ms))
}

func TestTextLocal_UnwrapsTheParagraphsAnEditLeaves(t *testing.T) {
	t.Parallel()
	got := textLocal(store.PendingLocalMessage{MsgType: "text", ContentRaw: `{"text":"<p>甲</p><p>乙</p>"}`})
	require.Equal(t, "甲\n乙", got)
}

func TestTextLocal_ABodyItCannotReadIsKept(t *testing.T) {
	t.Parallel()
	require.Equal(t, "not json", textLocal(store.PendingLocalMessage{MsgType: "text", ContentRaw: "not json"}))
}

func TestMentionsJSON_HoldsWhatTheColumnDocuments(t *testing.T) {
	t.Parallel()
	// The API sends an id_type and a tenant_key besides, and the column's
	// contract is [{id,key,name}] minified, so an id can be matched as text.
	require.Equal(t, `[{"id":"ou_a","key":"@_user_1","name":"张三"}]`,
		mentionsJSON([]larkcli.RawMention{{Key: "@_user_1", ID: "ou_a", IDType: "open_id", Name: "张三"}}))
	require.Empty(t, mentionsJSON(nil), "a message that names nobody carries no list")
}

func TestTick_ATextMessageIsRenderedWithoutACall(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	m := msg("om_at", "oc_a", clk.t.Add(-time.Minute), "@_user_1 看下")
	m.Mentions = []larkcli.RawMention{{Key: "@_user_1", ID: "ou_me", IDType: "open_id", Name: "林岚"}}
	f.AddMessage(m)

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	got, err := s.Store.GetMessage(ctx, "om_at")
	require.NoError(t, err)
	require.Equal(t, "@林岚 看下", got.Content)
	require.NotZero(t, got.RenderedAt)
	require.Equal(t, `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, got.MentionsJSON,
		"the mention list comes with the body, so it is there before any rendering")
	require.Zero(t, callsTo(f, "render"), "nothing was asked of lark-cli")
}

func TestIngestIDs_SpendsNoRenderCallOnABodyLarkimReadsItself(t *testing.T) {
	t.Parallel()
	// A send waits on this path, so a call asking lark-cli to read back a body
	// already on disk is the one worth not making.
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	f.AddMessage(msg("om_sent", "oc_a", clk.t, "发布好了"))

	require.NoError(t, s.IngestIDs(ctx, []string{"om_sent"}))

	got, err := s.Store.GetMessage(ctx, "om_sent")
	require.NoError(t, err)
	require.Equal(t, "发布好了", got.Content, "rendered in process, in the same breath as the ingest")
	require.Zero(t, callsTo(f, "render"))
}

func TestTick_AStickerIsRenderedWithoutACall(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	s.Opt().DataDir = t.TempDir()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	st := msg("om_st", "oc_a", clk.t.Add(-time.Minute), "")
	st.MsgType, st.Body.Content = "sticker", `{"file_key":"v3_face"}`
	f.AddMessage(st)

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	got, err := s.Store.GetMessage(ctx, "om_st")
	require.NoError(t, err)
	require.Equal(t, "[Sticker]", got.Content, "the whole of what a rendering would say")
	require.NotZero(t, got.RenderedAt)
	require.Zero(t, callsTo(f, "render"))
}

func TestIngestIDs_RendersTheMessageItPulledRatherThanTheQueuesHead(t *testing.T) {
	t.Parallel()
	// A recall or a forward ingests an older message; taking the local queue's
	// newest rows instead would leave the one somebody is waiting on unrendered.
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	for i, id := range []string{"om_old", "om_newer", "om_newest"} {
		f.AddMessage(msg(id, "oc_a", clk.t.Add(time.Duration(i)*time.Minute), id))
	}

	require.NoError(t, s.IngestIDs(ctx, []string{"om_old"}))

	got, err := s.Store.MessagesByIDs(ctx, []string{"om_old", "om_newest"})
	require.NoError(t, err)
	require.Equal(t, "om_old", got["om_old"].Content)
	require.Zero(t, got["om_newest"].RenderedAt, "the ingest rendered what it was asked for and no more")
}
