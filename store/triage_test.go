package store

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedTriage stores one chat with a spread of messages around the window an
// Untriaged call at now=10_000 with since=5_000 reads.
func seedTriage(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"}}, 1))
	msg := func(id string, at int64) Message {
		return Message{MessageID: id, ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_a", SenderType: "user",
			SenderName: "张三", ContentRaw: `{"text":"x"}`, CreateMs: at, MessagePosition: at, RawJSON: "{}"}
	}
	old, fresh, unrendered, recalled, quiet := msg("om_old", 1_000), msg("om_fresh", 6_000),
		msg("om_unrendered", 7_000), msg("om_recalled", 8_000), msg("om_quiet", 9_000)
	recalled.Deleted = true
	_, err := s.UpsertMessages(ctx, []Message{old, fresh, unrendered, recalled, quiet}, 10_000)
	require.NoError(t, err)
	for _, id := range []string{"om_old", "om_fresh", "om_recalled", "om_quiet"} {
		require.NoError(t, s.UpdateRendered(ctx, id, "x", "", 10_000))
	}
	_, err = s.DB().ExecContext(ctx, `UPDATE messages SET silenced = 1 WHERE message_id = 'om_quiet'`)
	require.NoError(t, err)
}

func TestUntriaged_ReadsRecentRenderedLiveUnsilencedArrivalsOnce(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)

	got, err := s.Untriaged(ctx, 5_000, 50)
	require.NoError(t, err)
	require.Equal(t, []string{"om_fresh"}, ids(got),
		"older than the window, unrendered, recalled and silenced messages are not judged")

	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_fresh", ChatID: "oc_quiet", Level: "P1", Reason: "p1", JudgedMs: 10_000}))
	got, err = s.Untriaged(ctx, 5_000, 50)
	require.NoError(t, err)
	require.Empty(t, got, "a judged message is not judged again")
}

func TestPutTriage_StoresJevsAnswerCompactAndARuleVerdictWithout(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)
	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_fresh", ChatID: "oc_quiet", Level: "P1", Reason: "jev:fyi",
		Jev: jsontext.Value("{\n  \"model\": \"jev-1.13.0\",\n  \"fits\": 0.2\n}"), JudgedMs: 1}))
	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_old", ChatID: "oc_quiet", Level: "P0", Reason: "p2p", JudgedMs: 1}))

	var stored *string
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT jev_json FROM triage WHERE message_id = 'om_old'`).Scan(&stored))
	require.Nil(t, stored, "a rule verdict has no answer: NULL, not an empty string")

	rows, err := s.ListTriage(ctx, TriageQuery{})
	require.NoError(t, err)
	byID := map[string]TriageEntry{}
	for _, r := range rows {
		byID[r.MessageID] = r
	}
	require.Equal(t, `{"model":"jev-1.13.0","fits":0.2}`, string(byID["om_fresh"].Jev))
	require.Empty(t, byID["om_old"].Jev)
}

func TestPutTriage_KeepsTheFirstVerdict(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)
	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_fresh", ChatID: "oc_quiet", Level: "P0", Reason: "p2p", JudgedMs: 1}))
	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_fresh", ChatID: "oc_quiet", Level: "P1", Reason: "p1", JudgedMs: 2}))

	rows, err := s.ListTriage(ctx, TriageQuery{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "P0", rows[0].Level)
}

func TestTriageQueues_HandOutP0RowsUntilStamped(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)
	p := 0.7
	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_fresh", ChatID: "oc_quiet", Level: "P0", Reason: "jev", JevP: &p, JudgedMs: 1}))
	require.NoError(t, s.PutTriage(ctx, Triage{MessageID: "om_old", ChatID: "oc_quiet", Level: "P1", Reason: "p1", JudgedMs: 1}))

	notify, err := s.TriageToNotify(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"om_fresh"}, triageIDs(notify), "only P0 rows raise a banner")
	require.NoError(t, s.MarkTriageNotified(ctx, "om_fresh", 5))
	notify, err = s.TriageToNotify(ctx)
	require.NoError(t, err)
	require.Empty(t, notify)

	draft, err := s.TriageToDraft(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"om_fresh"}, triageIDs(draft))
	require.NoError(t, s.FailTriageDraft(ctx, "om_fresh"))
	draft, err = s.TriageToDraft(ctx, 2)
	require.NoError(t, err)
	require.Len(t, draft, 1, "one failure leaves a try")
	require.NoError(t, s.FailTriageDraft(ctx, "om_fresh"))
	draft, err = s.TriageToDraft(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, draft, "two failures give it up")

	rows, err := s.ListTriage(ctx, TriageQuery{Level: "P0"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 0.7, *rows[0].JevP)
	require.Equal(t, int64(5), rows[0].NotifiedMs)
	require.Equal(t, 2, rows[0].DraftTries)
	require.Equal(t, "平台组", rows[0].ChatName)
	require.Equal(t, "张三", rows[0].SenderName)
}

func TestAnswered_SeesTheReadersOwnLaterMessage(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)
	answered, err := s.Answered(ctx, "om_fresh", "ou_self")
	require.NoError(t, err)
	require.False(t, answered)

	_, err = s.UpsertMessages(ctx, []Message{{MessageID: "om_mine", ChatID: "oc_quiet", MsgType: "text",
		SenderID: "ou_self", SenderType: "user", ContentRaw: `{"text":"好"}`, CreateMs: 6_500, MessagePosition: 6_500, RawJSON: "{}"}}, 1)
	require.NoError(t, err)
	answered, err = s.Answered(ctx, "om_fresh", "ou_self")
	require.NoError(t, err)
	require.True(t, answered)
}

func TestAnswered_CountsAReactionOnTheSourceMessage(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)
	require.NoError(t, s.UpdateReactions(ctx, "om_fresh",
		`{"details":[{"emoji_type":"OK","action_time":"1","operator":{"operator_id":"ou_self","operator_type":"user"}}]}`))

	answered, err := s.Answered(ctx, "om_fresh", "ou_self")
	require.NoError(t, err)
	require.True(t, answered, "an acknowledging reaction is how an ack is answered")

	answered, err = s.Answered(ctx, "om_old", "ou_self")
	require.NoError(t, err)
	require.False(t, answered, "the reaction answers only the message it is on")
}

func TestReminders_FireOnceDueAndASameMessageReplanReplaces(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedTriage(t, s)
	require.NoError(t, s.PutReminder(ctx, Reminder{MessageID: "om_fresh", ChatID: "oc_quiet", FireMs: 100, Title: "早会"}))
	require.NoError(t, s.PutReminder(ctx, Reminder{MessageID: "om_fresh", ChatID: "oc_quiet", FireMs: 200, Title: "早会改期"}))

	due, err := s.DueReminders(ctx, 150)
	require.NoError(t, err)
	require.Empty(t, due, "the replan moved it")
	due, err = s.DueReminders(ctx, 200)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "早会改期", due[0].Title)

	require.NoError(t, s.MarkReminderFired(ctx, "om_fresh", 201))
	due, err = s.DueReminders(ctx, 1_000)
	require.NoError(t, err)
	require.Empty(t, due)
}

func triageIDs(ts []Triage) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.MessageID)
	}
	return out
}
