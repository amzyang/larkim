package tui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestFeishuChatLink_UsesTheClientScheme(t *testing.T) {
	require.Equal(t, "lark://applink.feishu.cn/client/chat/open?openChatId=oc_1",
		feishuChatLink("oc_1", 0))
}

func TestFeishuChatLink_CarriesAMessagePosition(t *testing.T) {
	require.Equal(t, "lark://applink.feishu.cn/client/chat/open?openChatId=oc_1&position=227",
		feishuChatLink("oc_1", 227))
	require.NotContains(t, feishuChatLink("oc_1", -1), "position",
		"a thread reply has no position of its own")
}

func TestLoadMeta_NamesTheReactorsTheBlockOnlyHoldsIDsFor(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{{OpenID: "ou_b", Name: "李四"}}, 1))

	msgs := []store.Message{{MessageID: "om_a", ChatID: "oc_a", SenderID: "ou_a", SenderName: "张三",
		ReactionsJSON: `{"counts":[{"reaction_type":"OK","count":"1"}],
		  "details":[{"emoji_type":"OK","action_time":"1790155041","operator":{"operator_id":"ou_b"}}]}`}}
	meta, err := loadMeta(ctx, st, msgs)
	require.NoError(t, err)
	require.Equal(t, "李四", meta.people["ou_b"], "the block holds an id alone; the name comes from the contacts")
}
