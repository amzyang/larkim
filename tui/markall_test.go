package tui

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// sweepModel is a list of n chats, each carrying one message Feishu still
// reports unseen, with the opener recorded instead of reaching macOS.
func sweepModel(t *testing.T, n int) (Model, *store.Store, *[]openCall, *error) {
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

	var calls []openCall
	var openErr error
	m := New(Deps{Store: st, Self: "ou_me", Client: larkcli.NewFake(), Config: config.Config{ApplinkPaceMS: testPace}, OpenURL: func(targets []string, background bool) error {
		calls = append(calls, openCall{targets, background})
		return openErr
	}})
	m.width, m.height = 120, 36
	return m, st, &calls, &openErr
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

// waiting is every chat the store would still walk the client onto: the half
// only a Feishu receipt settles.
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

func TestMarkAllRead_WalksEveryChatThatWasWaiting(t *testing.T) {
	t.Parallel()
	m, st, calls, _ := sweepModel(t, 3)
	m = pressMarkAll(t, m)

	require.Equal(t, []openCall{
		opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_0&position=1", true),
		opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_1&position=1", true),
		opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_2&position=1", true),
	}, *calls, "one background applink per chat, each landing on that chat's newest unread")
	require.Empty(t, badges(t, st), "and the local half is settled")
	require.Len(t, waiting(t, st), 3,
		"the client's half is not: no receipt has come back yet, so another press would walk them again")
	require.Contains(t, m.notice, "3 chats marked read")
}

func TestMarkAllRead_NeverBatchesSeveralTargetsIntoOneOpen(t *testing.T) {
	t.Parallel()
	m, _, calls, _ := sweepModel(t, 3)
	pressMarkAll(t, m)

	for _, c := range *calls {
		require.Len(t, c.targets, 1,
			"the client can only be in one chat, so a set of applinks arrives as one navigation")
	}
}

func TestMarkAllRead_WritesBeforeItFiresTheFirstApplink(t *testing.T) {
	t.Parallel()
	m, st, calls, _ := sweepModel(t, 3)
	m, cmd := clickMarkAll(m)
	next, cmd := m.Update(cmd())
	m = next.(Model)

	// Run only as far as the write's own answer: the durable half has to be
	// down before the best-effort half starts (ARCH.md 三).
	msg := cmd()
	require.IsType(t, markAllDoneMsg{}, msg)
	require.Empty(t, badges(t, st))
	require.Empty(t, *calls)
}

func TestMarkAllRead_EndsOnTheChatTheReaderHasOpen(t *testing.T) {
	t.Parallel()
	m, _, calls, _ := sweepModel(t, 3)
	m.chatID = "oc_0"

	pressMarkAll(t, m)

	require.Len(t, *calls, 3)
	require.Equal(t, opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_0&position=1", true), (*calls)[2],
		"the client comes to rest where the terminal is")
}

func TestMarkAllRead_APressWhileAQuestionIsOnScreenStartsNothing(t *testing.T) {
	t.Parallel()
	m, st, calls, _ := sweepModel(t, 3)
	m.confirm = confirmation{kind: confirmRecall, messageID: "om_0"}
	m = m.notify("recall this message? y/n", false)

	m = pressMarkAll(t, m)

	require.Empty(t, *calls)
	// Not waiting(): that set survives the write now, so it would read the
	// same whether or not the press was swallowed.
	require.Len(t, badges(t, st), 3, "nothing was taken as read either")
	require.Equal(t, "recall this message? y/n", m.notice, "the question is still the one on screen")
	require.Equal(t, confirmRecall, m.confirm.kind)
}

func TestMarkAllRead_AnOpenFailureIsReportedAndDoesNotStopTheChain(t *testing.T) {
	t.Parallel()
	m, _, calls, openErr := sweepModel(t, 3)
	*openErr = errFailedOpen
	m = pressMarkAll(t, m)

	require.Len(t, *calls, 3, "the chats behind a refusal have dots of their own")
	require.Contains(t, m.notice, "3 not cleared in Feishu")
	require.True(t, m.noticeErr)
}

func TestMarkAllRead_ReportsOnlyTheFailuresOfItsOwnSweep(t *testing.T) {
	t.Parallel()
	m, _, calls, openErr := sweepModel(t, 2)

	// A read gate's applink fails on the way past. It says nothing on its own
	// way out, so it must leave nothing behind for the next sweep to claim.
	*openErr = errFailedOpen
	gated, cmd := m.takeRead("oc_elsewhere", unreadPage())
	m = drain(t, gated, cmd)
	*openErr = nil
	*calls = nil

	m = pressMarkAll(t, m)

	require.Len(t, *calls, 2)
	require.Equal(t, "2 chats marked read", m.notice, "a sweep whose every applink landed reports no failure")
	require.False(t, m.noticeErr)
}

func TestMarkAllRead_WaitsForTheLastOpenBeforeItReports(t *testing.T) {
	t.Parallel()
	m, _, calls, openErr := sweepModel(t, 1)
	*openErr = errFailedOpen
	m, cmd := clickMarkAll(m)
	next, cmd := m.Update(cmd())
	armed, _ := next.(Model).Update(cmd())
	m = armed.(Model)

	// The open and the tick behind it go out together and applink.Open waits
	// on a process, so the tick that finds the queue empty can beat the
	// answer it is waiting for.
	due, fired := m.Update(applinkDueMsg{m.applinks.gen})
	m = due.(Model)
	early, _ := m.Update(applinkDueMsg{m.applinks.gen})
	m = early.(Model)
	require.NotContains(t, m.notice, "marked read", "the sweep is not over while an open is still out")

	m = drain(t, m, fired)

	require.Len(t, *calls, 1)
	require.Contains(t, m.notice, "1 not cleared in Feishu", "the last open's failure is in the report")
	require.True(t, m.noticeErr)
}

func TestMarkAllRead_WalksAgainWhatTheLastSweepFailedToClear(t *testing.T) {
	t.Parallel()
	m, _, calls, openErr := sweepModel(t, 2)
	*openErr = errFailedOpen
	m = pressMarkAll(t, m)
	require.Len(t, *calls, 2)
	*openErr, *calls = nil, nil

	m = pressMarkAll(t, m)

	require.Len(t, *calls, 2,
		"nothing said the client's dots came down, so the second press is the reader's retry")
	require.Contains(t, m.notice, "2 chats marked read")
	require.False(t, m.noticeErr)
}

func TestMarkAllRead_LeavesOutWhatFeishuConfirmedRead(t *testing.T) {
	t.Parallel()
	m, st, calls, _ := sweepModel(t, 2)
	m = pressMarkAll(t, m)
	*calls = nil
	for _, id := range []string{"om_0", "om_1"} {
		require.NoError(t, st.SetReadStatus(t.Context(), id, new(true), 200, 0))
	}

	m = pressMarkAll(t, m)

	require.Empty(t, *calls)
	require.Equal(t, "nothing waiting in Feishu", m.notice, "the receipts are what end the sweep")
}

func TestMarkAllRead_OnAStoreWithNothingWaitingSaysSo(t *testing.T) {
	t.Parallel()
	m, _, calls, _ := sweepModel(t, 0)

	m = pressMarkAll(t, m)

	require.Equal(t, "nothing waiting in Feishu", m.notice)
	require.Empty(t, *calls)
}

func TestMarkAllRead_EscapeStopsTheWalk(t *testing.T) {
	t.Parallel()
	m, _, calls, _ := sweepModel(t, 4)
	m, cmd := clickMarkAll(m)
	next, cmd := m.Update(cmd())
	m = next.(Model)

	// The walk is driven by hand rather than drained, because backing out of
	// it is only possible while it is still running.
	armed, _ := m.Update(cmd())
	m = walkOne(t, armed.(Model))
	require.Len(t, *calls, 1)

	stopped, _ := m.onNormalKey("esc")
	m = stopped.(Model)
	require.Equal(t, "stopped", m.notice)
	require.Empty(t, m.applinks.left)

	// A chain already armed cannot be recalled, only ignored: its tick
	// arrives carrying the generation esc left behind.
	drain(t, m, m.applinkTick(m.applinks.gen-1))
	require.Len(t, *calls, 1, "a tick from the abandoned chain opens nothing")
}

// walkOne advances the queue by a single chat. The due message is delivered
// by hand instead of waited out, and of what comes back only the hand-over is
// run, so the walk stops where the test wants it.
func walkOne(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(applinkDueMsg{m.applinks.gen})
	m = next.(Model)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "a due slot hands back the open and the slot behind it")
	for _, c := range batch {
		if fired, ok := c().(applinkFiredMsg); ok {
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

// errFailedOpen stands for macOS refusing an applink, which is all the caller
// ever learns.
var errFailedOpen = errors.New("no application knows how to open URL")

// The markers are one visit's record of what was waiting. A mark-all is the
// reader saying none of it is, so they go with the counts, and the panel drawn
// beside the list answers the press rather than the watch's next beat.
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
