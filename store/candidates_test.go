package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// seedCandidateChat puts one chat on the books with messages from two senders,
// so a candidate row has a chat to resolve against and Replied has a self to
// compare with.
func seedCandidateChat(t *testing.T, s *Store) {
	t.Helper()
	require.NoError(t, s.UpsertChats(t.Context(), []Chat{
		{ChatID: "oc_quiet", Name: "平台组", ChatMode: "group"},
	}, 1))
	_, err := s.UpsertMessages(t.Context(), []Message{
		{MessageID: "om_ask", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_a",
			SenderType: "user", SenderName: "张三", ContentRaw: `{"text":"接口什么时候好"}`,
			Content: "接口什么时候好", CreateMs: 100, MessagePosition: 1, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
}

func TestPutCandidates_UpsertsTheSameMidAsAReDraft(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)

	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"old"}, "text", 200))
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"缓冲话术", "放行话术"}, "markdown", 300))

	rows, err := s.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Len(t, rows, 2, "a re-draft replaces the drafts, it does not append")
	require.Equal(t, []string{"缓冲话术", "放行话术"}, []string{rows[0].Text, rows[1].Text})
	require.Equal(t, "markdown", rows[0].Format)
	require.Equal(t, int64(300), rows[0].CreatedMs)
}

func TestChatCandidates_OrdersOldestPendingFirstAndMarksReplied(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"先看下"}, "text", 200))
	require.NoError(t, s.PutCandidates(ctx, "om_ask2", "oc_quiet", []string{"稍后回"}, "text", 100))
	// The reader answered before the newer draft landed, which is what the
	// dimmed row in the picker is for.
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_mine", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_self",
			SenderType: "user", SenderName: "林岚", ContentRaw: `{"text":"在看"}`,
			Content: "在看", CreateMs: 150, MessagePosition: 2, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	rows, err := s.ChatCandidates(ctx, "oc_quiet", "ou_self")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "om_ask2", rows[0].Mid, "the older pending sorts first")
	require.True(t, rows[0].Replied, "the reader answered after this draft")
	require.Equal(t, "om_ask", rows[1].Mid)
	require.False(t, rows[1].Replied, "this draft landed after the reader had spoken")

	all, err := s.ChatCandidates(ctx, "", "")
	require.NoError(t, err)
	require.Len(t, all, 2, "an empty chat id is every chat, for the CLI listing")
}

func TestChatCandidates_DropsLaterCandidatesWhenExtrasDoNotDecode(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	// PutCandidates writes extras as JSON; a hand-edited row is the one way
	// it stops being JSON.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO draft_candidates(mid, chat_id, draft, format, extras, created_ms)
		 VALUES('om_ask', 'oc_quiet', 'candidate 0', 'text', 'not json', 1)`)
	require.NoError(t, err)

	rows, err := s.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Equal(t, []string{"candidate 0"}, texts(rows), "the row survives; the unreadable tail does not")
}

func TestCandidateChats_CountsRowsPerChat(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a", "b"}, "text", 1))
	require.NoError(t, s.PutCandidates(ctx, "om_ask2", "oc_quiet", []string{"c"}, "text", 2))
	require.NoError(t, s.PutCandidates(ctx, "om_elsewhere", "oc_other", []string{"d"}, "text", 3))

	counts, err := s.CandidateChats(ctx, "ou_self")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"oc_quiet": 2, "oc_other": 1},
		counts, "one row per pending message, not per candidate")
}

func TestCandidateChats_SkipsRowsTheReaderRepliedPast(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a"}, "text", 100))
	require.NoError(t, s.PutCandidates(ctx, "om_ask2", "oc_quiet", []string{"b"}, "text", 300))
	// The reader spoke between the two drafts: the older one is answered, the
	// newer one still pending.
	_, err := s.UpsertMessages(ctx, []Message{
		{MessageID: "om_mine", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_self",
			SenderType: "user", SenderName: "林岚", ContentRaw: `{"text":"在看"}`,
			Content: "在看", CreateMs: 200, MessagePosition: 2, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	counts, err := s.CandidateChats(ctx, "ou_self")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"oc_quiet": 1}, counts, "only the draft the reader has not answered counts")

	_, err = s.UpsertMessages(ctx, []Message{
		{MessageID: "om_mine2", ChatID: "oc_quiet", MsgType: "text", SenderID: "ou_self",
			SenderType: "user", SenderName: "林岚", ContentRaw: `{"text":"好了"}`,
			Content: "好了", CreateMs: 400, MessagePosition: 3, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	counts, err = s.CandidateChats(ctx, "ou_self")
	require.NoError(t, err)
	require.Empty(t, counts, "a chat whose drafts are all answered past draws no marker")
}

func TestClearCandidate_DropsTheRowAndBumpsTheRevision(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a"}, "text", 1))
	before, err := s.DataRev(ctx)
	require.NoError(t, err)

	require.NoError(t, s.ClearCandidate(ctx, "om_ask"))
	rows, err := s.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Empty(t, rows)

	after, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.Greater(t, after, before,
		"the TUI learns of the clear through the revision, not by re-reading on a timer")
}

func texts(rows []Candidate) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Text
	}
	return out
}
