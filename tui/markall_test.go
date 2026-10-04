package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// sweepModel is a list of n chats, each carrying one message Feishu still
// reports unseen, with the clear lever recorded instead of reaching the gateway.
func sweepModel(t *testing.T, n int) (Model, *store.Store, *[]store.ChatUnread, *error) {
	t.Helper()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	var msgs []store.Message
	for i := range n {
		id := "oc_" + strconv.Itoa(i)
		require.NoError(t, st.EnsureChat(ctx, id, 1))
		msgs = append(msgs, store.Message{MessageID: "om_" + strconv.Itoa(i), ChatID: id, MsgType: "text",
			SenderID: "ou_x", SenderName: "张三", ContentRaw: `{"text":"在吗"}`, CreateMs: int64(100 + i),
			UpdateMs: int64(100 + i), MessagePosition: 1})
	}
	_, err = st.UpsertMessages(ctx, msgs, 1)
	require.NoError(t, err)
	unread := false
	for _, x := range msgs {
		require.NoError(t, st.SetReadStatus(ctx, x.MessageID, &unread, 100, 0))
	}

	var cleared []store.ChatUnread
	var clearErr error
	m := New(Deps{Store: st, Self: "ou_me", Client: larkcli.NewFake(),
		ClearBadge: func(_ context.Context, c store.ChatUnread) error {
			cleared = append(cleared, c)
			if clearErr != nil {
				return clearErr
			}
			return nil
		}})
	m.width, m.height = 120, 36
	return m, st, &cleared, &clearErr
}

// clickMarkAll is the click on the header button, at the column the header
// draws it in and on the header's own row, with the sweep it starts left
// unrun.
func clickMarkAll(m Model) (Model, tea.Cmd) {
	next, cmd := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: 1 + markAllCol(chatsWidth-2), Y: 1})
	return next.(Model), cmd
}

// pressMarkAll is that click carried through to the end of the walk.
func pressMarkAll(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := clickMarkAll(m)
	return drain(t, m, cmd)
}

// waiting is every chat the store would still clear: the half only a Feishu
// receipt settles.
func waiting(t *testing.T, st *store.Store) []store.ChatUnread {
	t.Helper()
	chats, err := st.ChatsWithUnread(t.Context())
	require.NoError(t, err)
	return chats
}

// badges is the other half: the chats larkim still draws a number beside,
// which is what a local write brings down.
func badges(t *testing.T, st *store.Store) map[string]int64 {
	t.Helper()
	chats, err := st.ListChats(t.Context(), store.ChatQuery{})
	require.NoError(t, err)
	out := map[string]int64{}
	for _, c := range chats {
		if c.UnreadCount > 0 {
			out[c.ChatID] = c.UnreadCount
		}
	}
	return out
}

func TestMarkAllRead_ClearsEveryChatThatWasWaiting(t *testing.T) {
	t.Parallel()
	m, st, cleared, _ := sweepModel(t, 3)
	m = pressMarkAll(t, m)

	require.Equal(t, []store.ChatUnread{
		{ChatID: "oc_0", Position: 1},
		{ChatID: "oc_1", Position: 1},
		{ChatID: "oc_2", Position: 1},
	}, *cleared)
	require.Empty(t, badges(t, st), "and the local half is settled")
	require.Len(t, waiting(t, st), 3,
		"the client's half is not: no receipt has come back yet, so another press would clear them again")
	require.Contains(t, m.notice, "3 chats marked read")
}

func TestMarkAllRead_WritesBeforeItFiresTheFirstClear(t *testing.T) {
	t.Parallel()
	m, st, cleared, _ := sweepModel(t, 3)
	m, cmd := clickMarkAll(m)
	next, cmd := m.Update(cmd())
	m = next.(Model)

	msg := cmd()
	require.IsType(t, markAllDoneMsg{}, msg)
	require.Empty(t, badges(t, st))
	require.Empty(t, *cleared)
}

func TestMarkAllRead_EndsOnTheChatTheReaderHasOpen(t *testing.T) {
	t.Parallel()
	m, _, cleared, _ := sweepModel(t, 3)
	m.chatID = "oc_0"

	pressMarkAll(t, m)

	require.Len(t, *cleared, 3)
	require.Equal(t, store.ChatUnread{ChatID: "oc_0", Position: 1}, (*cleared)[2],
		"the client comes to rest where the terminal is")
}

func TestMarkAllRead_APressWhileAQuestionIsOnScreenStartsNothing(t *testing.T) {
	t.Parallel()
	m, st, cleared, _ := sweepModel(t, 3)
	m.confirm = confirmation{kind: confirmRecall, messageID: "om_0"}
	m = m.notify("recall this message? y/n", false)

	m = pressMarkAll(t, m)

	require.Empty(t, *cleared)
	require.Len(t, badges(t, st), 3, "nothing was taken as read either")
	require.Equal(t, "recall this message? y/n", m.notice, "the question is still the one on screen")
	require.Equal(t, confirmRecall, m.confirm.kind)
}

func TestMarkAllRead_AClearFailureIsReportedAndDoesNotStopTheChain(t *testing.T) {
	t.Parallel()
	m, _, cleared, clearErr := sweepModel(t, 3)
	*clearErr = errFailedClear
	m = pressMarkAll(t, m)

	require.Len(t, *cleared, 3, "the chats behind a refusal have dots of their own")
	require.Contains(t, m.notice, "3 not cleared in Feishu")
	require.True(t, m.noticeErr)
}

func TestMarkAllRead_ReportsOnlyTheFailuresOfItsOwnSweep(t *testing.T) {
	t.Parallel()
	m, _, cleared, clearErr := sweepModel(t, 2)

	*clearErr = errFailedClear
	gated, cmd := m.takeRead("oc_elsewhere", unreadPage())
	m = drain(t, gated, cmd)
	*clearErr = nil
	*cleared = nil

	m = pressMarkAll(t, m)

	require.Len(t, *cleared, 2)
	require.Equal(t, "2 chats marked read", m.notice, "a sweep whose every clear landed reports no failure")
	require.False(t, m.noticeErr)
}

func TestMarkAllRead_WaitsForTheLastClearBeforeItReports(t *testing.T) {
	t.Parallel()
	m, _, cleared, clearErr := sweepModel(t, 1)
	*clearErr = errFailedClear
	m, cmd := clickMarkAll(m)
	next, cmd := m.Update(cmd())
	armed, _ := next.(Model).Update(cmd())
	m = armed.(Model)

	due, fired := m.Update(clearDueMsg{m.clears.gen})
	m = due.(Model)
	early, _ := m.Update(clearDueMsg{m.clears.gen})
	m = early.(Model)
	require.NotContains(t, m.notice, "marked read", "the sweep is not over while a clear is still out")

	m = drain(t, m, fired)

	require.Len(t, *cleared, 1)
	require.Contains(t, m.notice, "1 not cleared in Feishu", "the last clear's failure is in the report")
	require.True(t, m.noticeErr)
}

func TestMarkAllRead_ClearsAgainWhatTheLastSweepFailedToClear(t *testing.T) {
	t.Parallel()
	m, _, cleared, clearErr := sweepModel(t, 2)
	*clearErr = errFailedClear
	m = pressMarkAll(t, m)
	require.Len(t, *cleared, 2)
	*clearErr, *cleared = nil, nil

	m = pressMarkAll(t, m)

	require.Len(t, *cleared, 2,
		"nothing said the client's dots came down, so the second press is the reader's retry")
	require.Contains(t, m.notice, "2 chats marked read")
	require.False(t, m.noticeErr)
}

func TestMarkAllRead_LeavesOutWhatFeishuConfirmedRead(t *testing.T) {
	t.Parallel()
	m, st, cleared, _ := sweepModel(t, 2)
	m = pressMarkAll(t, m)
	*cleared = nil
	for _, id := range []string{"om_0", "om_1"} {
		require.NoError(t, st.SetReadStatus(t.Context(), id, new(true), 200, 0))
	}

	m = pressMarkAll(t, m)

	require.Empty(t, *cleared)
	require.Equal(t, "nothing waiting in Feishu", m.notice, "the receipts are what end the sweep")
}

func TestMarkAllRead_OnAStoreWithNothingWaitingSaysSo(t *testing.T) {
	t.Parallel()
	m, _, cleared, _ := sweepModel(t, 0)

	m = pressMarkAll(t, m)

	require.Equal(t, "nothing waiting in Feishu", m.notice)
	require.Empty(t, *cleared)
}

func TestMarkAllRead_EscapeStopsTheSweep(t *testing.T) {
	t.Parallel()
	m, _, cleared, _ := sweepModel(t, 4)
	m, cmd := clickMarkAll(m)
	next, cmd := m.Update(cmd())
	m = next.(Model)

	armed, _ := m.Update(cmd())
	m = clearOne(t, armed.(Model))
	require.Len(t, *cleared, 1)

	stopped, _ := m.onNormalKey("esc")
	m = stopped.(Model)
	require.Equal(t, "stopped", m.notice)
	require.Empty(t, m.clears.left)

	drain(t, m, m.clearTick(m.clears.gen-1))
	require.Len(t, *cleared, 1, "a tick from the abandoned chain clears nothing")
}

// clearOne advances the queue by a single chat. The due message is delivered
// by hand instead of waited out, and of what comes back only the clear is
// run, so the sweep stops where the test wants it.
func clearOne(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(clearDueMsg{m.clears.gen})
	m = next.(Model)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "a due slot hands back the clear and the slot behind it")
	for _, c := range batch {
		if fired, ok := c().(clearFiredMsg); ok {
			counted, _ := m.Update(fired)
			m = counted.(Model)
		}
	}
	return m
}

func TestRunCommand_ReadAllTakesTheSamePathAsTheButton(t *testing.T) {
	t.Parallel()
	m, _, _, _ := sweepModel(t, 2)

	next, cmd := m.runCommand("read-all")
	m = drain(t, next.(Model), cmd)

	require.Equal(t, "2 chats marked read", m.notice)
}

var errFailedClear = errors.New("session cookie missing")

func TestOnMarkAllDone_DropsTheMarkersAndReanchorsThePanel(t *testing.T) {
	t.Parallel()
	m, st, _, _ := sweepModel(t, 2)
	m = drain(t, m, m.startUnread(false))
	require.Len(t, m.feed.sections, 2)
	require.NotEmpty(t, m.dots)

	swept := waiting(t, st)
	_, err := st.MarkAllRead(t.Context(), 900)
	require.NoError(t, err)
	next, cmd := m.onMarkAllDone(markAllDoneMsg{chats: swept})
	m = drain(t, next, cmd)

	require.Empty(t, m.dots, "no marker outlives the press that settled it")
	require.Empty(t, m.feed.sections, "and no section holds the anchor it was drawn on")
}
