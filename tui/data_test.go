package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestFeishuChatLink_UsesTheClientScheme(t *testing.T) {
	require.Equal(t, "lark://applink.feishu.cn/client/chat/open?openChatId=oc_1",
		applink.ChatLink("oc_1", 0))
}

func TestFeishuChatLink_CarriesAMessagePosition(t *testing.T) {
	require.Equal(t, "lark://applink.feishu.cn/client/chat/open?openChatId=oc_1&position=227",
		applink.ChatLink("oc_1", 227))
	require.NotContains(t, applink.ChatLink("oc_1", -1), "position",
		"a thread reply has no position of its own")
}

func TestLoadMeta_NamesTheReactorsTheBlockOnlyHoldsIDsFor(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{{OpenID: "ou_b", Name: "李四"}}, 1))

	msgs := []store.Message{{MessageID: "om_a", ChatID: "oc_a", SenderID: "ou_a", SenderName: "张三",
		ReactionsJSON: `{"counts":[{"reaction_type":"OK","count":"1"}],
		  "details":[{"emoji_type":"OK","action_time":"1790155041","operator":{"operator_id":"ou_b"}}]}`}}
	meta, err := loadMeta(ctx, st, "ou_me", msgs)
	require.NoError(t, err)
	require.Equal(t, "李四", meta.people["ou_b"], "the block holds an id alone; the name comes from the contacts")
}

func TestLoadMeta_NamesTheThreadReplierThePageNeverHeardFrom(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_b", Name: "李四", AvatarPath: "avatars/ou_b.png"},
	}, 1))
	require.NoError(t, st.SetContactDetails(ctx, []string{"ou_b"},
		[]store.ContactDetail{{OpenID: "ou_b", Name: "李四", EnterpriseEmail: "lisi02@example.com"}}, 1))
	root := store.Message{MessageID: "om_root", ChatID: "oc_a", MsgType: "text", CreateMs: 100,
		MessagePosition: 100, SenderID: "ou_a", SenderName: "张三", ThreadID: "omt_1",
		ContentRaw: `{"text":"排期"}`, RawJSON: "{}"}
	reply := store.Message{MessageID: "om_reply", ChatID: "oc_a", MsgType: "text", CreateMs: 110,
		MessagePosition: -3, SenderID: "ou_b", SenderName: "李四", ThreadID: "omt_1",
		ContentRaw: `{"text":"收到"}`, RawJSON: "{}"}
	_, err = st.UpsertMessages(ctx, []store.Message{root, reply}, 1)
	require.NoError(t, err)

	meta, err := loadMeta(ctx, st, "ou_me", []store.Message{root})
	require.NoError(t, err)
	require.Equal(t, "02", meta.suffix["ou_b"], "the collapsed thread line names them, so they need a suffix")
	require.Equal(t, "avatars/ou_b.png", meta.avatars["ou_b"], "and a picture for the line's own lead")
}

// A send is a keypress waiting on a subprocess, so it must not queue behind
// the syncer's sweeps.
func TestWaited_TakesTheInteractiveLane(t *testing.T) {
	ctx, cancel := waited(time.Second)
	defer cancel()
	require.Equal(t, larkcli.LaneInteractive, larkcli.LaneOf(ctx))
	_, ok := ctx.Deadline()
	require.True(t, ok, "an interactive call still needs a deadline of its own")
}

// The 1.5s beat is not a keypress. Sharing the interactive lane, its listing
// and the refresh riding along with it held two of three slots and a send
// landed behind them.
func TestBeat_TakesTheBeatLane(t *testing.T) {
	ctx, cancel := beat(time.Second)
	defer cancel()
	require.Equal(t, larkcli.LaneBeat, larkcli.LaneOf(ctx))
	_, ok := ctx.Deadline()
	require.True(t, ok, "a beat still needs a deadline of its own")
}

// A recall is an event in the chat, not an absence: the client draws a notice
// where the message stood, so the page has to be handed the row.
func TestMessageQuery_KeepsARecallOnThePage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	kept := store.Message{MessageID: "om_kept", ChatID: "oc_a", MsgType: "text", CreateMs: 100,
		MessagePosition: 100, SenderID: "ou_a", SenderName: "张三", ContentRaw: `{"text":"排期"}`, RawJSON: "{}"}
	gone := store.Message{MessageID: "om_gone", ChatID: "oc_a", MsgType: "text", CreateMs: 110,
		MessagePosition: 101, SenderID: "ou_a", SenderName: "张三", Deleted: true, RawJSON: "{}"}
	_, err = st.UpsertMessages(ctx, []store.Message{kept, gone}, 1)
	require.NoError(t, err)

	rows, err := st.ListMessages(ctx, messageQuery("oc_a", 0, 50))
	require.NoError(t, err)
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.MessageID
	}
	require.ElementsMatch(t, []string{"om_kept", "om_gone"}, ids)
}

func TestThreadQuery_KeepsARecalledReply(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	root := store.Message{MessageID: "om_root", ChatID: "oc_a", MsgType: "text", CreateMs: 100,
		MessagePosition: 100, SenderID: "ou_a", SenderName: "张三", ThreadID: "omt_1",
		ContentRaw: `{"text":"排期"}`, RawJSON: "{}"}
	gone := store.Message{MessageID: "om_gone", ChatID: "oc_a", MsgType: "text", CreateMs: 110,
		MessagePosition: -3, SenderID: "ou_b", SenderName: "李四", ThreadID: "omt_1",
		Deleted: true, RawJSON: "{}"}
	_, err = st.UpsertMessages(ctx, []store.Message{root, gone}, 1)
	require.NoError(t, err)

	rows, err := st.ListMessages(ctx, threadQuery("omt_1"))
	require.NoError(t, err)
	require.Len(t, rows, 2, "a recalled reply keeps its slot, so the pane and the reply count agree")
}

func TestLoadMeta_CarriesTheWritingReadOutOfAPicture(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_shot", ChatID: "oc_a", MsgType: "image", CreateMs: 10, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.AddPendingResources(ctx, []store.ResourceRef{
		{MessageID: "om_shot", FileKey: "img_a", Type: "image"}}))
	require.NoError(t, st.MarkResourceDone(ctx, "img_a", "resources/lark-im-resources/img_a.png", 10))
	require.NoError(t, st.MarkResourceText(ctx, "img_a", "回归用例全绿"))

	meta, err := loadMeta(ctx, st, "ou_me", []store.Message{{MessageID: "om_shot", ChatID: "oc_a"}})
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"om_shot": {"回归用例全绿"}}, meta.imgText)
}
