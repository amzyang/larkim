package sync

import (
	"context"
	"encoding/json"
	"fmt"
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
	cs, _ := s.Store.ListContacts(ctx, 10)
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
	f.Chats = []larkcli.RawChat{{ChatID: "oc_p", Name: "李明", ChatMode: "p2p", P2PTargetID: "ou_lm", P2PTargetType: "user"}}
	f.Users = []larkcli.User{{
		OpenID: "ou_lm", Name: "李明", Email: "liming01@example.com",
		EnterpriseEmail: "liming01@example.com", Department: "产品部", P2PChatID: "oc_p",
	}}
	m := msg("om_1", "oc_p", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_lm", SenderType: "user", SenderName: "李明"}
	f.AddMessage(m)

	rep, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Contacts)

	c, err := s.Store.GetContact(ctx, "ou_lm")
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

func TestTick_MarksWithheldContactsAsHavingNoAvatar(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	f.Chats = []larkcli.RawChat{{ChatID: "oc_g", Name: "G", ChatMode: "group"}}
	f.Members["oc_g"] = []larkcli.ChatMember{{MemberID: "ou_in", Name: "In"}, {MemberID: "ou_out", Name: "Out"}}
	// Only ou_in is within the app's directory scope.
	f.Details["ou_in"] = larkcli.UserDetail{OpenID: "ou_in", Name: "In", AvatarURL: "https://cdn/in.png"}
	m := msg("om_1", "oc_g", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_in", SenderType: "user", SenderName: "In"}
	f.AddMessage(m)

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	in, err := s.Store.GetContact(ctx, "ou_in")
	require.NoError(t, err)
	require.Equal(t, "https://cdn/in.png", in.AvatarURL)

	out, err := s.Store.GetContact(ctx, "ou_out")
	require.NoError(t, err)
	require.Equal(t, store.AvatarNone, out.AvatarURL, "a withheld user is settled, not retried every tick")

	need, err := s.Store.ContactsNeedingAvatar(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, need)
}

func TestTick_APermanentAvatarRejectionDoesNotWedgeLaterSlices(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	f.Chats = []larkcli.RawChat{{ChatID: "oc_p", Name: "P", ChatMode: "p2p", P2PTargetID: "ou_a", P2PTargetType: "user"}}
	f.Users = []larkcli.User{{OpenID: "ou_a", Name: "A", EnterpriseEmail: "a01@x.cn"}}
	f.DetailsErr = &larkcli.Error{ExitCode: larkcli.ExitAPI, Type: "api", Subtype: "permission_denied", Code: 41050}
	m := msg("om_1", "oc_p", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_a", SenderType: "user", SenderName: "A"}
	f.AddMessage(m)

	rep, err := s.Tick(ctx)
	require.NoError(t, err, "a permanent rejection settles the batch instead of failing the tick")
	require.Equal(t, 1, rep.Contacts, "the slice after avatars still ran")

	c, err := s.Store.GetContact(ctx, "ou_a")
	require.NoError(t, err)
	require.Equal(t, store.AvatarNone, c.AvatarURL)

	need, err := s.Store.ContactsNeedingAvatar(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, need, "and the same ids do not come back next tick")
}

func TestTick_ATransientAvatarFailureStillFailsTheTick(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) { return nil, "", nil }
	f.Chats = []larkcli.RawChat{{ChatID: "oc_p", Name: "P", ChatMode: "p2p", P2PTargetID: "ou_a", P2PTargetType: "user"}}
	f.DetailsErr = &larkcli.Error{ExitCode: larkcli.ExitNetwork, Type: "network"}
	m := msg("om_1", "oc_p", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_a", SenderType: "user", SenderName: "A"}
	f.AddMessage(m)

	_, err := s.Tick(ctx)
	require.Error(t, err, "a network blip must not be recorded as 'this user has no avatar'")

	c, err := s.Store.GetContact(ctx, "ou_a")
	require.NoError(t, err)
	require.Empty(t, c.AvatarURL, "the contact stays queued")
}

// botMsg is a message as a bot sends it: the sender carries the app id that a
// bot's picture has to be looked up by.
func botMsg(id, chatID, botOpenID, appID string, at time.Time) larkcli.RawMessage {
	m := msg(id, chatID, at, "beep")
	m.Sender = larkcli.RawSender{ID: appID, IDType: "app_id", SenderType: "app", SenderName: "Bot", OpenBotID: botOpenID}
	m.Raw, _ = json.Marshal(map[string]any{
		"message_id": id, "chat_id": chatID, "msg_type": "text",
		"create_time": fmt.Sprint(at.UnixMilli()),
		"body":        map[string]string{"content": `{"text":"beep"}`},
		"sender": map[string]any{
			"id": appID, "id_type": "app_id", "sender_type": "app",
			"sender_name": "Bot", "open_bot_id": botOpenID,
		},
	})
	return m
}

func TestTick_ResolvesBotAvatarsThroughTheirApp(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	f.Chats = []larkcli.RawChat{{ChatID: "oc_b", Name: "轻舟平台", ChatMode: "p2p",
		P2PTargetID: "ou_bot", P2PTargetType: "bot"}}
	f.Apps["cli_x"] = larkcli.AppDetail{AppID: "cli_x", Name: "轻舟平台", AvatarURL: "https://cdn/bot.png"}
	f.AddMessage(botMsg("om_1", "oc_b", "ou_bot", "cli_x", clk.t.Add(-time.Minute)))

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	c, err := s.Store.GetContact(ctx, "ou_bot")
	require.NoError(t, err)
	require.Equal(t, "https://cdn/bot.png", c.AvatarURL)
	require.True(t, c.IsBot)

	require.Empty(t, mustBots(t, s), "and it is not asked for again")
}

func TestBotsNeedingAvatar_SkipsBotsThatNeverSpoke(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	f.Chats = []larkcli.RawChat{{ChatID: "oc_b", Name: "Quiet", ChatMode: "p2p",
		P2PTargetID: "ou_quiet", P2PTargetType: "bot"}}
	// A human message keeps the tick busy; the bot itself says nothing.
	m := msg("om_1", "oc_b", clk.t.Add(-time.Minute), "hi")
	m.Sender = larkcli.RawSender{ID: "ou_quiet", SenderType: "user", SenderName: "Quiet"}
	f.AddMessage(m)

	_, err := s.Tick(ctx)
	require.NoError(t, err)
	require.Empty(t, mustBots(t, s), "without a message there is no app id, so nothing to ask for")
}

func TestTick_SettlesABotWhoseAppTheTenantWillNotShow(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	s.Opt.DataDir = t.TempDir()
	s.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	f.Chats = []larkcli.RawChat{{ChatID: "oc_b", Name: "Hidden", ChatMode: "p2p",
		P2PTargetID: "ou_bot", P2PTargetType: "bot"}}
	// f.Apps has no entry, so AppDetail answers 210508 as upstream would.
	f.AddMessage(botMsg("om_1", "oc_b", "ou_bot", "cli_hidden", clk.t.Add(-time.Minute)))

	_, err := s.Tick(ctx)
	require.NoError(t, err, "a refused app must not fail the tick")

	c, err := s.Store.GetContact(ctx, "ou_bot")
	require.NoError(t, err)
	require.Equal(t, store.AvatarNone, c.AvatarURL)
	require.Empty(t, mustBots(t, s))
}

func mustBots(t *testing.T, s *Syncer) []store.BotRef {
	t.Helper()
	b, err := s.Store.BotsNeedingAvatar(context.Background(), 10)
	require.NoError(t, err)
	return b
}

// A bot only sees the message that names it, so a roster has to record which
// members are bots for @ to be able to offer them.
func TestTick_MembersFileABotAsABot(t *testing.T) {
	s, f, _ := newSyncer(t)
	ctx := context.Background()
	f.Chats = []larkcli.RawChat{{ChatID: "oc_g", Name: "平台组", ChatMode: "group"}}
	f.Members["oc_g"] = []larkcli.ChatMember{
		{MemberID: "ou_a", Name: "张三"},
		{MemberID: "ou_bot", Name: "构建机器人", IsBot: true},
	}

	_, err := s.Tick(ctx)
	require.NoError(t, err)

	roster, err := s.Store.ChatRoster(ctx, "oc_g", "ou_me")
	require.NoError(t, err)
	require.Len(t, roster, 2)
	require.Equal(t, "构建机器人", roster[1].Name)
	require.True(t, roster[1].IsBot)
	require.False(t, roster[0].IsBot)
}
