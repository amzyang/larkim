package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTick_MembersContactsAndAvatars(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	f.Chats = []larkcli.RawChat{{ChatID: "oc_g", Name: "G", ChatMode: "group", Avatar: "https://cdn/g.png"}}
	f.Members["oc_g"] = []larkcli.ChatMember{{MemberID: "ou_a", Name: "Alice"}, {MemberID: "ou_b", Name: "Bob"}}
	f.Details["ou_a"] = larkcli.UserDetail{OpenID: "ou_a", Name: "Alice", AvatarURL: "https://cdn/a.png"}
	m := msg("om_1", "oc_g", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_a", SenderType: "user", SenderName: "Alice"}
	f.AddMessage(m)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, rep.Members)
	n, _ := s.Store.ChatMemberCount(ctx, "oc_g")
	require.Equal(t, int64(2), n)
	cs, _ := s.Store.ListContacts(ctx, "", 10)
	require.Len(t, cs, 2, "members and sender collapse into contacts")

	chat, _ := s.Store.GetChat(ctx, "oc_g")
	require.Equal(t, filepath.Join("resources", "avatars", "chats", "oc_g.png"), chat.AvatarPath)
	_, statErr := os.Stat(filepath.Join(s.Opt.DataDir, chat.AvatarPath))
	require.NoError(t, statErr)
	a, _ := s.Store.GetContact(ctx, "ou_a")
	require.Equal(t, "https://cdn/a.png", a.AvatarURL)
	require.Equal(t, filepath.Join("resources", "avatars", "users", "ou_a.png"), a.AvatarPath)
	b, _ := s.Store.GetContact(ctx, "ou_b")
	require.Equal(t, store.AvatarNone, b.AvatarURL, "unknown user recorded as having no avatar")
	require.Equal(t, 2, rep.Avatars)

	f.Calls = nil
	clk.t = clk.t.Add(time.Minute)
	rep, _ = s.Tick(ctx)
	require.Zero(t, rep.Members, "members fresh for 24h")
	require.Zero(t, rep.Avatars)
	require.NotContains(t, f.Calls, "user:ou_a")
}

func TestTick_RepairPassRelistsActiveChats(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_a", Name: "A", ChatMode: "group"}}
	f.AddMessage(msg("om_1", "oc_a", clk.t.Add(-2*24*time.Hour), "old"))
	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Repaired, "first pass starts immediately and re-lists the active chat")

	// The message is recalled remotely; the repair pass catches it next round.
	m := f.Messages["om_1"]
	m.Deleted = true
	f.Messages["om_1"] = m
	clk.t = clk.t.Add(time.Minute)
	rep, _ = s.Tick(ctx)
	require.Zero(t, rep.Repaired, "pass complete until RepairEvery elapses")
	clk.t = clk.t.Add(6 * time.Hour)
	rep, _ = s.Tick(ctx)
	require.Equal(t, 1, rep.Repaired)
	got, _ := s.Store.GetMessage(ctx, "om_1")
	require.True(t, got.Deleted)
}

func TestTick_ResolvesContactDetailsForP2PPartners(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_p", Name: "陈建伟", ChatMode: "p2p", P2PTargetID: "ou_cjw", P2PTargetType: "user"}}
	f.Users = []larkcli.User{{
		OpenID: "ou_cjw", Name: "陈建伟", Email: "chenjianwei01@gaotu.cn",
		EnterpriseEmail: "chenjianwei01@gaotu.cn", Department: "产品部", P2PChatID: "oc_p",
	}}
	m := msg("om_1", "oc_p", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_cjw", SenderType: "user", SenderName: "陈建伟"}
	f.AddMessage(m)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Contacts)

	c, err := s.Store.GetContact(ctx, "ou_cjw")
	require.NoError(t, err)
	require.Equal(t, "01", c.AccountSuffix(), "the account suffix disambiguates same-named colleagues")
	require.Equal(t, "产品部", c.Department)
}

func TestTick_DoesNotReAskForContactsTheSearchOmitted(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_p", Name: "Gone", ChatMode: "p2p", P2PTargetID: "ou_gone", P2PTargetType: "user"}}
	m := msg("om_1", "oc_p", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_gone", SenderType: "user", SenderName: "Gone"}
	f.AddMessage(m)

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	need, err := s.Store.ContactsNeedingDetail(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, need, "an id the search cannot resolve is marked checked, not retried every tick")
}
