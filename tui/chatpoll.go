package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/larkcli"
)

// chatPollEvery paces the one chat the reader is looking at. Discovery for
// every other chat goes through messages/search, whose index runs about seven
// seconds behind; the listing this poll uses reads the message store and has
// no such lag, so the beat is very nearly the latency the reader sees.
const chatPollEvery = 1500 * time.Millisecond

// chatPollTimeout is generous because the beat, not the deadline, is what
// bounds how often a call is made: a slow call costs a skipped beat.
const chatPollTimeout = 30 * time.Second

// chatPollBackoff is how long a rate-limited poll stands down for. It matches
// what the syncer takes on the same answer, so the two paths do not take turns
// provoking the gateway.
const chatPollBackoff = 30 * time.Second

// chatPollDueMsg is one beat of the chain armed in Init.
type chatPollDueMsg struct{}

// chatPolledMsg carries a finished poll back so the next beat may start.
type chatPolledMsg struct{ err error }

// scheduleChatPoll arms the next beat. The chain is armed once and re-arms
// unconditionally: arming it per chat-open would stack one chain per visit.
func scheduleChatPoll() tea.Cmd {
	return tea.Tick(chatPollEvery, func(time.Time) tea.Msg { return chatPollDueMsg{} })
}

// claimChatPoll names the chat worth a call this beat, "" when none is. A
// blurred terminal catches up when the reader comes back to it.
func (m *Model) claimChatPoll(now time.Time) string {
	if m.chatPollInFlight || !m.focused || now.Before(m.chatPollPausedUntil) {
		return ""
	}
	return m.openingChat()
}

// pollChat re-lists the open chat straight from the message store. It takes
// the beat lane: the words on screen are what it fetches, so queueing it
// behind an attachment download on the background lane would reintroduce the
// delay it exists to remove.
func pollChat(d Deps, chatID, threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := beat(chatPollTimeout)
		defer cancel()
		_, err := d.Syncer.RefreshChat(ctx, chatID, threadID)
		return chatPolledMsg{err}
	}
}

// rideAlong is the refresh that takes this beat's turn beside the listing.
// Neither a read receipt nor a reaction moves a message's update_time, so the
// listing alone never brings them; taking turns keeps both about three seconds
// fresh for one extra call a beat, which the beat lane is wide enough to hold
// beside the listing.
func (m Model) rideAlong(chatID string) tea.Cmd {
	if m.chatPollBeat%2 == 0 {
		return refreshReadStatus(m.deps, chatID)
	}
	return refreshReactions(m.deps, chatID)
}

// notePollResult clears the in-flight flag and stands the beat down when the
// gateway refuses. Nobody asked for this poll, so a failure belongs in the log
// rather than the notice bar.
func (m *Model) notePollResult(err error, now time.Time) {
	m.chatPollInFlight = false
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	m.deps.Log.Warn("chat poll", "chat_id", m.openingChat(), "err", err)
	if le, ok := errors.AsType[*larkcli.Error](err); ok && le.IsRateLimit() {
		m.chatPollPausedUntil = now.Add(max(le.RetryAfter, chatPollBackoff))
	}
}
