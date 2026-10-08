package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

// A push that lands while the last clear is still in flight must still fire.
// The chain's own tick saw an empty queue by then and stopped, so the push
// alone is what has to arm the next one — through pushClears, whose armed
// guard relies on that stop having disarmed the queue.
func TestClearQueue_PushWhileLastClearInFlightStillClears(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	release := make(chan struct{})
	var fired []string
	m := New(Deps{Store: st, ClearBadge: func(_ context.Context, c store.ChatUnread) error {
		if c.ChatID == "oc_a" {
			<-release
		}
		fired = append(fired, c.ChatID)
		return nil
	}})
	m.width, m.height = 120, 36

	// Queue one chat and fire it; its clear stays in flight.
	m, _ = m.pushClears([]store.ChatUnread{{ChatID: "oc_a", Position: 1}})
	nm, out := m.Update(clearDueMsg{gen: m.clears.gen})
	m = nm.(Model)
	batch := out().(tea.BatchMsg)
	fireA, tick := batch[0], batch[1]

	// The next due lands on an empty-but-inflight queue and stops the chain.
	nm, out = m.Update(tick().(clearDueMsg))
	m = nm.(Model)
	require.Nil(t, out)

	// The reader opens another unread chat while the first clear is in flight.
	m, pushed := m.pushClears([]store.ChatUnread{{ChatID: "oc_b", Position: 1}})

	// The first clear settles; whatever the model still owes must clear the
	// chat behind it.
	close(release)
	firedMsg, ok := fireA().(clearFiredMsg)
	require.True(t, ok)
	nm, out = m.Update(firedMsg)
	m = nm.(Model)
	m = drain(t, m, tea.Batch(out, pushed))
	require.Contains(t, fired, "oc_a")
	require.Contains(t, fired, "oc_b")
}

// A chat already queued takes the newer watermark rather than a second slot:
// queued at the old one, it would settle short of the message that raised it,
// and nothing would ask for that message again.
func TestPushClears_AQueuedChatTakesTheNewerWatermark(t *testing.T) {
	t.Parallel()
	m := sized(120, 36)
	m.clears.armed = true

	m, _ = m.pushClears([]store.ChatUnread{{ChatID: "oc_a", Position: 1}})
	m, _ = m.pushClears([]store.ChatUnread{{ChatID: "oc_a", Position: 5}, {ChatID: "oc_b", Position: 2}})
	m, _ = m.pushClears([]store.ChatUnread{{ChatID: "oc_a", Position: 3}})

	require.Equal(t, []store.ChatUnread{{ChatID: "oc_a", Position: 5}, {ChatID: "oc_b", Position: 2}}, m.clears.left)
}
