package tui

import (
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

func TestSetChatMutedCmd_PersistsToStore(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.UpsertChats(ctx, []store.Chat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}, 1))

	f := larkcli.NewFake()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "平台组"}}

	d := Deps{Store: st, Client: f}
	msg := setChatMutedCmd(d, "oc_a", true)().(chatMutedMsg)
	require.NoError(t, msg.err)
	require.True(t, msg.muted)

	c, err := st.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.True(t, c.Muted)
	require.Greater(t, c.MuteCheckedAt, int64(0))
}
