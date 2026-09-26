package tui

import (
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
)

// applinkQueue owns the pace the Feishu desktop client is walked at. Two
// things ask for a chat to be opened — the read gate, one chat at a time, and
// a mark-all sweep, every chat at once — and the client can only be in one of
// them, so firing from both at once loses whichever navigation was in flight.
//
// It delays; it never drops. The number of applinks is still the number of
// unread messages (docs/read-sync/PRD.md). A cooling window would drop the
// ones that landed inside it, leaving messages markChatRead settles quietly
// and a dot the client never takes down — which is the thing that paragraph
// rules out.
type applinkQueue struct {
	// left is the chats still to be walked onto, each with the message to
	// land on. The reader's own chat ends up last, so the client comes to
	// rest where the terminal is.
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
	// inflight counts the opens that have been fired and not yet answered.
	// applink.Open waits on the process, which routinely outlives the tick
	// that started it, so the queue running dry is not the sweep being over
	// — the last failures are still on their way.
	inflight int
}

// applinkDueMsg advances the chain by one chat.
type applinkDueMsg struct{ gen int }

// applinkFiredMsg closes one applink, so the failures can be counted while
// the chain keeps going.
type applinkFiredMsg struct {
	gen int
	err error
}

// pushApplinks queues chats to walk the client onto and arms the chain if it
// is not already running. A chat already waiting is not queued twice: the
// client only has to arrive once, however many times it was asked.
func (m Model) pushApplinks(chats []store.ChatUnread) (Model, tea.Cmd) {
	for _, c := range chats {
		if slices.ContainsFunc(m.applinks.left, func(q store.ChatUnread) bool { return q.ChatID == c.ChatID }) {
			continue
		}
		m.applinks.left = append(m.applinks.left, c)
	}
	if m.applinks.armed || len(m.applinks.left) == 0 {
		return m, nil
	}
	m.applinks.armed = true
	return m, applinkTick(m.applinks.gen)
}

// clearApplinks drops whatever is still queued. The write that matters has
// already landed; what is abandoned is the client's own dots, which the
// read-status poller reports on either way.
func (m Model) clearApplinks() Model {
	m.applinks = applinkQueue{gen: m.applinks.gen + 1}
	return m
}

func applinkTick(gen int) tea.Cmd {
	return tea.Tick(applink.Pace, func(time.Time) tea.Msg { return applinkDueMsg{gen} })
}

// onApplinkDue fires the chat at the head and arms the next slot. The open
// and the tick go out together, so the pace is measured between two opens
// starting — applink.Open waits on the process, and that wait is part of the
// gap the client needs.
func (m Model) onApplinkDue(msg applinkDueMsg) (Model, tea.Cmd) {
	if msg.gen != m.applinks.gen {
		return m, nil
	}
	if len(m.applinks.left) == 0 {
		if m.applinks.inflight > 0 {
			return m, applinkTick(msg.gen)
		}
		return m.closeSweep(), nil
	}
	next := m.applinks.left[0]
	m.applinks.left = m.applinks.left[1:]
	m.applinks.inflight++
	return m, tea.Batch(fireApplink(m.deps, next, msg.gen), applinkTick(msg.gen))
}

// onApplinkFired counts one hand-over. The chain does not stop on a failure:
// the chats behind it have dots of their own, and nothing about this one says
// the next will fail too.
func (m Model) onApplinkFired(msg applinkFiredMsg) (Model, tea.Cmd) {
	if msg.gen != m.applinks.gen {
		return m, nil
	}
	m.applinks.inflight--
	if msg.err != nil {
		m.applinks.failed++
	}
	if m.applinks.swept > 0 && len(m.applinks.left) > 0 {
		return m.notify(sweepNote(len(m.applinks.left)), false), nil
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
	m.applinks.armed = false
	swept, failed := m.applinks.swept, m.applinks.failed
	m.applinks.swept, m.applinks.failed = 0, 0
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

// fireApplink walks the client onto one chat without taking the screen, which
// is what makes it send the read receipt Feishu offers no API for. It is the
// only lever larkim has on the client's own red dot, and it is best effort:
// local_read_at has already dropped the badge drawn here.
func fireApplink(d Deps, c store.ChatUnread, gen int) tea.Cmd {
	return func() tea.Msg {
		err := d.OpenURL([]string{applink.ChatLink(c.ChatID, c.Position)}, true)
		if err != nil {
			d.Log.Warn("clear feishu badge", "chat_id", c.ChatID, "err", err)
		}
		return applinkFiredMsg{gen: gen, err: err}
	}
}
