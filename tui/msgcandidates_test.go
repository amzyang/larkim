package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func candMsgModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	m := unreactedModel(t)
	st := m.deps.Store
	ctx := t.Context()
	require.NoError(t, st.PutCandidates(ctx, "om_a", "oc_team", []string{"缓冲话术", "放行话术"}, nil, "text", 100))
	m.chatCands, _ = st.ChatCandidates(ctx, "oc_team", "ou_me")
	return m, st
}

// unreactedModel is pickerModel with the reader's own THUMBSUP taken off om_a:
// a reaction of the reader's on the source answers its drafts, and these
// tests are about drafts still owed.
func unreactedModel(t *testing.T) Model {
	t.Helper()
	m := pickerModel(t)
	require.NoError(t, m.deps.Store.UpdateReactions(t.Context(), "om_a", `{}`))
	m.msgsBase, _ = m.deps.Store.ListMessages(t.Context(), store.MessageQuery{ChatID: "oc_team", Limit: 10})
	m.applyOutbox()
	m.layout()
	return m
}

// reactCandModel offers a reply and two reactions on om_a.
func reactCandModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	m := unreactedModel(t)
	st := m.deps.Store
	require.NoError(t, st.PutCandidates(t.Context(), "om_a", "oc_team", []string{"收到"}, []string{"THUMBSUP", "Get"}, "text", 100))
	m.chatCands, _ = st.ChatCandidates(t.Context(), "oc_team", "ou_me")
	m.rebuildMessages()
	return m, st
}

func candWith(t *testing.T, m Model, reaction string) store.Candidate {
	t.Helper()
	i := slices.IndexFunc(m.chatCands, func(c store.Candidate) bool { return c.Reaction == reaction })
	require.GreaterOrEqual(t, i, 0, "no %s candidate", reaction)
	return m.chatCands[i]
}

func TestCandidateRows_DrawAReactionByItsEmoji(t *testing.T) {
	t.Parallel()
	m, _ := reactCandModel(t)
	st := baseStyle()
	st.candidates = m.candidatesForStyle()
	out := ansi.Strip(rowText(renderRows(m.msgs, st)))
	require.Less(t, strings.Index(out, "收到"), strings.Index(out, "[Like]"), "the reply leads, the reactions follow")
	require.Contains(t, out, "[GotIt]")
}

func TestCandidateRows_SendOnAReactionReactsToTheSource(t *testing.T) {
	t.Parallel()
	m, st := reactCandModel(t)
	m, cmd := m.sendInlineCandidate(candWith(t, m, "Get"), m.msgs[0])
	require.NotNil(t, cmd)
	require.Empty(t, m.outbox, "a reaction is no message")
	require.Len(t, m.reacts, 1)
	p := m.reacts[0]
	require.Equal(t, "om_a", p.messageID)
	require.Equal(t, "Get", p.emojiType)
	require.True(t, p.on)
	require.Equal(t, "om_a", p.candMid)
	require.NotEmpty(t, m.candidatesForStyle()["om_a"], "the drafts stay until Feishu takes the reaction")

	mm, cmd := m.update(reactedMsg{p: p})
	m = mm.(Model)
	require.NotNil(t, cmd)
	require.Empty(t, m.candidatesForStyle()["om_a"], "a reaction Feishu took answers the message")
	clearCandidate(m.deps, "om_a")()
	rows, err := st.ChatCandidates(t.Context(), "oc_team", "")
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestCandidateRows_AFailedReactionKeepsTheRow(t *testing.T) {
	t.Parallel()
	m, _ := reactCandModel(t)
	m, _ = m.sendInlineCandidate(candWith(t, m, "Get"), m.msgs[0])
	mm, _ := m.update(reactedMsg{p: m.reacts[0], err: errors.New("no network")})
	m = mm.(Model)
	require.Len(t, m.candidatesForStyle()["om_a"], 3, "a refused press loses no suggestion")
	require.Contains(t, m.notice, "no network")
}

func TestCandidateRows_AReactionAlreadyMineIsNotTakenBack(t *testing.T) {
	t.Parallel()
	m, _ := reactCandModel(t)
	c := candWith(t, m, "THUMBSUP")
	require.NoError(t, m.deps.Store.UpdateReactions(t.Context(), "om_a",
		`{"counts":[{"reaction_type":"THUMBSUP","count":"1"}],"details":[{"emoji_type":"THUMBSUP","operator":{"operator_id":"ou_me"}}]}`))
	m.msgsBase, _ = m.deps.Store.ListMessages(t.Context(), store.MessageQuery{ChatID: "oc_team", Limit: 10})
	m.applyOutbox()

	m, cmd := m.sendInlineCandidate(c, m.msgs[0])
	require.Nil(t, cmd)
	require.Empty(t, m.reacts, "a candidate never takes a reaction back")
	require.Contains(t, m.notice, "already")
}

func TestCandidateRows_DrawUnderReactions(t *testing.T) {
	t.Parallel()
	m, _ := candMsgModel(t)
	msgs := []store.Message{{MessageID: "om_a", ChatID: "oc_team", SenderID: "ou_a", SenderName: "李四",
		Content: "接口什么时候好", CreateMs: msgAt(23, 9, 0), RenderedAt: 1,
		ReactionsJSON: twoReactions}}
	st := baseStyle()
	st.candidates = m.candidatesForStyle()
	out := ansi.Strip(rowText(renderRows(msgs, st)))
	iBody := strings.Index(out, "接口什么时候好")
	iReact := strings.Index(out, "Like")
	iCand := strings.Index(out, "缓冲话术")
	require.Less(t, iBody, iReact, "body before reactions")
	require.Less(t, iReact, iCand, "reactions before draft rows")
	require.Contains(t, out, "放行话术")
}

func TestCandidateRows_IgnoreHidesDraft(t *testing.T) {
	t.Parallel()
	m, st := candMsgModel(t)
	c0 := m.chatCands[0]
	m, cmd := m.ignoreInlineCandidate(c0)
	require.Nil(t, cmd, "one draft left: mirror stays until all are dismissed")
	stStyle := baseStyle()
	stStyle.candidates = m.candidatesForStyle()
	out := rowText(renderRows(m.msgs, stStyle))
	require.NotContains(t, out, "缓冲话术")
	require.Contains(t, out, "放行话术")
	c1 := m.chatCands[1]
	m, cmd = m.ignoreInlineCandidate(c1)
	require.NotNil(t, cmd)
	clearCandidate(m.deps, c0.Mid)()
	rows, err := st.ChatCandidates(t.Context(), "oc_team", "")
	require.NoError(t, err)
	for _, r := range rows {
		require.NotEqual(t, "om_a", r.Mid)
	}
}

func TestCandidateRows_SendPostsReply(t *testing.T) {
	t.Parallel()
	m, st := candMsgModel(t)
	c := m.chatCands[1]
	msg := m.msgs[0]
	m, cmd := m.sendInlineCandidate(c, msg)
	require.NotNil(t, cmd)
	require.Len(t, m.outbox, 1)
	require.Equal(t, "om_a", m.outbox[0].replyTo)
	require.Equal(t, "放行话术", strings.TrimSpace(m.outbox[0].body))
	require.Equal(t, "om_a", m.candFilled)
	require.Empty(t, m.candidatesForStyle()["om_a"])
	mm, _ := m.update(sentMsg{localID: m.outbox[0].localID, messageID: "om_sent_1"})
	m = mm.(Model)
	require.Empty(t, m.candFilled)
	clearCandidate(m.deps, "om_a")()
	rows, err := st.ChatCandidates(t.Context(), "oc_team", "")
	require.NoError(t, err)
	for _, r := range rows {
		require.NotEqual(t, "om_a", r.Mid)
	}
}

func TestVisibleCandidates_FiltersReplied(t *testing.T) {
	t.Parallel()
	m, _ := candMsgModel(t)
	m.chatCands[0].Answered = true
	require.Len(t, m.visibleCandidates(m.chatCands), 1)
}
