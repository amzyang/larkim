package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fromBot is a main-flow message of the noisy sender the rules below name.
func fromBot(id, chatID string, createMs int64, text string) Message {
	m := msgAt(id, chatID, createMs, createMs, text)
	m.SenderID, m.SenderType, m.SenderName = "cli_c", "app", "Build bot"
	return m
}

func silencedOf(t *testing.T, s *Store, messageID string) bool {
	t.Helper()
	m, err := s.GetMessage(context.Background(), messageID)
	require.NoError(t, err)
	return m.Silenced
}

func TestSilenceRules_RejectARuleThatMatchesEverything(t *testing.T) {
	require.NoError(t, SilenceRules{{Chat: "oc_quiet"}}.Validate())
	require.ErrorContains(t, SilenceRules{{Sender: "cli_c"}, {}}.Validate(), "rule 2")
}

func TestSilenceRules_FingerprintFollowsTheRules(t *testing.T) {
	a := SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	require.Equal(t, a.Fingerprint(), SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}.Fingerprint())
	require.NotEqual(t, a.Fingerprint(), SilenceRules{{Chat: "oc_quiet", Sender: "ou_a"}}.Fingerprint())
	require.NotEqual(t, a.Fingerprint(), SilenceRules{}.Fingerprint())
}

func TestUpsertMessages_SilencesAMatchingSender(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{
		fromBot("om_noise", "oc_quiet", 200, "nightly build #418 passed"),
		msgAt("om_human", "oc_quiet", 100, 1, "did anyone look at it"),
	}, 1)
	require.NoError(t, err)

	require.True(t, silencedOf(t, s, "om_noise"))
	require.False(t, silencedOf(t, s, "om_human"), "the rule names one sender, not the chat")
}

func TestUpsertMessages_LeavesAnUnmatchedFieldAlone(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{fromBot("om_elsewhere", "oc_loud", 200, "nightly build #418 passed")}, 1)
	require.NoError(t, err)

	require.False(t, silencedOf(t, s, "om_elsewhere"), "the fields of one rule are an AND")
}

func TestUpsertMessages_SilencesOnTheRawBodyBeforeRendering(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Contains: "nightly build"}}
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_noise", "oc_quiet", 200, 1, "nightly build #418 passed")}, 1)
	require.NoError(t, err)

	require.True(t, silencedOf(t, s, "om_noise"), "a rule holds from the first tick, not from the render queue")
}

func TestUpdateRendered_SilencesOnTheRenderedBody(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Contains: "nightly build"}}
	ctx := context.Background()
	card := Message{MessageID: "om_card", ChatID: "oc_quiet", MsgType: "interactive", SenderID: "cli_c",
		SenderType: "app", ContentRaw: `{"elements":[{"tag":"div"}]}`, CreateMs: 200, UpdateMs: 200, MessagePosition: 1}
	_, err := s.UpsertMessages(ctx, []Message{card}, 1)
	require.NoError(t, err)
	require.False(t, silencedOf(t, s, "om_card"), "the body carries no text yet")

	require.NoError(t, s.UpdateRendered(ctx, "om_card", "nightly build #418 passed", "", "", 2))
	require.True(t, silencedOf(t, s, "om_card"))
}

func TestUpdateRendered_ClearsSilenceWhenTheRenderingStopsMatching(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Contains: "nightly build"}}
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_quiet", 200, 1, "nightly build")}, 1)
	require.NoError(t, err)
	require.True(t, silencedOf(t, s, "om_a"))

	require.NoError(t, s.UpdateRendered(ctx, "om_a", "the release is out", "", "", 2))
	require.False(t, silencedOf(t, s, "om_a"), "the rendering is what the rule matches once it lands")
}

func TestUpdateRendered_RefreshesTheSummaryWhenSilenceFlips(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Contains: "nightly build"}}
	ctx := context.Background()
	card := Message{MessageID: "om_card", ChatID: "oc_quiet", MsgType: "interactive", SenderID: "cli_c",
		SenderType: "app", ContentRaw: `{"elements":[]}`, CreateMs: 200, UpdateMs: 200, MessagePosition: 2}
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_human", "oc_quiet", 100, 1, "morning"), card}, 1)
	require.NoError(t, err)
	require.Equal(t, int64(200), summaryOf(t, s, "oc_quiet").LastUnsilencedMs)

	require.NoError(t, s.UpdateRendered(ctx, "om_card", "nightly build #418 passed", "", "", 2))
	c := summaryOf(t, s, "oc_quiet")
	require.Equal(t, "om_card", c.LastMessageID, "the row still shows what arrived last")
	require.Equal(t, int64(100), c.LastUnsilencedMs, "the chat keeps the place its last real message gave it")
}

func TestReapplySilence_RewritesEveryMessageWhenTheRulesChange(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_human", "oc_quiet", 100, 1, "morning"),
		fromBot("om_noise", "oc_quiet", 200, "nightly build #418 passed"),
	}, 1)
	require.NoError(t, err)
	require.False(t, silencedOf(t, s, "om_noise"))

	s.Silence = SilenceRules{{Sender: "cli_c"}}
	n, err := s.ReapplySilence(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.True(t, silencedOf(t, s, "om_noise"))
	require.Equal(t, int64(100), summaryOf(t, s, "oc_quiet").LastUnsilencedMs, "the sort key follows the rescan")

	s.Silence = nil
	n, err = s.ReapplySilence(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.False(t, silencedOf(t, s, "om_noise"), "dropping the rules restores the list")
	require.Equal(t, int64(200), summaryOf(t, s, "oc_quiet").LastUnsilencedMs)
}

func TestReapplySilence_WritesNothingWhenTheFingerprintMatches(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Sender: "cli_c"}}
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{fromBot("om_noise", "oc_quiet", 200, "nightly build")}, 1)
	require.NoError(t, err)
	_, err = s.ReapplySilence(ctx)
	require.NoError(t, err)

	before, err := s.DataRev(ctx)
	require.NoError(t, err)
	n, err := s.ReapplySilence(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	after, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "an unchanged rule set must not wake every consumer")
}

func TestRefreshChatSummary_KeepsTheNewestMessageAndSinksTheSortKey(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Sender: "cli_c"}}
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_human", "oc_quiet", 100, 1, "morning"),
		fromBot("om_noise", "oc_quiet", 200, "nightly build #418 passed"),
	}, 1)
	require.NoError(t, err)

	c := summaryOf(t, s, "oc_quiet")
	require.Equal(t, "om_noise", c.LastMessageID)
	require.Equal(t, int64(200), c.LastMessageMs, "the row's timestamp is the newest message's")
	require.Equal(t, int64(100), c.LastUnsilencedMs)
}

func TestListChats_SinksAFullySilencedChat(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet"}}
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))
	require.NoError(t, s.EnsureChat(ctx, "oc_loud", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		fromBot("om_noise", "oc_quiet", 300, "nightly build #418 passed"),
		msgAt("om_human", "oc_loud", 100, 1, "morning"),
	}, 1)
	require.NoError(t, err)

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"oc_loud", "oc_quiet"}, chatIDs(chats))
	require.Equal(t, "om_noise", chats[1].LastMessageID, "the row sank but still says what is in there")
}

func TestMarkChatRead_MarksSilencedMessagesToo(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Sender: "cli_c"}}
	ctx := context.Background()
	_, err := s.UpsertMessages(ctx, []Message{fromBot("om_noise", "oc_quiet", 200, "nightly build")}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_noise")

	require.NoError(t, s.MarkChatRead(ctx, "oc_quiet", 500))
	m, err := s.GetMessage(ctx, "om_noise")
	require.NoError(t, err)
	require.Equal(t, int64(500), m.LocalReadAt,
		"a message the reader had in front of them is read, badge or no badge")
}

func TestSilenceMatches_CountsOneRule(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_human", "oc_quiet", 100, 1, "morning"),
		fromBot("om_noise", "oc_quiet", 200, "nightly build #418 passed"),
	}, 1)
	require.NoError(t, err)

	n, lastMs, err := s.SilenceMatches(ctx, SilenceRule{Sender: "cli_c"})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.Equal(t, int64(200), lastMs)

	n, lastMs, err = s.SilenceMatches(ctx, SilenceRule{Sender: "cli_typo"})
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, lastMs, "a rule that matches nothing is the failure mode worth seeing")
}
