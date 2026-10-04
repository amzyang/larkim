package tui

import (
	"context"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// clearQueue keeps the read gate and mark-all in a single line, so a sweep's
// requests reach the gateway paced rather than as one burst.
//
// It delays; it never drops. The number of clears is still the number of
// unread messages (docs/read-sync/PRD.md). A cooling window would drop the
// ones that landed inside it, leaving messages markChatRead settles quietly
// and a dot only the next sweep would find — which is the thing that
// paragraph rules out.
type clearQueue struct {
	// left is the chats still to be cleared, each with the message to land on.
	// The reader's own chat ends up last, so the client comes to rest where
	// the terminal is.
	left []store.ChatUnread
	// gen invalidates the ticks of a queue that was cleared: a chain cannot
	// be cancelled once armed, so the message it delivers is what gets
	// dropped.
	gen int
	// armed says a tick is already in flight, so a push joins the chain
	// running rather than starting a second one.
	armed bool
	// swept and failed are what a mark-all reports when the queue runs dry,
	// and swept is also what says a sweep is the thing running. A single
	// chat from the read gate says nothing on its way out: it was never a
	// request to open anything, and an error banner there would blame the
	// reader's navigation for a dot only the client still draws.
	swept, failed int
	// inflight counts the clears that have been fired and not yet answered.
	// One routinely outlives the tick that started it, so the queue running
	// dry is not the sweep being over — the last failures are still on their
	// way.
	inflight int
}

// clearDueMsg advances the chain by one chat.
type clearDueMsg struct{ gen int }

// clearFiredMsg closes one clear, so the failures can be counted while the
// chain keeps going.
type clearFiredMsg struct {
	gen int
	err error
}

// pushClears queues chats to clear and arms the chain if it is not already
// running. A chat already waiting is not queued twice: the gateway only has
// to be told once, however many times it was asked.
func (m Model) pushClears(chats []store.ChatUnread) (Model, tea.Cmd) {
	for _, c := range chats {
		if slices.ContainsFunc(m.clears.left, func(q store.ChatUnread) bool { return q.ChatID == c.ChatID }) {
			continue
		}
		m.clears.left = append(m.clears.left, c)
	}
	if m.clears.armed || len(m.clears.left) == 0 {
		return m, nil
	}
	m.clears.armed = true
	return m, m.clearTick(m.clears.gen)
}

// dropClears drops whatever is still queued. The write that matters has
// already landed; what is abandoned is the client's own dots, which the
// read-status poller reports on either way.
func (m Model) dropClears() Model {
	m.clears = clearQueue{gen: m.clears.gen + 1}
	return m
}

func (m Model) clearTick(gen int) tea.Cmd {
	return tea.Tick(clearPace, func(time.Time) tea.Msg { return clearDueMsg{gen} })
}

// onClearDue fires the chat at the head and arms the next slot. The clear
// and the tick go out together, so the pace is measured between two clears
// starting — the wait on one is part of the gap.
func (m Model) onClearDue(msg clearDueMsg) (Model, tea.Cmd) {
	if msg.gen != m.clears.gen {
		return m, nil
	}
	if len(m.clears.left) == 0 {
		if m.clears.inflight > 0 {
			return m, nil
		}
		return m.closeSweep(), nil
	}
	next := m.clears.left[0]
	m.clears.left = m.clears.left[1:]
	m.clears.inflight++
	return m, tea.Batch(fireBadgeClear(m.deps, next, msg.gen), m.clearTick(msg.gen))
}

// onClearFired counts one hand-over. The chain does not stop on a failure:
// the chats behind it have dots of their own, and nothing about this one says
// the next will fail too.
func (m Model) onClearFired(msg clearFiredMsg) (Model, tea.Cmd) {
	if msg.gen != m.clears.gen {
		return m, nil
	}
	m.clears.inflight--
	if msg.err != nil {
		m.clears.failed++
	}
	if len(m.clears.left) == 0 && m.clears.inflight == 0 {
		return m.closeSweep(), nil
	}
	if m.clears.swept > 0 && len(m.clears.left) > 0 {
		return m.notify(sweepNote(len(m.clears.left)), false), nil
	}
	return m, nil
}

// sweepNote is what a mark-all says while its queue drains.
func sweepNote(left int) string {
	return "clearing Feishu badges… " + strconv.Itoa(left) + " left"
}

// closeSweep reports what a mark-all managed once the queue has run dry. The
// counters are zeroed whether or not there is anything to say, so the read
// gate's own failures leave nothing behind for the next mark-all to report as
// its own.
func (m Model) closeSweep() Model {
	m.clears.armed = false
	swept, failed := m.clears.swept, m.clears.failed
	m.clears.swept, m.clears.failed = 0, 0
	if swept == 0 {
		return m
	}
	note := strconv.Itoa(swept) + " chats marked read"
	isErr := failed > 0
	if isErr {
		note += ", " + strconv.Itoa(failed) + " not cleared in Feishu"
	}
	return m.notify(note, isErr)
}

// fireBadgeClear drops one chat's red dot and accepts the receipt on success.
func fireBadgeClear(d Deps, c store.ChatUnread, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		err := d.ClearBadge(ctx, c)
		if err != nil {
			d.Log.Warn("clear feishu badge", "chat_id", c.ChatID, "err", err)
			return clearFiredMsg{gen: gen, err: err}
		}
		if _, err := d.Store.AcceptRemoteRead(ctx, c.ChatID, c.Position); err != nil {
			d.Log.Warn("accept remote read", "chat_id", c.ChatID, "err", err)
			return clearFiredMsg{gen: gen, err: err}
		}
		return clearFiredMsg{gen: gen}
	}
}
