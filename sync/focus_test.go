package sync

import (
	stdsync "sync"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestRefreshChat_ListsTheOpenChatWithoutSearching(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.AddMessage(msg("om_fresh", "oc_a", clk.Now().Add(-2*time.Second), "just sent"))

	n, err := s.RefreshChat(ctx, "oc_a", "")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NotContains(t, f.Calls, "search", "the listing reads the store, not the search index")
	require.Equal(t, "list:chat:oc_a", f.Calls[0])

	got, err := s.Store.GetMessage(ctx, "om_fresh")
	require.NoError(t, err)
	require.Equal(t, "oc_a", got.ChatID)

	chat, err := s.Store.GetChat(ctx, "oc_a")
	require.NoError(t, err)
	require.Equal(t, clk.Now().Add(-2*time.Second).UnixMilli(), chat.CursorMs, "the chat cursor advanced")
}

func TestRefreshChat_CostsOneCallAndNoRepaintWhenNothingIsNew(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	m := msg("om_fresh", "oc_a", clk.Now().Add(-2*time.Second), "just sent")
	f.AddMessage(m)
	f.Rendered["om_fresh"] = larkcli.RenderedMessage{MessageID: "om_fresh", ChatID: "oc_a",
		MsgType: "text", Content: "just sent"}

	_, err := s.RefreshChat(ctx, "oc_a", "")
	require.NoError(t, err)
	f.Calls = nil
	revBefore, err := s.Store.DataRev(ctx)
	require.NoError(t, err)

	// The beat still re-lists the window and re-upserts what is in it, so the
	// count is not zero; what must not move is the revision the panes watch.
	_, err = s.RefreshChat(ctx, "oc_a", "")
	require.NoError(t, err)
	require.Equal(t, []string{"list:chat:oc_a"}, f.Calls,
		"a quiet beat is one listing; nothing to render, nothing to re-fetch")

	revAfter, err := s.Store.DataRev(ctx)
	require.NoError(t, err)
	require.Equal(t, revBefore, revAfter, "re-listing an unchanged window must not repaint the panes")
}

func TestRefreshChat_FollowsTheOpenThread(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	// The root predates the window, so pullChat never sees it and would miss
	// the reply; naming the open thread is what brings it in.
	root := msg("om_root", "oc_a", clk.Now().Add(-2*time.Hour), "old root")
	root.ThreadID = "omt_1"
	f.AddMessage(root)
	reply := msg("om_reply", "oc_a", clk.Now().Add(-2*time.Second), "new reply")
	reply.ThreadID = "omt_1"
	reply.MessagePosition = -1
	f.AddMessage(reply)

	n, err := s.RefreshChat(ctx, "oc_a", "omt_1")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Contains(t, f.Calls, "list:thread:omt_1")
	got, err := s.Store.GetMessage(ctx, "om_reply")
	require.NoError(t, err)
	require.Equal(t, "omt_1", got.ThreadID)
}

func TestRefreshChat_LeavesTheSearchCursorsAlone(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.AddMessage(msg("om_fresh", "oc_a", clk.Now().Add(-2*time.Second), "just sent"))
	_, err := s.Tick(ctx)
	require.NoError(t, err)
	wmBefore, _, _ := s.Store.GetState(ctx, KeyWatermark)
	histBefore, _, _ := s.Store.GetState(ctx, KeyHistoryCursor)

	clk.t = clk.t.Add(time.Second)
	_, err = s.RefreshChat(ctx, "oc_a", "")
	require.NoError(t, err)

	wmAfter, _, _ := s.Store.GetState(ctx, KeyWatermark)
	histAfter, _, _ := s.Store.GetState(ctx, KeyHistoryCursor)
	require.Equal(t, wmBefore, wmAfter, "the hot path must not move the search watermark")
	require.Equal(t, histBefore, histAfter)
}

func TestRefreshChat_NudgesBeforeTheRenderingLands(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	nudges := 0
	s.OnChange = func() { nudges++ }
	m := msg("om_fresh", "oc_a", clk.Now().Add(-2*time.Second), "just sent")
	f.AddMessage(m)
	f.Rendered["om_fresh"] = larkcli.RenderedMessage{MessageID: "om_fresh", ChatID: "oc_a",
		MsgType: "text", Content: "just sent"}

	_, err := s.RefreshChat(ctx, "oc_a", "")
	require.NoError(t, err)
	require.Equal(t, 2, nudges, "the body repaints, then the rendering does")
}

func TestRefreshChat_RunsAlongsideATick(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	f.AddMessage(msg("om_fresh", "oc_a", clk.Now().Add(-2*time.Second), "just sent"))

	var wg stdsync.WaitGroup
	wg.Go(func() {
		_, err := s.Tick(ctx)
		require.NoError(t, err)
	})
	wg.Go(func() {
		_, err := s.RefreshChat(ctx, "oc_a", "")
		require.NoError(t, err)
	})
	wg.Wait()

	got, err := s.Store.GetMessage(ctx, "om_fresh")
	require.NoError(t, err)
	require.Equal(t, "oc_a", got.ChatID)
}
