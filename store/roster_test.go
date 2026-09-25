package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p2pStore is one chat of two, named the way Feishu names a p2p chat: after
// the peer.
func p2pStore(t *testing.T, peerID, peerType, chatName string, contacts ...Contact) (*Store, context.Context) {
	t.Helper()
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{
		ChatID: "oc_pair", Name: chatName, ChatMode: "p2p",
		P2PTargetID: peerID, P2PTargetType: peerType,
	}}, 1))
	require.NoError(t, s.UpsertContacts(ctx, contacts, 1))
	return s, ctx
}

func TestChatRoster_PairsThePeerAndSelfInAP2PChat(t *testing.T) {
	s, ctx := p2pStore(t, "ou_a", "user", "张三",
		Contact{OpenID: "ou_a", Name: "张三"}, Contact{OpenID: "ou_me", Name: "林岚"})

	roster, err := s.ChatRoster(ctx, "oc_pair", "ou_me")

	require.NoError(t, err)
	require.Len(t, roster, 2)
	// The peer leads: it is who a mention in a chat of two means.
	assert.Equal(t, "ou_a", roster[0].OpenID)
	assert.Equal(t, "张三", roster[0].Name)
	assert.Equal(t, "ou_me", roster[1].OpenID)
	assert.Equal(t, "林岚", roster[1].Name)
}

func TestChatRoster_NamesAnUnknownPeerByTheChatsOwnName(t *testing.T) {
	s, ctx := p2pStore(t, "ou_a", "user", "张三", Contact{OpenID: "ou_me", Name: "林岚"})

	roster, err := s.ChatRoster(ctx, "oc_pair", "ou_me")

	require.NoError(t, err)
	require.Len(t, roster, 2)
	assert.Equal(t, "张三", roster[0].Name)
}

func TestChatRoster_MarksABotPeerFromTheTargetType(t *testing.T) {
	s, ctx := p2pStore(t, "ou_bot", "bot", "构建机器人", Contact{OpenID: "ou_me", Name: "林岚"})

	roster, err := s.ChatRoster(ctx, "oc_pair", "ou_me")

	require.NoError(t, err)
	require.Len(t, roster, 2)
	assert.Equal(t, "构建机器人", roster[0].Name)
	assert.True(t, roster[0].IsBot)
}

func TestChatRoster_ListsOneEntryForTheChatWithYourself(t *testing.T) {
	s, ctx := p2pStore(t, "ou_me", "user", "林岚", Contact{OpenID: "ou_me", Name: "林岚"})

	roster, err := s.ChatRoster(ctx, "oc_pair", "ou_me")

	require.NoError(t, err)
	require.Len(t, roster, 1)
	assert.Equal(t, "ou_me", roster[0].OpenID)
}

func TestChatRoster_AnswersTheMemberListForAGroup(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_team", Name: "平台组", ChatMode: "group"}}, 1))
	members := []Contact{{OpenID: "ou_a", Name: "张三"}, {OpenID: "ou_bot", Name: "构建机器人", IsBot: true}}
	require.NoError(t, s.UpsertContacts(ctx, members, 1))
	require.NoError(t, s.SetChatMembers(ctx, "oc_team", members, 1))

	roster, err := s.ChatRoster(ctx, "oc_team", "ou_me")

	require.NoError(t, err)
	require.Len(t, roster, 2)
	assert.Equal(t, "张三", roster[0].Name)
	assert.Equal(t, "构建机器人", roster[1].Name)
	assert.True(t, roster[1].IsBot)
}
