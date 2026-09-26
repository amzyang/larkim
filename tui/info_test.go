package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func infoModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 140, 36
	m.chatID = "oc_group"
	m.chats = []store.Chat{{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		Description: "发布与值班", OwnerID: "ou_a"}}
	m.info = []store.Contact{
		{OpenID: "ou_a", Name: "张三"},
		{OpenID: "ou_b", Name: "李四"},
		{OpenID: "cli_c", Name: "构建机器人", IsBot: true},
	}
	return m, st
}

func infoText(m Model) string {
	return ansi.Strip(strings.Join(m.infoLines(40), "\n"))
}

func TestInfoLines_NamesTheChatAndItsMembers(t *testing.T) {
	m, _ := infoModel(t)

	got := infoText(m)

	assert.Contains(t, got, "平台组")
	assert.Contains(t, got, "group")
	assert.Contains(t, got, "发布与值班")
	assert.Contains(t, got, "Members 3")
	assert.Contains(t, got, "张三")
	assert.Contains(t, got, "构建机器人")
}

// chat_members is synced daily and was never shown before; the owner is the
// one member the chat row already knew about.
func TestInfoLines_MarksTheOwner(t *testing.T) {
	m, _ := infoModel(t)

	assert.Contains(t, infoText(m), "张三 owner")
}

// An external group was indistinguishable from any other before this.
func TestInfoLines_BadgesAnExternalChat(t *testing.T) {
	m, _ := infoModel(t)
	m.chats[0].External = true

	assert.Contains(t, infoText(m), "external")
}

func TestInfoLines_BadgesADissolvedChat(t *testing.T) {
	m, _ := infoModel(t)
	m.chats[0].ChatStatus = "dissolved"

	assert.Contains(t, infoText(m), "dissolved")
}

func TestInfoLines_ReportsASyncError(t *testing.T) {
	m, _ := infoModel(t)
	m.chats[0].SyncError = "restricted mode"

	assert.Contains(t, infoText(m), "sync:")
}

// "Members" of a pair is a question nobody asks, so a chat of two answers with
// the person across from it.
func TestInfoLines_P2PDrawsThePeersCard(t *testing.T) {
	m, _ := infoModel(t)
	m.chats[0] = store.Chat{ChatID: "oc_group", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a"}
	m.info = []store.Contact{{OpenID: "ou_a", Name: "张三",
		EnterpriseEmail: "zhangsan01@example.com", Department: "平台组", IsCrossTenant: true}}

	got := infoText(m)

	assert.Contains(t, got, "direct message")
	assert.Contains(t, got, "zhangsan01@example.com")
	assert.Contains(t, got, "平台组")
	assert.Contains(t, got, "outside this tenant")
	assert.NotContains(t, got, "Members")
}

func TestToggleInfo_TakesTheRightPaneFromTheThread(t *testing.T) {
	m, _ := infoModel(t)
	m.rightKind = rightThread

	next, cmd := m.toggleInfo()
	m = next.(Model)

	assert.True(t, m.infoOpen)
	assert.False(t, m.threadOpen(), "one thing about the chat at a time")
	assert.NotNil(t, cmd)
}

func TestToggleInfo_ClosesWhenAlreadyOpen(t *testing.T) {
	m, _ := infoModel(t)
	m.infoOpen = true

	next, _ := m.toggleInfo()

	assert.False(t, next.(Model).infoOpen)
}

func TestToggleInfo_WithoutAChatSaysSo(t *testing.T) {
	m, _ := infoModel(t)
	m.chatID = ""

	next, _ := m.toggleInfo()
	m = next.(Model)

	assert.False(t, m.infoOpen)
	assert.Contains(t, m.notice, "open a chat first")
}

func TestLoadInfo_ReadsTheRosterTheSyncStored(t *testing.T) {
	m, st := infoModel(t)
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_group", 1))
	members := []store.Contact{{OpenID: "ou_a", Name: "张三"}, {OpenID: "ou_b", Name: "李四"}}
	require.NoError(t, st.UpsertContacts(ctx, members, 1))
	require.NoError(t, st.SetChatMembers(ctx, "oc_group", members, false, 1))

	msg := loadInfo(m.deps, "oc_group")().(infoLoadedMsg)

	require.Len(t, msg.members, 2)
	assert.Equal(t, "oc_group", msg.chatID)
}

// A member the contacts table has never seen still counts: leaving it out
// would say the chat is smaller than it is.
func TestChatMembers_KeepsAMemberWithNoContactRow(t *testing.T) {
	_, st := infoModel(t)
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_group", 1))
	require.NoError(t, st.SetChatMembers(ctx, "oc_group",
		[]store.Contact{{OpenID: "ou_unknown"}}, false, 1))

	got, err := st.ChatMembers(ctx, "oc_group")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ou_unknown", got[0].OpenID)
	assert.Empty(t, got[0].Name)
}

// A capped roster is a part of the membership, and a bare count of it reads as
// the size of the chat.
func TestInfoLines_SaysWhenTheServerCappedTheRoster(t *testing.T) {
	m, _ := infoModel(t)
	m.chats[0].MembersTruncated = true

	got := infoText(m)

	assert.Contains(t, got, "Members partial")
	assert.Contains(t, got, "the server caps this list")
	assert.NotContains(t, got, "Members 3", "3 is what came back, not who is in the chat")
}
