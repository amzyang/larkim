package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func aiStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSaveAITurn_OutOfOrderWritesCannotLoseState(t *testing.T) {
	st := aiStore(t)
	ctx := t.Context()

	// The stream's terminal write can land after a later write of the same
	// row's intermediate state: rows go whole, so the last one to name the
	// truth wins no matter which order they arrive in.
	require.NoError(t, st.SaveAISession(ctx, AISession{ID: "as_1", ChatID: "oc_quiet", CreatedMs: 1}))
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_1", SessionID: "as_1", Seq: 0,
		Ask: "发布单合了吗", State: AITurnAsking, AtMs: 1}))
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_1", SessionID: "as_1", Seq: 0,
		Ask: "发布单合了吗", State: AITurnDone, Answer: "今晚合。", AtMs: 1}))
	// A stale intermediate write arrives last and is overwritten by the truth
	// already stored — unless it is itself the newer truth, which upserts.
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_1", SessionID: "as_1", Seq: 0,
		Ask: "发布单合了吗", State: AITurnAsking, AtMs: 1}))
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_1", SessionID: "as_1", Seq: 0,
		Ask: "发布单合了吗", State: AITurnDone, Answer: "今晚合。", AtMs: 1}))

	turns, err := st.ListAITurns(ctx, "as_1")
	require.NoError(t, err)
	require.Len(t, turns, 1)
	require.Equal(t, AITurnDone, turns[0].State)
	require.Equal(t, "今晚合。", turns[0].Answer)
}

func TestListAISessions_OrdersByCreationAndScopesToTheChat(t *testing.T) {
	st := aiStore(t)
	ctx := t.Context()
	for _, v := range []AISession{
		{ID: "as_2", ChatID: "oc_quiet", Title: "第二个", CreatedMs: 20},
		{ID: "as_1", ChatID: "oc_quiet", Title: "总结", CreatedMs: 10},
		{ID: "as_3", ChatID: "oc_elsewhere", Title: "别处的", CreatedMs: 15},
	} {
		require.NoError(t, st.SaveAISession(ctx, v))
	}

	quiet, err := st.ListAISessions(ctx, "oc_quiet")
	require.NoError(t, err)
	require.Equal(t, []string{"as_1", "as_2"},
		[]string{quiet[0].ID, quiet[1].ID}, "creation order, this chat's own")

	elsewhere, err := st.ListAISessions(ctx, "oc_elsewhere")
	require.NoError(t, err)
	require.Equal(t, []string{"as_3"}, []string{elsewhere[0].ID})
}

func TestSaveAISession_UpsertsTheTitle(t *testing.T) {
	st := aiStore(t)
	ctx := t.Context()
	require.NoError(t, st.SaveAISession(ctx, AISession{ID: "as_1", ChatID: "oc_quiet", CreatedMs: 1}))
	require.NoError(t, st.SaveAISession(ctx, AISession{ID: "as_1", ChatID: "oc_quiet", Title: "总结", CreatedMs: 1}))

	s, err := st.ListAISessions(ctx, "oc_quiet")
	require.NoError(t, err)
	require.Len(t, s, 1)
	require.Equal(t, "总结", s[0].Title)
	require.Equal(t, int64(1), s[0].CreatedMs, "the upsert keeps the creation time")
}

func TestListAITurns_RoundsTheRecordedContext(t *testing.T) {
	st := aiStore(t)
	ctx := t.Context()
	require.NoError(t, st.SaveAISession(ctx, AISession{ID: "as_1", ChatID: "oc_quiet", CreatedMs: 1}))
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_1", SessionID: "as_1", Seq: 0,
		Ask: "帮我润色", Sent: "帮我润色，语气委婉", Draft: true, AnchorID: "om_1",
		ThreadID: "omt_1", Window: 80, Compose: "我的草稿", Sel: []string{"om_1", "om_2"},
		State: AITurnStopped, AtMs: 1234}))
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_0", SessionID: "as_1", Seq: 1,
		Ask: "第二条", State: AITurnDone, AtMs: 2345}))

	turns, err := st.ListAITurns(ctx, "as_1")
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, "at_1", turns[0].ID, "question order, not write order")
	require.Equal(t, "at_0", turns[1].ID)
	first := turns[0]
	require.True(t, first.Draft)
	require.Equal(t, "om_1", first.AnchorID)
	require.Equal(t, "omt_1", first.ThreadID)
	require.Equal(t, 80, first.Window)
	require.Equal(t, "我的草稿", first.Compose)
	require.Equal(t, []string{"om_1", "om_2"}, first.Sel)
}

func TestDeleteAISession_DropsTheTurnsWithIt(t *testing.T) {
	st := aiStore(t)
	ctx := t.Context()
	require.NoError(t, st.SaveAISession(ctx, AISession{ID: "as_1", ChatID: "oc_quiet", CreatedMs: 1}))
	require.NoError(t, st.SaveAITurn(ctx, AITurn{ID: "at_1", SessionID: "as_1", Seq: 0, State: AITurnDone}))

	require.NoError(t, st.DeleteAISession(ctx, "as_1"))

	s, err := st.ListAISessions(ctx, "oc_quiet")
	require.NoError(t, err)
	require.Empty(t, s)
	turns, err := st.ListAITurns(ctx, "as_1")
	require.NoError(t, err)
	require.Empty(t, turns, "a turn without its session never comes back")
}
