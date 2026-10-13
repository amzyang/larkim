package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	m.rebuildMessages()
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

// candPress presses the target the chat pane drew for c, the way a click on
// it does, or the box's dismiss button when dismiss is set.
func candPress(t *testing.T, m Model, c store.Candidate, dismiss bool) (Model, tea.Cmd) {
	t.Helper()
	for line, r := range m.msgRows {
		for _, z := range r.zones {
			if z.cand.dismiss != dismiss || z.cand.c.Mid != c.Mid || !dismiss && z.cand.c != c {
				continue
			}
			mm, cmd := m.pressZone(paneMessages, m.msgRows, line, z)
			return mm.(Model), cmd
		}
	}
	require.Fail(t, "no target drawn", "%+v dismiss=%v", c, dismiss)
	return m, nil
}

// draftBox is the stripped lines of the box drawn for om_a's drafts, its
// rounded corners included. Every line of it must be as wide as its border:
// pictures and trimmed drafts alike take the cells the border counted.
func draftBox(t *testing.T, rows []msgRow) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(rowText(rows), "\n"), "\n")
	top := slices.IndexFunc(lines, func(s string) bool { return strings.Contains(s, "╭") })
	require.GreaterOrEqual(t, top, 0, "no box drawn:\n%s", strings.Join(lines, "\n"))
	bottom := slices.IndexFunc(lines[top:], func(s string) bool { return strings.Contains(s, "╰") })
	require.Positive(t, bottom, "the box is never closed")
	box := lines[top : top+bottom+1]
	w := ansi.StringWidth(box[0])
	for _, l := range box {
		require.Equal(t, w, ansi.StringWidth(l), "every line of the box is as wide as its border: %q", l)
	}
	return box
}

func TestCandidateRows_DrawTheDraftsInsideARoundedBox(t *testing.T) {
	t.Parallel()
	m, _ := reactCandModel(t)
	st := baseStyle()
	st.candidates = m.candidatesForStyle()
	box := draftBox(t, renderRows(m.msgs, st))
	require.Len(t, box, 4, "border, the reactions, the reply, border:\n%s", strings.Join(box, "\n"))
	require.Contains(t, box[1], "[Like]")
	require.Contains(t, box[1], "[GotIt]", "every reaction shares one line")
	require.Greater(t, strings.Index(box[1], candDismissIcon), strings.Index(box[1], "[GotIt]"),
		"dismiss closes the reactions line on its right")
	require.Contains(t, box[2], "收到")
	require.NotContains(t, box[2], candDismissIcon, "one dismiss for the whole box")
	for _, l := range box {
		require.NotContains(t, l, "\uf1d8", "no send icon before a draft")
	}
}

func TestCandidateRows_DrawOneReplyPerLine(t *testing.T) {
	t.Parallel()
	m, _ := candMsgModel(t)
	st := baseStyle()
	st.candidates = m.candidatesForStyle()
	box := draftBox(t, renderRows(m.msgs, st))
	require.Len(t, box, 4)
	require.Contains(t, box[1], "缓冲话术")
	require.Contains(t, box[1], candDismissIcon, "with no reactions dismiss sits on the first reply")
	require.Contains(t, box[2], "放行话术")
	require.NotContains(t, box[2], candDismissIcon)
}

// A suggested reaction is not on the message yet, so it is drawn as an emoji
// standing in text is: no chip tint behind it, and no name after it.
func TestCandidateRows_DrawAReactionAsABarePicture(t *testing.T) {
	t.Parallel()
	m, _ := reactCandModel(t)
	dir := t.TempDir()
	writeTestEmoji(t, dir, "THUMBSUP")
	writeTestEmoji(t, dir, "Get")
	st := baseStyle()
	st.dataDir = dir
	st.place = picturesIn(dir).place
	st.candidates = m.candidatesForStyle()

	segs := candidateEmoji("THUMBSUP", st)
	require.Len(t, segs, 1, "the picture alone")
	require.Positive(t, segs[0].pic.cols)
	require.False(t, segs[0].pic.chip, "no reaction tint behind a suggestion")
	box := draftBox(t, renderRows(m.msgs, st))
	require.NotContains(t, strings.Join(box, "\n"), "Like")
}

// A narrow pane trims a long draft rather than letting it push the box's right
// edge out of line.
func TestCandidateRows_ANarrowPaneKeepsTheBoxSquare(t *testing.T) {
	t.Parallel()
	m, st := candMsgModel(t)
	require.NoError(t, st.PutCandidates(t.Context(), "om_a", "oc_team",
		[]string{strings.Repeat("很长的回复草稿", 12)}, nil, "text", 100))
	m.chatCands, _ = st.ChatCandidates(t.Context(), "oc_team", "ou_me")
	sty := baseStyle()
	sty.width = 40
	sty.candidates = m.candidatesForStyle()
	box := draftBox(t, renderRows(m.msgs, sty))
	_, border, _ := strings.Cut(box[0], "╭")
	require.LessOrEqual(t, 1+ansi.StringWidth(border), sty.inner(), "the box, its marker column aside, fits the pane")
	require.Contains(t, box[1], candDismissIcon, "the trim never costs the dismiss")
}

func TestCandidateRows_PressingAReplyPutsItInTheComposer(t *testing.T) {
	t.Parallel()
	m, _ := candMsgModel(t)
	c := m.chatCands[1]
	m, _ = candPress(t, m, c, false)
	require.Equal(t, modeInsert, m.mode)
	require.Equal(t, "放行话术", m.area().Value())
	require.NotNil(t, m.replyTo)
	require.Equal(t, "om_a", m.replyTo.MessageID, "the draft answers the message it sits under")
	require.Empty(t, m.outbox, "nothing goes until Enter")
	require.Equal(t, "om_a", m.candFilled)
	require.Len(t, m.candidatesForStyle()["om_a"], 2, "the drafts stay until the reply is sent")
}

func TestCandidateRows_PressingAReactionReactsToTheSource(t *testing.T) {
	t.Parallel()
	m, st := reactCandModel(t)
	m, cmd := candPress(t, m, candWith(t, m, "Get"), false)
	require.NotNil(t, cmd)
	require.Equal(t, modeNormal, m.mode, "a reaction has no wording to edit")
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
	m, _ = candPress(t, m, candWith(t, m, "Get"), false)
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
	m.rebuildMessages()

	m, cmd := candPress(t, m, c, false)
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

// Dismiss answers the whole box: every draft under the message goes, here and
// in the store, so the next reload cannot bring one back.
func TestCandidateRows_DismissDropsEveryDraft(t *testing.T) {
	t.Parallel()
	m, st := reactCandModel(t)
	m, cmd := candPress(t, m, m.chatCands[0], true)
	require.NotNil(t, cmd)
	require.Empty(t, m.candidatesForStyle()["om_a"])
	require.NotContains(t, rowText(m.msgRows), "╭", "no box is left behind")
	require.Empty(t, m.reacts, "dismissing reacts with nothing")
	require.Empty(t, m.outbox, "dismissing sends nothing")
	cmd()
	rows, err := st.ChatCandidates(t.Context(), "oc_team", "")
	require.NoError(t, err)
	require.Empty(t, rows)

	m.chatCands, _ = st.ChatCandidates(t.Context(), "oc_team", "ou_me")
	require.Empty(t, m.candidatesForStyle()["om_a"])
}

func TestVisibleCandidates_FiltersReplied(t *testing.T) {
	t.Parallel()
	m, _ := candMsgModel(t)
	m.chatCands[0].Answered = true
	require.Len(t, m.visibleCandidates(m.chatCands), 1)
}
