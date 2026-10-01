package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettlePlan_SettlesTheFullySilencedTail(t *testing.T) {
	pos, push := SettlePlan([]UnreadMsg{{Position: 1, Silenced: true}, {Position: 2, Silenced: true}})
	require.True(t, push)
	require.EqualValues(t, 2, pos, "nothing unsilenced is left to keep unread")
}

func TestSettlePlan_StopsBelowTheFirstUnsilenced(t *testing.T) {
	pos, push := SettlePlan([]UnreadMsg{
		{Position: 1, Silenced: true},
		{Position: 2, Silenced: true},
		{Position: 3},
		{Position: 4, Silenced: true},
	})
	require.True(t, push)
	require.EqualValues(t, 2, pos, "the watermark cannot cross the unsilenced message")
}

func TestSettlePlan_DoesNothingWhenTheFirstUnreadIsUnsilenced(t *testing.T) {
	pos, push := SettlePlan([]UnreadMsg{{Position: 1}, {Position: 2, Silenced: true}})
	require.False(t, push)
	require.Zero(t, pos)
}

func TestSettlePlan_AnswersFalseForNothingUnread(t *testing.T) {
	pos, push := SettlePlan(nil)
	require.False(t, push)
	require.Zero(t, pos)
}

// queueOf answers the queue as PendingSilenceSettle plans it.
func queueOf(t *testing.T, s *Store, limit int) []SilenceSettle {
	t.Helper()
	q, err := s.PendingSilenceSettle(t.Context(), limit)
	require.NoError(t, err)
	return q
}

// botAt is the noisy sender's message at an explicit position, for the plans
// that read the order positions make.
func botAt(id string, position int64, text string) Message {
	m := msgAt(id, "oc_quiet", position, position, text)
	m.SenderID, m.SenderType, m.SenderName = "cli_c", "app", "Build bot"
	return m
}

// humanAt is somebody else's message, so arrival marking counts it unread.
func humanAt(id string, position int64, text string) Message {
	m := msgAt(id, "oc_quiet", position, position, text)
	m.SenderID, m.SenderName = "ou_b", "Bob"
	return m
}

func TestUpsertMessages_QueuesAChatWhoseArrivalWasSilenced(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := t.Context()
	_, err := s.UpsertMessagesArriving(ctx,
		[]Message{botAt("om_noise", 1, "nightly build #418 passed")},
		1, Arrival{Self: "ou_a", SinceMs: 0})
	require.NoError(t, err)

	q := queueOf(t, s, 10)
	require.Len(t, q, 1)
	require.Equal(t, "oc_quiet", q[0].ChatID)
	require.True(t, q[0].Push, "the flag flip and the queue row land together")
	require.EqualValues(t, 1, q[0].Position, "the tail is fully silenced")
}

func TestUpsertMessages_DoesNotQueueAReadSilencedMessage(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{botAt("om_read", 1, "nightly build #418 passed")}, 1)
	require.NoError(t, err)
	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_read", &read, 100, 0))
	// The render path is the one that flips a card's flag late; it must not
	// queue a message Feishu already answered as read.
	require.NoError(t, s.UpdateRendered(ctx, "om_read", "nightly build #418 passed", "", 200))

	require.Empty(t, queueOf(t, s, 10), "the badge-clear path owns a read chat, not the settle")
}

func TestReapplySilence_QueuesChatsAcrossARuleChange(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessagesArriving(ctx,
		[]Message{botAt("om_noise", 1, "nightly build #418 passed")},
		1, Arrival{Self: "ou_a", SinceMs: 0})
	require.NoError(t, err)
	require.Empty(t, queueOf(t, s, 10))

	s.Silence = SilenceRules{{Sender: "cli_c"}}
	_, err = s.ReapplySilence(ctx)
	require.NoError(t, err)

	q := queueOf(t, s, 10)
	require.Len(t, q, 1, "a rebuilt rule set queues chats no listing will touch again")
	require.Equal(t, "oc_quiet", q[0].ChatID)
}

func TestPendingSilenceSettle_AnswersTheWatermarkBelowTheFirstUnsilenced(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := t.Context()
	_, err := s.UpsertMessagesArriving(ctx, []Message{
		botAt("om_a", 1, "nightly build #418"),
		humanAt("om_human", 2, "did anyone look at it"),
		botAt("om_c", 3, "nightly build #419"),
	}, 1, Arrival{Self: "ou_a", SinceMs: 0})
	require.NoError(t, err)
	require.Len(t, queueOf(t, s, 10), 1)

	q := queueOf(t, s, 10)
	require.True(t, q[0].Push)
	require.EqualValues(t, 1, q[0].Position,
		"the settle stops below the human message, which keeps its dot")
}

func TestPendingSilenceSettle_PlansNoActionForAChatReadElsewhere(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := t.Context()
	_, err := s.UpsertMessagesArriving(ctx,
		[]Message{botAt("om_noise", 1, "nightly build #418 passed")},
		1, Arrival{Self: "ou_a", SinceMs: 0})
	require.NoError(t, err)
	read := true
	require.NoError(t, s.SetReadStatus(ctx, "om_noise", &read, 100, 0))

	q := queueOf(t, s, 10)
	require.Len(t, q, 1)
	require.False(t, q[0].Push, "the plan re-derives from the store, not from what queued the row")
}

func TestSilenceSettleDone_DropsTheRowAndRemembersTheWatermark(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Chat: "oc_quiet", Sender: "cli_c"}}
	ctx := t.Context()
	_, err := s.db.ExecContext(ctx, `INSERT INTO silence_settle_queue (chat_id) VALUES ('oc_quiet')`)
	require.NoError(t, err)
	_, err = s.UpsertMessagesArriving(ctx,
		[]Message{botAt("om_noise", 1, "nightly build #418 passed"), botAt("om_more", 2, "nightly build #419")},
		1, Arrival{Self: "ou_a", SinceMs: 0})
	require.NoError(t, err)
	require.NoError(t, s.EnsureChat(ctx, "oc_quiet", 1))

	q := queueOf(t, s, 10)
	require.Len(t, q, 1, "the upsert queued the chat when the flags flipped")
	require.NoError(t, s.SilenceSettleDone(ctx, "oc_quiet", 1))
	require.Empty(t, queueOf(t, s, 10), "the settled watermark floors the plan even though the row came back")
	var settled int64
	require.NoError(t, s.db.QueryRowContext(ctx,
		`SELECT silence_settled_pos FROM chats WHERE chat_id = 'oc_quiet'`).Scan(&settled))
	require.EqualValues(t, 1, settled)

	// A re-render of the covered message never re-queues it; one above the
	// watermark does, and its plan settles there.
	require.NoError(t, s.UpdateRendered(ctx, "om_noise", "nightly build #418 again", "", 2))
	require.Empty(t, queueOf(t, s, 10), "the covered message stays covered")
	require.NoError(t, s.UpdateRendered(ctx, "om_more", "nightly build #419 again", "", 3))
	q = queueOf(t, s, 10)
	require.Len(t, q, 1)
	require.True(t, q[0].Push)
	require.EqualValues(t, 2, q[0].Position)
	require.NoError(t, s.SilenceSettleDone(ctx, "oc_quiet", 2))
	require.NoError(t, s.db.QueryRowContext(ctx,
		`SELECT silence_settled_pos FROM chats WHERE chat_id = 'oc_quiet'`).Scan(&settled))
	require.EqualValues(t, 2, settled, "a settle never moves the watermark down")
}

func TestSilenceSettleFailed_RetiresAChatAfterTenAttempts(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.db.ExecContext(ctx, `INSERT INTO silence_settle_queue (chat_id) VALUES ('oc_quiet')`)
	require.NoError(t, err)

	for range silenceSettleMaxAttempts - 1 {
		require.NoError(t, s.SilenceSettleFailed(ctx, "oc_quiet"))
		require.Len(t, queueOf(t, s, 10), 1)
	}
	require.NoError(t, s.SilenceSettleFailed(ctx, "oc_quiet"))
	require.Empty(t, queueOf(t, s, 10), "a chat the gateway keeps refusing cannot hold the queue")
}
