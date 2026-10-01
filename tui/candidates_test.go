package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// candModel is a chat open with two of its messages carrying pending
// lark-watch drafts: three candidates on the older one, one on the newer.
func candModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	m := pickerModel(t)
	st := m.deps.Store
	ctx := t.Context()
	require.NoError(t, st.EnsureChat(ctx, "oc_elsewhere", 1))
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: "om_b", ChatID: "oc_team",
		MsgType: "text", SenderID: "ou_a", SenderName: "李四",
		ContentRaw: `{"text":"接口什么时候好"}`, CreateMs: 200, UpdateMs: 200}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.PutCandidates(ctx, "om_a", "oc_team", []string{"缓冲话术", "放行话术", "拦下话术"}, "text", 100))
	require.NoError(t, st.PutCandidates(ctx, "om_b", "oc_team", []string{"已排期，周四发"}, "markdown", 200))
	return m, st
}

func TestOpenCandidates_ListsEveryPendingDraftOldestFirst(t *testing.T) {
	m, _ := candModel(t)

	next, cmd := m.openCandidates()
	m = next.(Model)

	loaded := cmd()
	mm, _ := m.update(loaded.(candidatesLoadedMsg))
	m = mm.(Model)
	require.Equal(t, modeCandidates, m.mode)
	require.Len(t, m.cand.rows, 4)
	require.Equal(t, "om_a", m.cand.rows[0].Mid)
	require.Equal(t, "缓冲话术", m.cand.rows[0].Text)
	require.Equal(t, "om_b", m.cand.rows[3].Mid)
	require.Equal(t, "markdown", m.cand.rows[3].Format)
}

func TestOpenCandidates_TellsAChatWithNothingPending(t *testing.T) {
	m := pickerModel(t)

	next, cmd := m.openCandidates()
	m = next.(Model)
	loaded := cmd()
	mm, _ := m.update(loaded)
	m = mm.(Model)
	require.Equal(t, modeNormal, m.mode, "the notice is the whole answer; no empty picker opens")
}

func TestChooseCandidate_FillsTheComposerAndRemembersTheMid(t *testing.T) {
	m, _ := candModel(t)
	next, cmd := m.openCandidates()
	m = next.(Model)
	loaded := cmd()
	mm, _ := m.update(loaded)
	m = mm.(Model)

	mm, _ = m.onCandidatesKey(keyMsg("j"))
	mm, _ = mm.(Model).onCandidatesKey(keyMsg("enter"))
	m = mm.(Model)
	require.Equal(t, modeNormal, m.mode)
	require.Equal(t, "放行话术", m.input.Value())
	require.Equal(t, "om_a", m.candFilled, "the send this seeds has to know which row to clear")
}

func TestChooseCandidate_LeavesTheComposerAloneOnEsc(t *testing.T) {
	m, _ := candModel(t)
	next, cmd := m.openCandidates()
	m = next.(Model)
	loaded := cmd()
	mm, _ := m.update(loaded)
	m = mm.(Model)

	mm, _ = m.onCandidatesKey(keyMsg("esc"))
	m = mm.(Model)
	require.Equal(t, modeNormal, m.mode)
	require.Empty(t, m.input.Value())
	require.Empty(t, m.candFilled)
}

func TestCandidates_KeyAndCommandOpenTheSamePicker(t *testing.T) {
	m, _ := candModel(t)
	next, _ := m.onNormalKey("C")
	require.Equal(t, modeNormal, next.(Model).mode, "C only asks; the picker opens on the store's answer")

	next, _ = m.runCommand("candidates")
	require.NotNil(t, next)
}

func TestChatRow_DrawsTheCandidateMark(t *testing.T) {
	c := store.Chat{ChatID: "oc_team", Name: "平台组", ChatMode: "group", LastMessageMs: 100}
	with := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, 2,
		gistOf(listRow{chat: c}, "ou_me", emojiPics{}), testNow, 40, nil)
	without := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, 0,
		gistOf(listRow{chat: c}, "ou_me", emojiPics{}), testNow, 40, nil)
	require.Contains(t, with.bottom, candGlyph)
	require.NotContains(t, without.bottom, candGlyph)
}

func TestIngestedMsg_ClearsTheSeededCandidateRow(t *testing.T) {
	m, st := candModel(t)
	m.candFilled = "om_b"
	require.NoError(t, st.PutCandidates(t.Context(), "om_keep", "oc_elsewhere", []string{"x"}, "text", 1))

	mm, _ := m.update(ingestedMsg{localID: "local"})
	m = mm.(Model)
	require.Empty(t, m.candFilled)
	// The clear runs on its own schedule; the row it names is what has to go.
	clearCandidate(m.deps, "om_b")()
	rows, err := st.ChatCandidates(t.Context(), "oc_team", "")
	require.NoError(t, err)
	for _, c := range rows {
		require.NotEqual(t, "om_b", c.Mid, "the seeded row is the send's to clear")
	}
	require.NotEmpty(t, rows, "another mid's candidates are not the send's to clear")
	kept, err := st.ChatCandidates(t.Context(), "oc_elsewhere", "")
	require.NoError(t, err)
	require.Len(t, kept, 1, "another chat's mirror is not the send's to clear")
}
