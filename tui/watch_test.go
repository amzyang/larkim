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
	m.searchHits = messageHits(
		store.Message{MessageID: "om_hit_a", ChatID: "oc_1", Content: "hit a", RenderedAt: 1},
		store.Message{MessageID: "om_hit_b", ChatID: "oc_7", Content: "hit b", RenderedAt: 1},
	)
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

// slide is the page window moving on: the oldest messages drop off its head as
// a newer one joins its tail, so every row number moves.
func slide(msgs []store.Message, drop int) []store.Message {
	return append(slices.Clone(msgs[drop:]), store.Message{MessageID: "om_x", ChatID: "oc_1",
		SenderName: "张三", SenderID: "ou_a", Content: "new", RenderedAt: 1, CreateMs: 99_000})
}

func TestMessagesLoaded_AScrolledUpViewHoldsTheMessageOnItsTopRow(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, 30
	m.rebuildMessages()
	m.scrollMessagesToSelection()
	for range 3 {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelUp, X: chatsWidth + 5, Y: 5})
		m = mm.(Model)
	}
	require.Positive(t, m.msgTop, "the fixture has to overflow the pane for a top row to mean anything")
	onTop, was := idAt(m.msgs, m.msgRows[m.msgTop].idx), idAt(m.msgs, m.msgIdx)

	m = loaded(m, slide(m.msgs, 3))

	require.Equal(t, onTop, idAt(m.msgs, m.msgRows[m.msgTop].idx),
		"a page that slid under a scrolled-up view must not move it")
	require.Equal(t, was, idAt(m.msgs, m.msgIdx), "and the cursor keeps its own message")
}

func TestMessagesLoaded_AViewOnTheTailFollowsTheArrivingMessage(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	m.rebuildMessages()
	m.scrollMessagesToSelection()
	require.True(t, atTail(m.msgRows, m.msgTop, m.msgListHeight()), "the fixture starts on the tail")

	m = loaded(m, slide(m.msgs, 3))

	require.True(t, atTail(m.msgRows, m.msgTop, m.msgListHeight()),
		"a view showing the last line follows the message that arrives")
	require.Equal(t, "om_x", idAt(m.msgs, m.msgIdx), "and so does a cursor on the newest message")
}

func TestMessagesLoaded_ACursorLeftOnTheTailDoesNotDragAScrolledView(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	m.rebuildMessages()
	m.scrollMessagesToSelection()

	// The wheel took the view up and left the cursor on the newest message.
	// The cursor is not what decides the view has to sit at the tail.
	for range 4 {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelUp, X: chatsWidth + 5, Y: 5})
		m = mm.(Model)
	}
	onTop, top := idAt(m.msgs, m.msgRows[m.msgTop].idx), m.msgTop
	require.Positive(t, top)

	m = loaded(m, append(slices.Clone(m.msgs), store.Message{MessageID: "om_x", ChatID: "oc_1",
		SenderName: "张三", SenderID: "ou_a", Content: "new", RenderedAt: 1, CreateMs: 99_000}))

	require.Equal(t, top, m.msgTop, "the arriving message must not scroll a view the reader took up")
	require.Equal(t, onTop, idAt(m.msgs, m.msgRows[m.msgTop].idx))
}

func TestOnWheel_MessagesKeepsItsScrollAndItsCursor(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	m.rebuildMessages()
	m.scrollMessagesToSelection()
	was := m.msgIdx

	for range 4 {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelUp, X: chatsWidth + 5, Y: 5})
		m = mm.(Model)
	}
	top := m.msgTop
	require.Positive(t, top, "the wheel has to have somewhere to scroll")
	require.Equal(t, was, m.msgIdx, "the wheel moves the view, not the selection")

	// The cursor is parked on the newest message, off the top of the view. It
	// must not be what decides the view has to sit at the tail.
	m = loaded(m, m.msgs)
	require.Equal(t, top, m.msgTop, "a reload must not undo a wheel scroll")
	require.Equal(t, was, m.msgIdx)
}

func TestLayout_AResizeHoldsTheMessageOnTheTopRow(t *testing.T) {
	m := shortMsgs(sized(120, 36), 40)
	m.focus, m.msgIdx = paneMessages, 30
	m.rebuildMessages()
	m.scrollMessagesToSelection()
	for range 3 {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelUp, X: chatsWidth + 5, Y: 5})
		m = mm.(Model)
	}
	onTop := idAt(m.msgs, m.msgRows[m.msgTop].idx)

	// A narrower pane rewraps every row, so a line number kept across it would
	// land the view somewhere else entirely.
	m.width = 80
	m.layout()

	require.Equal(t, onTop, idAt(m.msgs, m.msgRows[m.msgTop].idx),
		"a resize holds the reader's place, it does not scroll to the cursor")
}

// threaded opens a thread long enough to overflow the right pane.
func threaded(m Model) Model {
	m.threadOpen, m.threadID, m.threadBase = true, "omt_1", m.msgs
	m.applyOutbox()
	m.layout()
	return m
}

func TestThreadLoaded_AScrolledUpViewHoldsTheMessageOnItsTopRow(t *testing.T) {
	m := threaded(shortMsgs(sized(120, 36), 40))
	m.focus, m.threadIdx = paneThread, len(m.thread)-1
	m.rebuildThread()
	m.scrollThreadToSelection()
	for range 3 {
		mm, _ := m.onWheel(tea.Mouse{Button: tea.MouseWheelUp, X: m.width - 5, Y: 5})
		m = mm.(Model)
	}
	onTop, top := idAt(m.thread, m.threadRows[m.threadTop].idx), m.threadTop
	require.Positive(t, top, "the fixture has to overflow the pane for a top row to mean anything")

	mm, _ := m.Update(threadLoadedMsg{threadID: "omt_1", msgs: slide(m.thread, 3)})
	m = mm.(Model)

	require.Equal(t, onTop, idAt(m.thread, m.threadRows[m.threadTop].idx),
		"a reload must not move a thread the reader scrolled up")
}
