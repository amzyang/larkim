package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// collect runs a command tree and names the messages it produced.
func collect(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	var out []string
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, collect(c)...)
		}
	default:
		out = append(out, fmt.Sprintf("%T", msg))
	}
	return out
}

func TestUpdate_RevMsgReloadsEveryPane(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	_, err = st.UpsertMessages(context.Background(), []store.Message{{MessageID: "om_1", ChatID: "oc_1",
		MsgType: "text", ContentRaw: `{"text":"1"}`, CreateMs: 100, UpdateMs: 100, ThreadID: "omt_1", RawJSON: "{}"}}, 1000)
	require.NoError(t, err)

	m := New(Deps{Store: st})
	m.width, m.height = 120, 40
	m.chatID, m.threadOpen, m.threadID = "oc_1", true, "omt_1"
	// A closed channel keeps the re-subscribe from blocking the collector.
	revs := make(chan int64)
	close(revs)
	m.revs = revs

	// The revision moves for updates to rows already on screen — a rendering
	// that landed, a read status that flipped — and says nothing about which,
	// so every pane reloads.
	_, cmd := m.Update(revMsg{})
	kinds := collect(cmd)
	require.Contains(t, kinds, "tui.chatsLoadedMsg")
	require.Contains(t, kinds, "tui.messagesLoadedMsg")
	require.Contains(t, kinds, "tui.threadLoadedMsg")
}

func TestUpdate_MessagesLoadedKeepsSearchCursor(t *testing.T) {
	m := sized(120, 36)
	m.searching, m.focus = true, paneMessages
	m.searchResults = []store.Message{
		{MessageID: "om_hit_a", ChatID: "oc_1", Content: "hit a", RenderedAt: 1},
		{MessageID: "om_hit_b", ChatID: "oc_7", Content: "hit b", RenderedAt: 1},
	}
	m.msgIdx, m.msgTop = 1, 0

	// A background reload of the open chat lands while the pane is showing
	// hits from several chats; the cursor belongs to those hits.
	mm, _ := m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: m.msgs})
	m = mm.(Model)
	require.True(t, m.searching)
	require.Equal(t, 1, m.msgIdx)
	require.Equal(t, 0, m.msgTop)
}

// grown is the page a background tick delivers once one more message landed.
func grown(m Model, id string) []store.Message {
	return append(slices.Clone(m.msgs), store.Message{MessageID: id, ChatID: "oc_1",
		SenderName: "张三", SenderID: "ou_a", Content: "new", RenderedAt: 1, CreateMs: 99_000})
}

func TestUpdate_MessagesLoadedKeepsCursorOffTheEnd(t *testing.T) {
	m := shortMsgs(sized(120, 36), 6)
	m.focus, m.msgIdx = paneMessages, 2
	was := idAt(m.msgs, m.msgIdx)

	m = loaded(m, grown(m, "om_x"))

	require.Equal(t, was, idAt(m.msgs, m.msgIdx),
		"a background reload must leave the cursor on the message the reader put it on")
}

func TestUpdate_MessagesLoadedFollowsTheEnd(t *testing.T) {
	m := shortMsgs(sized(120, 36), 6)
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1

	m = loaded(m, grown(m, "om_x"))

	require.Equal(t, "om_x", idAt(m.msgs, m.msgIdx),
		"a cursor already on the newest message follows the one that arrives")
}

func TestUpdate_MessagesLoadedEntersChatAtTheEnd(t *testing.T) {
	m := shortMsgs(sized(120, 36), 6)
	m.focus, m.msgIdx = paneMessages, 2
	m.pendingChat = "oc_7"

	page := []store.Message{
		{MessageID: "om_a", ChatID: "oc_7", Content: "a", RenderedAt: 1, CreateMs: 1000},
		{MessageID: "om_b", ChatID: "oc_7", Content: "b", RenderedAt: 1, CreateMs: 2000},
	}
	mm, _ := m.Update(messagesLoadedMsg{chatID: "oc_7", msgs: page})
	m = mm.(Model)

	require.Equal(t, "oc_7", m.chatID)
	require.Equal(t, "om_b", idAt(m.msgs, m.msgIdx), "a chat opens on its newest message")
}

func TestUpdate_MessagesLoadedKeepsCursorRowWhenThePageSlides(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, 30
	m.rebuildMessages()
	m.scrollMessagesToSelection()
	require.Positive(t, m.msgTop, "the fixture has to overflow the pane for the row to mean anything")
	was, row := idAt(m.msgs, m.msgIdx), firstRow(m.msgRows, m.msgIdx)-m.msgTop

	// The page window slid: the oldest messages dropped off its head as a
	// newer one joined its tail, so every row number moved.
	slid := append(slices.Clone(m.msgs[3:]), store.Message{MessageID: "om_x", ChatID: "oc_1",
		SenderName: "张三", SenderID: "ou_a", Content: "new", RenderedAt: 1, CreateMs: 99_000})
	m = loaded(m, slid)

	require.Equal(t, was, idAt(m.msgs, m.msgIdx))
	require.Equal(t, row, firstRow(m.msgRows, m.msgIdx)-m.msgTop,
		"a page that slid under the cursor must not move it on screen")
}

func TestOnWheel_CarriesTheCursorIntoView(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	m.rebuildMessages()
	m.scrollMessagesToSelection()

	for range 4 {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelUp, X: chatsWidth + 5, Y: 5})
		m = mm.(Model)
	}
	top := m.msgTop
	require.Positive(t, top, "the wheel has to have somewhere to scroll")
	require.GreaterOrEqual(t, lastRow(m.msgRows, m.msgIdx), top)
	require.Less(t, firstRow(m.msgRows, m.msgIdx), top+m.listHeight())

	// The cursor is what a reload scrolls back to, so one the wheel left
	// behind drags the viewport down again.
	m = loaded(m, m.msgs)
	require.Equal(t, top, m.msgTop, "a reload must not undo a wheel scroll")
}
