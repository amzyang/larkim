package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchMessages_TrigramMatchesCJKSubstrings(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_1", ChatID: "oc_a", SenderName: "林岚", CreateMs: 1, RawJSON: "{}"},
		{MessageID: "om_2", ChatID: "oc_b", SenderName: "张三", CreateMs: 2, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.UpdateRendered(ctx, "om_1", "今天下午三点开会讨论 release 计划", "", "", 1))
	require.NoError(t, s.UpdateRendered(ctx, "om_2", "release notes are ready", "", "", 1))

	hits, err := s.SearchMessages(ctx, "开会讨论", "", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "om_1", hits[0].MessageID)

	hits, _ = s.SearchMessages(ctx, "release", "", 10)
	require.Len(t, hits, 2)
	require.Equal(t, "om_2", hits[0].MessageID, "newest first")
	hits, _ = s.SearchMessages(ctx, "release 计划", "", 10)
	require.Len(t, hits, 1, "all terms must match")
	hits, _ = s.SearchMessages(ctx, "release", "oc_b", 10)
	require.Len(t, hits, 1)
	hits, _ = s.SearchMessages(ctx, "林岚", "", 10)
	require.Len(t, hits, 1, "two-character terms use the substring fallback")
	hits, _ = s.SearchMessages(ctx, "三点", "", 10)
	require.Len(t, hits, 1)

	// Re-rendering keeps the index in step.
	require.NoError(t, s.UpdateRendered(ctx, "om_1", "改成明天", "", "", 2))
	hits, _ = s.SearchMessages(ctx, "开会讨论", "", 10)
	require.Empty(t, hits)
}

func TestMembersRepairAndAvatars(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_g", Name: "G", ChatMode: "group", AvatarURL: "https://x/g.jpg"}, {ChatID: "oc_p", Name: "P", ChatMode: "p2p"}}, 1))
	_, err := s.UpsertMessages(ctx, []Message{{MessageID: "om_1", ChatID: "oc_g", SenderID: "ou_a", CreateMs: 5000, RawJSON: "{}"}}, 1)
	require.NoError(t, err)

	need, _ := s.ChatsNeedingMembers(ctx, 100, 10)
	require.Len(t, need, 1, "p2p chats have no member list")
	require.NoError(t, s.SetChatMembers(ctx, "oc_g", []Contact{{OpenID: "ou_a", Name: "A"}, {OpenID: "ou_bot", IsBot: true}}, false, 200))
	n, _ := s.ChatMemberCount(ctx, "oc_g")
	require.Equal(t, int64(2), n)
	need, _ = s.ChatsNeedingMembers(ctx, 100, 10)
	require.Empty(t, need)

	rep, _ := s.ChatsForRepair(ctx, 1000, 300, 10)
	require.Len(t, rep, 1)
	require.NoError(t, s.SetChatRepaired(ctx, "oc_g", 300))
	rep, _ = s.ChatsForRepair(ctx, 1000, 300, 10)
	require.Empty(t, rep)

	require.NoError(t, s.UpsertContacts(ctx, []Contact{{OpenID: "ou_a", Name: "A"}, {OpenID: "ou_bot", IsBot: true}}, 1))
	cs, _ := s.ContactsNeedingAvatar(ctx, 10)
	require.Len(t, cs, 1)
	require.NoError(t, s.SetContactAvatar(ctx, "ou_a", "https://x/a.png", 2))
	chats, contacts, err := s.AvatarsToDownload(ctx, 10)
	require.NoError(t, err)
	require.Len(t, chats, 1)
	require.Len(t, contacts, 1)
	require.NoError(t, s.SetChatAvatarPath(ctx, "oc_g", "resources/avatars/chats/oc_g.jpg"))
	require.NoError(t, s.SetContactAvatarPath(ctx, "ou_a", "resources/avatars/users/ou_a.png"))
	chats, contacts, _ = s.AvatarsToDownload(ctx, 10)
	require.Empty(t, chats)
	require.Empty(t, contacts)
}
