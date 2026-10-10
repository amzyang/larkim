package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// seedCandidateChat puts one chat on the books with messages from two senders,
// so a candidate row has a chat to resolve against and Answered has a self to
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

	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"old"}, nil, "text", 200))
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"缓冲话术", "放行话术"}, nil, "markdown", 300))

	rows, err := s.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Len(t, rows, 2, "a re-draft replaces the drafts, it does not append")
	require.Equal(t, []string{"缓冲话术", "放行话术"}, []string{rows[0].Text, rows[1].Text})
	require.Equal(t, "markdown", rows[0].Format)
	require.Equal(t, int64(300), rows[0].CreatedMs)
}

func TestChatCandidates_OrdersOldestPendingFirstAndMarksAnswered(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"先看下"}, nil, "text", 200))
	require.NoError(t, s.PutCandidates(ctx, "om_ask2", "oc_quiet", []string{"稍后回"}, nil, "text", 100))
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
	require.True(t, rows[0].Answered, "the reader answered after this draft")
	require.Equal(t, "om_ask", rows[1].Mid)
	require.False(t, rows[1].Answered, "this draft landed after the reader had spoken")

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
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a", "b"}, nil, "text", 1))
	require.NoError(t, s.PutCandidates(ctx, "om_ask2", "oc_quiet", []string{"c"}, nil, "text", 2))
	require.NoError(t, s.PutCandidates(ctx, "om_elsewhere", "oc_other", []string{"d"}, nil, "text", 3))

	counts, err := s.CandidateChats(ctx, "ou_self")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"oc_quiet": 2, "oc_other": 1},
		counts, "one row per pending message, not per candidate")
}

func TestCandidateChats_SkipsRowsTheReaderRepliedPast(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a"}, nil, "text", 100))
	require.NoError(t, s.PutCandidates(ctx, "om_ask2", "oc_quiet", []string{"b"}, nil, "text", 300))
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

// reactedBy is a minimal reaction block naming one operator, the shape
// UpdateReactions stores.
func reactedBy(operator string) string {
	return `{"counts":[{"reaction_type":"THUMBSUP","count":"1"}],"details":[{"emoji_type":"THUMBSUP","action_time":"1","operator":{"operator_id":"` +
		operator + `","operator_type":"user"}}]}`
}

func TestChatCandidates_ExpandsTextsThenReactions(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"周四好", "我看下再回你"},
		[]string{"THUMBSUP", "OnIt"}, "text", 200))

	rows, err := s.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Equal(t, []Candidate{
		{Mid: "om_ask", ChatID: "oc_quiet", Text: "周四好", Format: "text", CreatedMs: 200},
		{Mid: "om_ask", ChatID: "oc_quiet", Text: "我看下再回你", Format: "text", CreatedMs: 200},
		{Mid: "om_ask", ChatID: "oc_quiet", Reaction: "THUMBSUP", CreatedMs: 200},
		{Mid: "om_ask", ChatID: "oc_quiet", Reaction: "OnIt", CreatedMs: 200},
	}, rows, "the replies lead, the reactions follow, each a candidate of its own")
}

func TestChatCandidates_AReactionOnlyRowYieldsNoTextCandidate(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", nil, []string{"Get"}, "text", 200))

	rows, err := s.ChatCandidates(ctx, "oc_quiet", "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "an empty draft column is no reply to offer")
	require.Equal(t, "Get", rows[0].Reaction)
	require.Empty(t, rows[0].Text)

	counts, err := s.CandidateChats(ctx, "ou_self")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"oc_quiet": 1}, counts, "a reaction-only row still draws the marker")
}

func TestChatCandidates_ASelfReactionOnTheSourceAnswersIt(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"好"}, []string{"OK"}, "text", 200))

	require.NoError(t, s.UpdateReactions(ctx, "om_ask", reactedBy("ou_b")))
	rows, err := s.ChatCandidates(ctx, "oc_quiet", "ou_self")
	require.NoError(t, err)
	require.False(t, rows[0].Answered, "somebody else's reaction answers nothing for the reader")

	require.NoError(t, s.UpdateReactions(ctx, "om_ask", reactedBy("ou_self")))
	rows, err = s.ChatCandidates(ctx, "oc_quiet", "ou_self")
	require.NoError(t, err)
	require.True(t, rows[0].Answered, "the reader reacting to the source is an answer")
	require.True(t, rows[1].Answered)
}

func TestCandidateChats_SkipsRowsTheReaderReactedTo(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"好"}, nil, "text", 200))

	require.NoError(t, s.UpdateReactions(ctx, "om_ask", reactedBy("ou_self")))
	counts, err := s.CandidateChats(ctx, "ou_self")
	require.NoError(t, err)
	require.Empty(t, counts, "a reaction the reader put on the source settles its drafts")
}

func TestClearCandidate_DropsTheRowAndBumpsTheRevision(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	seedCandidateChat(t, s)
	require.NoError(t, s.PutCandidates(ctx, "om_ask", "oc_quiet", []string{"a"}, nil, "text", 1))
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
