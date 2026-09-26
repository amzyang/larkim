package store

import (
	"cmp"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threadRow is one message of a thread, written the way ingest writes it: a
// root keeps a non-negative position, a reply a negative one.
type threadRow struct {
	id       string
	thread   string
	sender   string
	position int64
	createMs int64
	text     string
	mentions string
	deleted  bool
	unread   bool
}

// stakeStore lays the rows down in one chat and returns the store. A row sent
// by the build bot is silenced, which is the one rule the store is given.
func stakeStore(t *testing.T, chatID string, rows []threadRow) (*Store, context.Context) {
	t.Helper()
	s, ctx := openTest(t), context.Background()
	s.Silence = SilenceRules{{Sender: "cli_c"}}
	require.NoError(t, s.EnsureChat(ctx, chatID, 1))
	msgs := make([]Message, 0, len(rows))
	for _, r := range rows {
		msgs = append(msgs, Message{MessageID: r.id, ChatID: chatID, MsgType: "text", SenderID: r.sender,
			SenderType: "user", SenderName: r.sender, ThreadID: r.thread, MessagePosition: r.position,
			ContentRaw: `{"text":"` + cmp.Or(r.text, "x") + `"}`, Deleted: r.deleted,
			CreateMs: r.createMs, UpdateMs: r.createMs})
	}
	_, err := s.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)
	for _, r := range rows {
		// mentions_json belongs to the rendering pass, not to ingest.
		if r.mentions != "" || r.text != "" {
			require.NoError(t, s.UpdateRendered(ctx, r.id, cmp.Or(r.text, "x"), r.mentions, "", r.createMs))
		}
		if r.unread {
			unread := false
			require.NoError(t, s.SetReadStatus(ctx, r.id, &unread, r.createMs, 0))
		}
	}
	return s, ctx
}

func feedOf(t *testing.T, s *Store, ctx context.Context, self string) []ThreadFeed {
	t.Helper()
	feed, err := s.ListThreadFeed(ctx, ThreadFeedQuery{Self: self})
	require.NoError(t, err)
	return feed
}

func stakedIDs(t *testing.T, s *Store, ctx context.Context, self string, limit int) []string {
	t.Helper()
	threads, err := s.StakedThreads(ctx, self, limit)
	require.NoError(t, err)
	ids := make([]string, len(threads))
	for i, th := range threads {
		ids[i] = th.ThreadID
	}
	return ids
}

func TestStakedThreads_TakesThreadsTheReaderAnswered(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root_mine", thread: "omt_mine", sender: "ou_a", position: 10, createMs: 100},
		{id: "om_reply_mine", thread: "omt_mine", sender: "ou_me", position: -3, createMs: 200},
		{id: "om_root_theirs", thread: "omt_theirs", sender: "ou_a", position: 11, createMs: 300},
		{id: "om_reply_theirs", thread: "omt_theirs", sender: "ou_b", position: -3, createMs: 400},
	})

	assert.Equal(t, []string{"omt_mine"}, stakedIDs(t, s, ctx, "ou_me", 10))
}

// Starting a thread is taking a turn in it: the root is the reader's word.
func TestStakedThreads_CountsTheRootAsATurn(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_reply", thread: "omt_x", sender: "ou_a", position: -3, createMs: 200},
	})

	assert.Equal(t, []string{"omt_x"}, stakedIDs(t, s, ctx, "ou_me", 10))
}

func TestStakedThreads_TakesThreadsThatCallTheReaderByName(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_a", position: 10, createMs: 100},
		{id: "om_reply", thread: "omt_x", sender: "ou_b", position: -3, createMs: 200,
			mentions: `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`},
	})

	assert.Equal(t, []string{"omt_x"}, stakedIDs(t, s, ctx, "ou_me", 10))
}

// One open id must not answer for another that merely starts with it.
func TestStakedThreads_DoesNotMatchAnIdPrefix(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_a", position: 10, createMs: 100},
		{id: "om_reply", thread: "omt_x", sender: "ou_b", position: -3, createMs: 200,
			mentions: `[{"id":"ou_meeting","key":"@_user_1","name":"会议助手"}]`},
	})

	assert.Empty(t, stakedIDs(t, s, ctx, "ou_me", 10))
}

// Freshest first, and the quiet ones fall out of a bounded window on their own.
func TestStakedThreads_TakesTheFreshestWithinTheBound(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_old_root", thread: "omt_old", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_old_reply", thread: "omt_old", sender: "ou_a", position: -3, createMs: 150},
		{id: "om_new_root", thread: "omt_new", sender: "ou_me", position: 11, createMs: 200},
		{id: "om_new_reply", thread: "omt_new", sender: "ou_a", position: -3, createMs: 900},
	})

	assert.Equal(t, []string{"omt_new", "omt_old"}, stakedIDs(t, s, ctx, "ou_me", 10))
	assert.Equal(t, []string{"omt_new"}, stakedIDs(t, s, ctx, "ou_me", 1))
}

// A chat whose listing Feishu refused would refuse its threads too.
func TestStakedThreads_LeavesOutARefusedChat(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_reply", thread: "omt_x", sender: "ou_a", position: -3, createMs: 200},
	})
	require.NoError(t, s.SetChatSyncError(ctx, "oc_group", "230002: restricted", 300))

	assert.Empty(t, stakedIDs(t, s, ctx, "ou_me", 10))
}

// An empty reader has a stake in nothing rather than in everything.
func TestStakedThreads_AnswersNothingWithoutAReader(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_a", position: 10, createMs: 100},
		{id: "om_reply", thread: "omt_x", sender: "ou_b", position: -3, createMs: 200},
	})

	assert.Empty(t, stakedIDs(t, s, ctx, "", 10))
}

// The row is titled by the root and stands where its newest reply put it.
func TestListThreadFeed_TakesTheRootAndTheNewestReply(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_a", position: 10, createMs: 100, text: "release plan"},
		{id: "om_r1", thread: "omt_x", sender: "ou_me", position: -3, createMs: 200, text: "on it"},
		{id: "om_r2", thread: "omt_x", sender: "ou_b", position: -3, createMs: 300, text: "thanks"},
	})

	feed := feedOf(t, s, ctx, "ou_me")
	require.Len(t, feed, 1)
	assert.Equal(t, "omt_x", feed[0].ThreadID)
	assert.Equal(t, "oc_group", feed[0].ChatID)
	assert.Equal(t, "om_root", feed[0].Root.MessageID)
	assert.Equal(t, "release plan", feed[0].Root.Content)
	assert.Equal(t, "om_r2", feed[0].Last.MessageID)
	assert.Equal(t, int64(300), feed[0].Last.CreateMs)
}

func TestListThreadFeed_LeavesOutAThreadWithoutAStake(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_a", position: 10, createMs: 100},
		{id: "om_r1", thread: "omt_x", sender: "ou_b", position: -3, createMs: 200},
	})

	assert.Empty(t, feedOf(t, s, ctx, "ou_me"))
}

// A root nobody answered is the newest thing in its thread, and the chat's
// own row already says that much.
func TestListThreadFeed_LeavesOutARootWithNoReply(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_me", position: 10, createMs: 100},
	})

	assert.Empty(t, feedOf(t, s, ctx, "ou_me"))
}

func TestListThreadFeed_CountsTheUnreadRepliesAndTheNameCalledOnThem(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_read", thread: "omt_x", sender: "ou_a", position: -3, createMs: 200},
		{id: "om_new", thread: "omt_x", sender: "ou_a", position: -3, createMs: 300, unread: true,
			text: "@林岚 看下", mentions: `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`},
	})

	feed := feedOf(t, s, ctx, "ou_me")
	require.Len(t, feed, 1)
	assert.Equal(t, int64(1), feed[0].Unread, "only the reply with a read_state row still unread")
	assert.True(t, feed[0].NamesSelf)
}

// A recall keeps its place and says so, the way a chat's summary line does.
func TestListThreadFeed_KeepsARecalledLastReply(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_r1", thread: "omt_x", sender: "ou_a", position: -3, createMs: 200},
		{id: "om_gone", thread: "omt_x", sender: "ou_a", position: -3, createMs: 300, deleted: true},
	})

	feed := feedOf(t, s, ctx, "ou_me")
	require.Len(t, feed, 1)
	assert.Equal(t, "om_gone", feed[0].Last.MessageID)
	assert.True(t, feed[0].Last.Deleted)
}

// Silence asks not to be pulled, and a row moving to the top is that pull.
func TestListThreadFeed_LeavesSilencedRepliesOutOfTheWindow(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_r1", thread: "omt_x", sender: "ou_a", position: -3, createMs: 200, text: "real answer"},
		{id: "om_noise", thread: "omt_x", sender: "cli_c", position: -3, createMs: 300, unread: true},
	})

	feed := feedOf(t, s, ctx, "ou_me")
	require.Len(t, feed, 1)
	assert.Equal(t, "om_r1", feed[0].Last.MessageID)
	assert.Zero(t, feed[0].Unread)
}

func TestListThreadFeed_OrdersByNewestReply(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root_a", thread: "omt_a", sender: "ou_me", position: 10, createMs: 100},
		{id: "om_reply_a", thread: "omt_a", sender: "ou_a", position: -3, createMs: 900},
		{id: "om_root_b", thread: "omt_b", sender: "ou_me", position: 11, createMs: 200},
		{id: "om_reply_b", thread: "omt_b", sender: "ou_a", position: -3, createMs: 400},
	})

	feed := feedOf(t, s, ctx, "ou_me")
	require.Len(t, feed, 2)
	assert.Equal(t, "omt_a", feed[0].ThreadID)
	assert.Equal(t, "omt_b", feed[1].ThreadID)
}

func TestListThreadFeed_AnswersNothingWithoutAReader(t *testing.T) {
	s, ctx := stakeStore(t, "oc_group", []threadRow{
		{id: "om_root", thread: "omt_x", sender: "ou_a", position: 10, createMs: 100},
		{id: "om_r1", thread: "omt_x", sender: "ou_b", position: -3, createMs: 200},
	})

	assert.Empty(t, feedOf(t, s, ctx, ""))
}

// The listing has to cost what it lists. Walking messages rather than the
// thread index makes it grow with the whole synced history, and the rows come
// out the same either way, so the plan is the only place this shows.
func TestListThreadFeed_WalksTheThreadIndex(t *testing.T) {
	s := openTest(t)

	plan := explainPlan(t, s, threadFeedQuery, "ou_me", "ou_me", "ou_me", 200)

	assert.Contains(t, plan, "messages_thread")
}
