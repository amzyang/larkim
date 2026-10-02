package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func candMsgModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	m := pickerModel(t)
	st := m.deps.Store
	ctx := t.Context()
	require.NoError(t, st.PutCandidates(ctx, "om_a", "oc_team", []string{"缓冲话术", "放行话术"}, "text", 100))
	m.chatCands, _ = st.ChatCandidates(ctx, "oc_team", "ou_me")
	return m, st
}

func TestCandidateRows_DrawUnderReactions(t *testing.T) {
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
	m, _ := candMsgModel(t)
	m.chatCands[0].Replied = true
	require.Len(t, m.visibleCandidates(m.chatCands), 1)
}
