package tui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// paged is a chat whose store page holds n messages under a limit of limit,
// scrolled to the top of it.
func paged(n, limit int) Model {
	m := sized(120, 36)
	m.msgsBase = nil
	for i := range n {
		m.msgsBase = append(m.msgsBase, store.Message{MessageID: fmt.Sprintf("om_%d", i), ChatID: "oc_1",
			SenderName: "林岚", SenderID: "ou_me", Content: "content", RenderedAt: 1, CreateMs: int64(i) * 60_000})
	}
	m.msgLimit = limit
	m.applyOutbox()
	m.rebuildMessages()
	m.msgTop = 0
	return m
}

func TestNew_StartsOnOneMessagePage(t *testing.T) {
	require.Equal(t, messagePageSize, New(Deps{Self: "ou_me"}).msgLimit)
}

func TestMessageQuery_CarriesTheGrownLimit(t *testing.T) {
	require.Equal(t, 600, messageQuery("oc_1", 0, 600).Limit)
	require.Equal(t, 600, messageQuery("oc_1", 123, 600).Limit, "an anchored page is bounded by the same limit")
}

func TestGrowMessages_FullPageAsksForMore(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	require.NotNil(t, m.growMessages())
	require.Equal(t, 2*messagePageSize, m.msgLimit)
}

func TestGrowMessages_AsksOnceWhileThePageIsInFlight(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	require.NotNil(t, m.growMessages())
	require.Nil(t, m.growMessages(), "the widened limit is itself the guard")
	require.Equal(t, 2*messagePageSize, m.msgLimit)
}

func TestGrowMessages_ShortChatAsksForNothing(t *testing.T) {
	m := paged(messagePageSize-1, messagePageSize)
	require.Nil(t, m.growMessages())
	require.True(t, m.atLocalFloor())
}

func TestGrowMessages_OnlyAtTheTop(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	m.msgTop = 1
	require.Nil(t, m.growMessages())
	require.Equal(t, messagePageSize, m.msgLimit)
}

func TestGrowMessages_SearchPanelAsksForNothing(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	m.searching = true
	require.Nil(t, m.growMessages())
}

func TestGrowMessages_EmptyChatAsksForNothing(t *testing.T) {
	m := paged(0, messagePageSize)
	m.chatID = ""
	require.Nil(t, m.growMessages())
}

func TestGrowMessages_FromASearchAnchorDropsTheAnchor(t *testing.T) {
	m := paged(anchoredPageSize, anchoredPageSize)
	m.msgSince = 123
	require.NotNil(t, m.growMessages())
	require.Zero(t, m.msgSince, "older messages lie before the anchor, so the anchor goes")
	require.Equal(t, anchoredPageSize+messagePageSize, m.msgLimit)
}

func TestMove_ScrollToTopAsksForAnOlderPage(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	m.focus, m.msgIdx = paneMessages, 0
	mm, cmd := m.move(-1)
	require.NotNil(t, cmd)
	require.Equal(t, 2*messagePageSize, mm.(Model).msgLimit)
}

func TestWheel_ScrollToTopAsksForAnOlderPage(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	m.msgTop = 1
	mm, cmd := m.onWheel(tea.Mouse{X: chatsWidth + 5, Y: 4, Button: tea.MouseWheelUp})
	require.NotNil(t, cmd)
	require.Equal(t, 2*messagePageSize, mm.(Model).msgLimit)
}

func TestGrownPage_KeepsTheTopRow(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	m.chatID, m.msgTop = "oc_1", 6
	top := m.msgRows[m.msgTop].idx
	wantID := m.msgs[top].MessageID

	older := append([]store.Message{{MessageID: "om_older", ChatID: "oc_1", SenderName: "张三", SenderID: "ou_a",
		Content: "older", RenderedAt: 1, CreateMs: -60_000}}, m.msgsBase...)
	mm, _ := m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: older})
	m = mm.(Model)

	require.Equal(t, wantID, m.msgs[m.msgRows[m.msgTop].idx].MessageID,
		"the message the top row held stays on the top row when older ones land above it")
}

// atFloor is a chat scrolled to the top of everything the store holds, with
// floorMs of history still waiting at Feishu (0 meaning none).
func atFloor(floorMs int64) Model {
	m := paged(messagePageSize-1, messagePageSize)
	m.chats[indexOfChat(m.chats, m.chatID)].HistoryFloorMs = floorMs
	m.rebuildMessages()
	return m
}

func TestRebuildMessages_MarksTheLocalFloor(t *testing.T) {
	require.True(t, atFloor(1000).msgRows[0].plain)
	require.Contains(t, atFloor(1000).msgRows[0].text, floorLabel)
	require.Contains(t, atFloor(0).msgRows[0].text, startLabel, "nothing older is left to fetch")

	m := atFloor(1000)
	m.msgPullInFlight = true
	m.rebuildMessages()
	require.Contains(t, m.msgRows[0].text, fetchingLabel)
}

func TestRebuildMessages_FullPageCarriesNoFloor(t *testing.T) {
	m := paged(messagePageSize, messagePageSize)
	require.NotContains(t, m.msgRows[0].text, floorLabel, "a full page has more behind it")
}

func TestPullOlder_AtTheFloorReachesPastIt(t *testing.T) {
	m := atFloor(1000)
	require.NotNil(t, m.growMessages())
	require.True(t, m.msgPullInFlight)
	require.Equal(t, messagePageSize, m.msgLimit, "the store had nothing more to widen into")
}

func TestPullOlder_AsksOnceWhileTheCallIsOut(t *testing.T) {
	m := atFloor(1000)
	require.NotNil(t, m.growMessages())
	require.Nil(t, m.growMessages())
}

func TestPullOlder_CompleteHistoryAsksNothing(t *testing.T) {
	m := atFloor(0)
	require.Nil(t, m.growMessages())
	require.False(t, m.msgPullInFlight)
}

func TestNoteOlderPull_AFailureSaysSoAndFreesTheNextTry(t *testing.T) {
	m := atFloor(1000)
	m.msgPullInFlight = true
	require.Nil(t, m.noteOlderPull(olderPulledMsg{chatID: m.chatID, err: errors.New("rate limited")}))
	require.False(t, m.msgPullInFlight)
	require.Contains(t, m.notice, "rate limited")
	require.True(t, m.noticeErr)
}

func TestNoteOlderPull_AnAnswerForAChatSinceLeftIsDropped(t *testing.T) {
	m := atFloor(1000)
	m.msgPullInFlight = true
	require.Nil(t, m.noteOlderPull(olderPulledMsg{chatID: "oc_elsewhere", err: errors.New("boom")}))
	require.True(t, m.msgPullInFlight, "the flag belongs to the chat on screen")
	require.Empty(t, m.notice)
}

func TestRebuildMessages_EmptyChatCarriesNoFloor(t *testing.T) {
	require.Empty(t, paged(0, messagePageSize).msgRows)
}

func TestNoteOlderPull_ASuccessRereadsTheFloorItMoved(t *testing.T) {
	m := atFloor(1000)
	m.msgPullInFlight = true
	// history_floor_ms is outside the revision trigger, so the pane would go
	// on offering history the pull just exhausted unless the chats are re-read.
	require.NotNil(t, m.noteOlderPull(olderPulledMsg{chatID: m.chatID}))
	require.False(t, m.msgPullInFlight)
	require.Empty(t, m.notice)
}
