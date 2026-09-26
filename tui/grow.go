package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// The head of a page the store cannot reach behind says what is above it.
// Feishu's client has no counterpart for any of these: its history lives on
// the server and never runs out, while this one is bounded by what has been
// pulled, and by whether this process is the one allowed to pull.
const (
	floorLabel    = "Oldest synced"
	startLabel    = "Beginning of chat"
	fetchingLabel = "Loading older messages"
	// readOnlyLabel names the one thing the reader can do about it. Only the
	// process holding the data-dir lock may write synced data, so a TUI
	// beside a daemon can show the floor but never move it.
	readOnlyLabel = floorLabel + " · run without a daemon to reach further back"
)

// atLocalFloor reports that the store holds nothing behind the page on screen.
// A page shorter than the limit it was asked for is the whole of what the
// store has; ExcludeThreadReplies makes that limit count only the rows the
// pane draws, so the comparison is exact.
func (m Model) atLocalFloor() bool { return len(m.msgsBase) < m.msgLimit }

// historyFloorMs is how far back the open chat has been pulled, 0 once the
// whole of it is stored.
func (m Model) historyFloorMs() int64 {
	if i := indexOfChat(m.chats, m.chatID); i >= 0 {
		return m.chats[i].HistoryFloorMs
	}
	return 0
}

// floorRow heads the page with why it stops here.
func (m Model) floorRow(w int) msgRow {
	label := floorLabel
	switch {
	case m.historyFloorMs() == 0:
		label = startLabel
	case m.deps.Syncer == nil:
		label = readOnlyLabel
	case m.msgPullInFlight:
		label = fetchingLabel
	}
	return msgRow{text: daySeparator(label, w), plain: true}
}

// growMessages reaches for what lies above a page the reader has scrolled to
// the top of: another page from the store, or, once the store is spent, one
// from Feishu. The Feishu client loads older messages on reaching the top
// rather than on a key, so this does too.
//
// Nothing marks a widening as in flight: the wider limit is itself the guard,
// because atLocalFloor holds from the moment it is raised until the wider page
// lands. A stream of wheel events therefore asks exactly once.
func (m *Model) growMessages() tea.Cmd {
	if m.searching || m.msgTop > 0 {
		return nil
	}
	if m.atLocalFloor() {
		return m.pullOlder()
	}
	if m.msgSince > 0 {
		// An anchored page runs from a search hit to now, so what lies older
		// is before the anchor rather than behind the limit. Dropping the
		// anchor for a count that covers the page already drawn keeps every
		// row on screen where it is and adds one page above them.
		m.msgSince, m.msgLimit = 0, len(m.msgsBase)+messagePageSize
	} else {
		m.msgLimit += messagePageSize
	}
	return loadMessages(m.deps, m.chatID, m.msgSince, m.msgLimit)
}

// pullOlder asks Feishu for the page behind the store's own floor. Unlike
// widening the limit this leaves nothing on the model to make it self-limiting
// — the answer is a write to the database, which comes back as a revision
// rather than as a page — so an explicit flag is what stops a stream of wheel
// events becoming a stream of calls.
func (m *Model) pullOlder() tea.Cmd {
	if m.deps.Syncer == nil || m.msgPullInFlight || m.historyFloorMs() == 0 {
		return nil
	}
	m.msgPullInFlight = true
	d, chatID := m.deps, m.chatID
	return func() tea.Msg {
		ctx, cancel := beat(chatPollTimeout)
		defer cancel()
		_, err := d.Syncer.PullOlder(ctx, chatID)
		return olderPulledMsg{chatID: chatID, err: err}
	}
}

// olderPulledMsg closes one PullOlder. The messages it stored arrive by way of
// the revision watch like any other write, so nothing here carries them.
type olderPulledMsg struct {
	chatID string
	err    error
}

// noteOlderPull frees the reader to ask again. A pull for a chat since left
// answers for a page nobody is looking at, so only the open chat's clears the
// flag — the chat it belongs to already cleared it on the way out.
//
// The reader asked for this one, unlike the background beat, so a refusal says
// so on the status line rather than only in the log.
func (m *Model) noteOlderPull(msg olderPulledMsg) tea.Cmd {
	if msg.chatID != m.chatID {
		return nil
	}
	m.msgPullInFlight = false
	if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
		m.deps.log().Warn("pull older", "chat_id", msg.chatID, "err", msg.err)
		*m = m.notify("could not reach further back: "+msg.err.Error(), true)
		return nil
	}
	// The floor is not one of the columns the revision trigger watches, so a
	// pull that moved it and nothing else would leave this head still offering
	// history that is no longer there. The chats carry the floor, so re-read
	// them rather than wait for an unrelated write to repaint the pane.
	return loadChats(m.deps)
}
