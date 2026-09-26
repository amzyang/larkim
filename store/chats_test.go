package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func chatIDs(chats []Chat) []string {
	out := make([]string, len(chats))
	for i, c := range chats {
		out[i] = c.ChatID
	}
	return out
}

func markUnread(t *testing.T, s *Store, messageID string) {
	t.Helper()
	unread := false
	require.NoError(t, s.SetReadStatus(context.Background(), messageID, &unread, 100, 0))
}

func TestListChats_LeavesAChatWhereAThreadReplyLandsIt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_recent", "oc_thread"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	reply := msgAt("om_reply", "oc_thread", 900, -3, "someone answered an old topic")
	reply.ThreadID = "omt_1"
	root := msgAt("om_root", "oc_thread", 200, 1, "root")
	root.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_recent", "oc_recent", 800, 1, "hello"),
		root, reply,
	}, 1)
	require.NoError(t, err)

	chats, err := s.ListChats(ctx, ChatQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"oc_recent", "oc_thread"}, chatIDs(chats),
		"a thread reply does not pull its chat past a newer main-flow message")
}

func TestListChats_ThreadRootCountsAsMainFlow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_topic", 1))
	root := msgAt("om_root", "oc_topic", 100, 1, "a new topic")
	root.ThreadID = "omt_1"
	reply := msgAt("om_reply", "oc_topic", 400, -1, "an answer")
	reply.ThreadID = "omt_1"
	_, err := s.UpsertMessages(ctx, []Message{root, reply}, 1)
	require.NoError(t, err)

	c, err := s.GetChat(ctx, "oc_topic")
	require.NoError(t, err)
	require.Equal(t, int64(100), c.LastUnsilencedMs, "opening a thread is a main-flow event, answering it is not")
	require.Equal(t, "om_root", c.LastMessageID)
}

func TestListChats_KeepsAChatInPlaceWhenItIsRead(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_recent", "oc_stale"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_recent", "oc_recent", 800, 1, "read"),
		msgAt("om_stale", "oc_stale", 100, 1, "unread"),
	}, 1)
	require.NoError(t, err)
	markUnread(t, s, "om_stale")
	before := chatIDs(listChats(t, s))

	require.NoError(t, s.MarkChatRead(ctx, "oc_stale", 5000))

	require.Equal(t, before, chatIDs(listChats(t, s)),
		"reading a chat is not news about the chat, so it does not move")
}

func TestListChats_TiebreaksOnChatID(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"oc_c", "oc_a", "oc_b"} {
		require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: id, Name: "平台组"}}, 1))
	}

	first := chatIDs(listChats(t, s))
	require.Equal(t, []string{"oc_a", "oc_b", "oc_c"}, first,
		"same name and no messages still yields one order, not an arbitrary one")
	require.Equal(t, first, chatIDs(listChats(t, s)), "and the same order on every read")
}

func TestListChats_CarriesTheUnreadCount(t *testing.T) {
	s := openTest(t)
	s.Silence = SilenceRules{{Sender: "cli_c"}}
	ctx := context.Background()
	require.NoError(t, s.EnsureChat(ctx, "oc_loud", 1))
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_1", "oc_loud", 100, 1, "did anyone look at it"),
		msgAt("om_2", "oc_loud", 200, 1, "bumping this"),
		msgAt("om_3", "oc_loud", 300, -1, "in a thread"),
		fromBot("om_4", "oc_loud", 400, "nightly build #418 passed"),
	}, 1)
	require.NoError(t, err)
	for _, id := range []string{"om_1", "om_2", "om_3", "om_4"} {
		markUnread(t, s, id)
	}

	chats := listChats(t, s)
	require.Len(t, chats, 1)
	require.Equal(t, int64(2), chats[0].UnreadCount,
		"the badge counts unread main-flow messages, thread replies and silenced ones left out")

	require.NoError(t, s.MarkChatRead(ctx, "oc_loud", 5000))
	require.Equal(t, int64(0), listChats(t, s)[0].UnreadCount)
}

// unreadCounts is the badge of every chat that carries one, the shape the
// sidebar builds from ListChats.
func unreadCounts(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, c := range listChats(t, s) {
		if c.UnreadCount > 0 {
			out[c.ChatID] = c.UnreadCount
		}
	}
	return out
}

func listChats(t *testing.T, s *Store) []Chat {
	t.Helper()
	chats, err := s.ListChats(context.Background(), ChatQuery{})
	require.NoError(t, err)
	return chats
}

func TestChat_AvatarSeedIsThePeerForP2P(t *testing.T) {
	p2p := Chat{ChatID: "oc_quiet", ChatMode: "p2p", P2PTargetID: "ou_a"}
	require.Equal(t, "ou_a", p2p.AvatarSeed(),
		"the chat list and the message pane draw the same peer, so both seed from the peer")
	require.Equal(t, "oc_quiet", Chat{ChatID: "oc_quiet", ChatMode: "p2p"}.AvatarSeed(),
		"a peer the contact sync has not resolved leaves the chat as the only identity")
	require.Equal(t, "oc_quiet", Chat{ChatID: "oc_quiet", ChatMode: "group", P2PTargetID: "ou_a"}.AvatarSeed())
}

// explainPlan is the query plan SQLite chooses for q, one step per line.
func explainPlan(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	scanDetail := func(sc scanner) (string, error) {
		var id, parent, notused int64
		var detail string
		return detail, sc.Scan(&id, &parent, &notused, &detail)
	}
	steps, err := queryAll(t.Context(), s.db, scanDetail, `EXPLAIN QUERY PLAN `+q, args...)
	require.NoError(t, err)
	return strings.Join(steps, "\n")
}

// The badge has to cost what it counts. Driving the aggregate from messages
// makes it grow with the whole synced history, which is what the
// read_state_unread index exists to prevent; asserting the plan is the only
// thing that holds that, since the counts come out the same either way.
func TestListChats_UnreadAggregateDrivesFromReadState(t *testing.T) {
	s := openTest(t)

	plan := explainPlan(t, s, fmt.Sprintf(unreadAggregate, atMeExpr), "ou_me")

	require.Contains(t, plan, "read_state_unread", "the unread set is what the aggregate walks")
	require.NotContains(t, plan, "SCAN m", "and messages is probed by id, never scanned")
}

func TestMessageCountsByChat_CountsOnlyChatsThatHaveMessages(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	for _, id := range []string{"oc_quiet", "oc_busy", "oc_empty"} {
		require.NoError(t, s.EnsureChat(ctx, id, 1))
	}
	_, err := s.UpsertMessages(ctx, []Message{
		msgAt("om_a", "oc_busy", 100, 1, "一"),
		msgAt("om_b", "oc_busy", 200, 1, "二"),
		msgAt("om_c", "oc_busy", 300, -1, "in a thread"),
		msgAt("om_d", "oc_quiet", 400, 1, "单独一条"),
	}, 1)
	require.NoError(t, err)

	counts, err := s.MessageCountsByChat(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"oc_busy": 3, "oc_quiet": 1}, counts,
		"every stored message counts, thread replies included; a chat with none is absent")
}

func TestListChats_DoesNotCountMessagesPerRow(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	require.NoError(t, s.EnsureChat(ctx, "oc_busy", 1))
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_a", "oc_busy", 100, 1, "hello")}, 1)
	require.NoError(t, err)

	require.Equal(t, int64(0), listChats(t, s)[0].MessageCount,
		"a listing leaves the count to MessageCountsByChat")
}

func TestChats_HistoryFloorRecordsHowFarBackAChatReaches(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertChats(ctx, []Chat{{ChatID: "oc_a", Name: "Alpha", ChatMode: "p2p"}}, 100))

	// Until backfill has run the floor says nothing, which is what the zero
	// backfill_done_at rather than the zero floor is there to tell.
	c, err := s.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Zero(t, c.BackfillDoneAt)
	require.Zero(t, c.HistoryFloorMs)

	require.NoError(t, s.SetChatBackfillDone(ctx, "oc_a", 5000, 9000))
	c, _ = s.GetChat(ctx, "oc_a")
	require.EqualValues(t, 9000, c.BackfillDoneAt)
	require.EqualValues(t, 5000, c.HistoryFloorMs, "the floor is where the initial pull reached, not when it ran")

	require.NoError(t, s.SetChatHistoryFloor(ctx, "oc_a", 1000))
	c, _ = s.GetChat(ctx, "oc_a")
	require.EqualValues(t, 1000, c.HistoryFloorMs)

	require.NoError(t, s.SetChatHistoryFloor(ctx, "oc_a", 0))
	c, _ = s.GetChat(ctx, "oc_a")
	require.Zero(t, c.HistoryFloorMs, "0 under a set backfill_done_at means the whole chat is stored")
}
