package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
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
	t.Parallel()
	m, _ := candModel(t)

	next, cmd := m.openCandidates()
	m = next.(Model)

	loaded := cmd()
	mm, _ := m.update(loaded.(candidatesLoadedMsg))
	m = mm.(Model)
	require.Equal(t, modeCandidates, m.mode)
	require.Len(t, m.cand.items, 4)
	require.Equal(t, "om_a", m.cand.items[0].Mid)
	require.Equal(t, "缓冲话术", m.cand.items[0].Text)
	require.Equal(t, "om_b", m.cand.items[3].Mid)
	require.Equal(t, "markdown", m.cand.items[3].Format)
}

func TestOpenCandidates_TellsAChatWithNothingPending(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)

	next, cmd := m.openCandidates()
	m = next.(Model)
	loaded := cmd()
	mm, _ := m.update(loaded)
	m = mm.(Model)
	require.Equal(t, modeNormal, m.mode, "the notice is the whole answer; no empty picker opens")
}

func TestChooseCandidate_FillsTheComposerAndRemembersTheMid(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	m, _ := candModel(t)
	next, _ := m.onNormalKey("C")
	require.Equal(t, modeNormal, next.(Model).mode, "C only asks; the picker opens on the store's answer")

	next, _ = m.runCommand("candidates")
	require.NotNil(t, next)
}

func TestChatRow_DrawsTheCandidateMark(t *testing.T) {
	t.Parallel()
	c := store.Chat{ChatID: "oc_team", Name: "平台组", ChatMode: "group", LastMessageMs: 100}
	with := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, 2,
		gistOf(listRow{chat: c}, "ou_me", emojiPics{}), testNow, 40, nil)
	without := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, 0,
		gistOf(listRow{chat: c}, "ou_me", emojiPics{}), testNow, 40, nil)
	require.Contains(t, with.bottom, candGlyph)
	require.NotContains(t, without.bottom, candGlyph)
}

func TestSentMsg_ClearsTheSeededCandidateRow(t *testing.T) {
	t.Parallel()
	m, st := candModel(t)
	m.candFilled = "om_b"
	require.NoError(t, st.PutCandidates(t.Context(), "om_keep", "oc_elsewhere", []string{"x"}, "text", 1))

	mm, _ := m.update(sentMsg{localID: "local"})
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

// openCand is candModel with the picker open on its four drafts.
func openCand(t *testing.T) Model {
	t.Helper()
	m, _ := candModel(t)
	next, cmd := m.openCandidates()
	mm, _ := next.(Model).update(cmd())
	return mm.(Model)
}

func TestCandidates_ADigitPicksTheRowItIsDrawnBeside(t *testing.T) {
	t.Parallel()
	mm, _ := openCand(t).onCandidatesKey(keyMsg("2"))
	m := mm.(Model)
	require.Equal(t, modeNormal, m.mode)
	require.Equal(t, "放行话术", m.input.Value())
	require.Equal(t, "om_a", m.candFilled)

	mm, _ = openCand(t).onCandidatesKey(keyMsg("4"))
	require.Equal(t, "已排期，周四发", mm.(Model).input.Value())

	// A digit past the list reaches nothing, and the picker stays open.
	mm, _ = openCand(t).onCandidatesKey(keyMsg("5"))
	require.Equal(t, modeCandidates, mm.(Model).mode)
	require.Empty(t, mm.(Model).input.Value())
}

func TestCandidates_StandOverThePaneNumbered(t *testing.T) {
	t.Parallel()
	m := openCand(t)
	segs := m.floatSegs()
	require.Len(t, segs, 4)
	require.Equal(t, "1 缓冲话术", strings.TrimRight(ansi.Strip(m.joinSegsWidth(segs[0])), " "))
	require.Equal(t, "4 已排期，周四发", strings.TrimRight(ansi.Strip(m.joinSegsWidth(segs[3])), " "))

	f, ok := m.floater()
	require.True(t, ok)
	require.Equal(t, m.bandLeft(m.side), f.x, "the list lines up with the box a pick fills")
	require.Equal(t, m.bodyHeight()+2, f.y+f.h)
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "fill")
}

func TestCandidates_TheBoxBesideTheListReadsTheWholeDraft(t *testing.T) {
	t.Parallel()
	m, st := candModel(t)
	require.NoError(t, st.PutCandidates(t.Context(), "om_c", "oc_team", []string{"第一行\n第二行"}, "markdown", 300))
	next, cmd := m.openCandidates()
	mm, _ := next.(Model).update(cmd())
	m = mm.(Model)
	m.cand.move(len(m.cand.items), m.candRows())

	v, ok := m.floatMenu()
	require.True(t, ok)
	require.Equal(t, "第一行 …", ansi.Strip(m.cand.rows[m.cand.idx].name), "the row opens the draft")
	require.Equal(t, "第一行\n第二行", ansi.Strip(v.info[0]), "the box carries it whole")
	require.Equal(t, "markdown", ansi.Strip(v.info[1]))
	require.Contains(t, ansi.Strip(m.View().Content), "第二行")
}

func TestCandidates_CapAtTheTenRowsTheDigitsReach(t *testing.T) {
	t.Parallel()
	m, st := candModel(t)
	texts := make([]string, 12)
	for i := range texts {
		texts[i] = "草稿" + string(rune('a'+i))
	}
	require.NoError(t, st.PutCandidates(t.Context(), "om_many", "oc_team", texts, "text", 300))
	next, cmd := m.openCandidates()
	mm, _ := next.(Model).update(cmd())
	m = mm.(Model)
	require.Greater(t, len(m.cand.items), quickRows)
	require.Equal(t, min(quickRows, m.floatCeiling()), m.candRows())
}

func TestCandidates_ParkABlockCursorOnTheDraftBelow(t *testing.T) {
	t.Parallel()
	m := openCand(t)
	c := m.View().Cursor
	require.NotNil(t, c)
	require.Equal(t, tea.CursorBlock, c.Shape, "the keys are commands here, not text")
	require.Equal(t, m.bodyHeight()+3, c.Y, "on the composer's first row, where i would resume")
}
