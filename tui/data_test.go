package tui

import (
	"testing"

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
