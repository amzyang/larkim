package sync

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	stdsync "sync"
	"time"

	"github.com/amzyang/larkim/internal/oplog"
	"github.com/amzyang/larkim/larkcli"
)

// livePage is how much of the active-time ordering discovery reads. A chat
// that has just seen a message is at the top of it however long it is, and
// the gateway answers 30 chats about a tenth of a second sooner than 100.
const livePage = 30

// discover finds the messages that have just been sent to the user: it reads
// the chat list's active-time ordering and lists the chats ActiveDelta names.
// It reads chat state rather than the search index, so it names a chat
// seconds before the search can find the message, and listing the chat has no
// such lag either. It waits for every listing, which is what a one-shot tick
// wants; the loop Run keeps goes through scoutOnce instead.
func (s *Syncer) discover(ctx context.Context, now time.Time) (moved, probed int, err error) {
	prev, order, err := s.activeProbe(ctx, now)
	if err != nil {
		return 0, 0, fmt.Errorf("active probe: %w", err)
	}
	named := ActiveDelta(prev, order)
	fresh, errs := fanOut(ctx, named, func(ctx context.Context, id string) (int, error) {
		return s.pullNamed(ctx, id, now)
	})
	for _, n := range fresh {
		probed += n
	}
	if err := firstFailure(errs); err != nil {
		return len(named), probed, fmt.Errorf("active probe pull: %w", err)
	}
	// Recording the ordering is what consumes the delta: once written, the
	// chats it named are no longer named. So it waits for the pull, and a
	// failed pull leaves them named next cycle rather than dropping them on
	// the safety net half a minute out. Most cycles see the ordering they
	// already recorded, and write nothing.
	if !slices.Equal(prev, order) {
		if err := s.setActiveOrder(ctx, order); err != nil {
			return len(named), probed, fmt.Errorf("active order: %w", err)
		}
	}
	return len(named), probed, nil
}

// pullNamed lists one chat discovery named and puts what it brings on screen:
// its bodies as the pull lands them, then the renderings Feishu has to be
// asked for, then a nudge to the sweep for the rest.
func (s *Syncer) pullNamed(ctx context.Context, id string, now time.Time) (int, error) {
	_, fresh, err := s.pullFromCursor(ctx, []string{id}, "active probe", now)
	if err != nil || fresh == 0 {
		return fresh, err
	}
	// pullFromCursor rendered the bodies as it landed them.
	n, err := s.renderRemote(ctx, id, hotRenderBatch, now)
	s.changed(n)
	s.wakeSweep()
	return fresh, err
}

// scout is what the discovery loop keeps between cycles, now that a chat's
// listing runs on its own rather than inside the cycle that named it: which
// chats are being listed, which ones a failed listing left owed, and the
// failure the loop has yet to back off for.
type scout struct {
	now      func() time.Time
	delay    func(err error, failures int) time.Duration
	mu       stdsync.Mutex
	inflight map[string]bool
	owed     map[string]owing
	err      error
	wg       stdsync.WaitGroup
}

// owing is what a chat whose listing failed is owed: how many times in a row
// it has failed, and when it may be listed again. Without the wait, a chat
// failing for a reason of its own, such as a listing the gateway refuses,
// would be relisted every cycle for as long as it keeps failing, since the
// loop's own backoff counts the cycles that reported the failure rather than
// the ones that caused it. A timeout leaves the count alone and the chat on
// the usual pace: every one seen so far was a TLS handshake, which is the
// network rather than the chat, and the network is what the loop's own
// timeoutRun watches through the probe.
type owing struct {
	failures int
	dueAt    time.Time
}

func newScout(now func() time.Time, delay func(error, int) time.Duration) *scout {
	return &scout{now: now, delay: delay, inflight: map[string]bool{}, owed: map[string]owing{}}
}

// plan is the chats to list this cycle: the ones the ordering names plus the
// ones a failed listing owes, less the ones still resting. A resting chat
// stays owed, so its own wait is what brings it back rather than the ordering.
func (sc *scout) plan(named []string) []string {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	ids := UniqueStrings(append(named, slices.Sorted(maps.Keys(sc.owed))...))
	now := sc.now()
	return slices.DeleteFunc(ids, func(id string) bool {
		return now.Before(sc.owed[id].dueAt)
	})
}

// scoutOnce reads the ordering and sets a listing going for every chat it
// names that is not being listed already or resting after a failure, then
// returns without waiting for them: a slow chat holds up nobody but itself,
// and the next probe goes out at once. The ordering is recorded straight away,
// so a chat whose listing fails is owed a later cycle instead, and its failure
// is returned before that cycle probes, which is when the loop backs off.
func (s *Syncer) scoutOnce(ctx context.Context, sc *scout, now time.Time) (named int, err error) {
	if err := sc.failure(); err != nil {
		return 0, err
	}
	prev, order, err := s.activeProbe(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("active probe: %w", err)
	}
	ids := sc.plan(ActiveDelta(prev, order))
	for _, id := range ids {
		sc.launch(id, func() (int, error) {
			n, err := s.pullNamed(ctx, id, now)
			if n > 0 {
				s.log().InfoContext(ctx, "discovery", "chat_id", id, "probed", n)
			}
			if transientFailure(err) {
				s.log().InfoContext(ctx, "discovery timed out", "chat_id", id, "err", err)
			}
			return n, err
		})
	}
	if !slices.Equal(prev, order) {
		if err := s.setActiveOrder(ctx, order); err != nil {
			return len(ids), fmt.Errorf("active order: %w", err)
		}
	}
	return len(ids), nil
}

// launch runs pull for chat id unless one is already running for it. A pull
// that fails owes the chat a listing once its wait is up; a cancelled one owes
// nothing, since the loop is ending.
func (sc *scout) launch(id string, pull func() (int, error)) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.inflight[id] {
		return
	}
	sc.inflight[id] = true
	sc.wg.Go(func() {
		_, err := pull()
		sc.mu.Lock()
		defer sc.mu.Unlock()
		delete(sc.inflight, id)
		switch {
		case err == nil:
			delete(sc.owed, id)
		case !errors.Is(err, context.Canceled):
			owed := sc.owed[id]
			if transientFailure(err) {
				// Empty cycle for this chat; try again on the usual pace.
				owed.dueAt = sc.now().Add(sc.delay(nil, 0))
			} else {
				owed.failures++
				// From the failure rather than from the launch: a listing that
				// takes its whole timeout to fail would otherwise come due the
				// moment it failed.
				owed.dueAt = sc.now().Add(sc.delay(err, owed.failures))
				sc.err = cmp.Or(sc.err, err)
			}
			sc.owed[id] = owed
		}
	})
}

// failure hands over the first failure since the last call, once.
func (sc *scout) failure() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	err := sc.err
	sc.err = nil
	return err
}

// activeProbe reads the active-time ordering of the chat list's first page,
// and the ordering the last probe recorded. Feishu puts a chat that just
// received a message at position 1, so the first page is complete for this
// purpose however many chats there are.
func (s *Syncer) activeProbe(ctx context.Context, now time.Time) (prev, order []string, err error) {
	chats, err := s.activeChats(ctx, livePage, now)
	if err != nil {
		return nil, nil, err
	}
	order = chatIDs(chats)
	raw, ok, err := s.Store.GetState(ctx, KeyActiveOrder)
	if err != nil {
		return nil, nil, err
	}
	if ok && raw != "" {
		// A hand-edited or half-written value would otherwise fail every
		// cycle; treating it as a first run costs one cycle of blindness.
		if err := json.Unmarshal([]byte(raw), &prev); err != nil {
			s.log().WarnContext(ctx, "active order unreadable", "err", err)
			prev = nil
		}
	}
	return prev, order, nil
}

// setActiveOrder records the ordering the next cycle compares against.
func (s *Syncer) setActiveOrder(ctx context.Context, order []string) error {
	enc, err := json.Marshal(order)
	if err != nil {
		return err
	}
	return s.Store.SetState(ctx, KeyActiveOrder, string(enc))
}

func chatIDs(chats []larkcli.RawChat) []string {
	ids := make([]string, len(chats))
	for i, c := range chats {
		ids[i] = c.ChatID
	}
	return ids
}

// runDiscovery runs discover until ctx ends, on a lane of its own: the sweeps
// fan out wide, and a new message waiting behind one of them is the delay this
// loop exists to remove. While the sweep reports the user logged out it makes
// no call, since the sweep is what asks whether a login is back.
func (s *Syncer) runDiscovery(ctx context.Context) {
	ctx = larkcli.WithLane(ctx, larkcli.LaneDiscovery)
	sc := newScout(s.now, s.delayFor)
	defer sc.wg.Wait()
	failures := 0
	var timeouts timeoutRun
	for {
		cycle := oplog.With(ctx, "discover")
		loggedOut, err := s.loggedOut(cycle)
		var moved int
		if err == nil && !loggedOut {
			moved, err = s.scoutOnce(cycle, sc, s.now())
		}
		if ctx.Err() != nil {
			return
		}
		if err = timeouts.judge(err, s.now()); excused(err) {
			// An empty cycle, on the usual pace.
			s.log().InfoContext(cycle, "discovery timed out", "err", err)
			err = nil
		}
		wake := s.attend
		if err != nil {
			failures++
			// Somebody coming back is no reason to retry an API that just
			// refused.
			wake = nil
		} else {
			failures = 0
		}
		pause := s.discoveryPause(loggedOut, err, failures)
		switch {
		case err != nil:
			s.log().WarnContext(cycle, "discovery failed", "err", err, "class", errClass(err), "failures", failures, "retry_in", pause)
			if s.OnError != nil {
				s.OnError(err)
			}
		case !loggedOut && moved > 0:
			s.log().DebugContext(cycle, "discovery", "moved", moved)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-time.After(pause):
		}
	}
}

// attendedDiscoveryPause is the floor between cycles while somebody is
// looking. A cycle is already a round trip, but with no floor the discovery
// lane fills as fast as Feishu answers, which is what produces 2200 on the
// chats that sit in the active head.
const attendedDiscoveryPause = 400 * time.Millisecond

// discoveryPause is how long discovery rests before its next cycle. With
// somebody looking it only takes the floor: a cycle is already a round trip
// or two, which is most of a new message's wait. With nobody looking, or no
// login to call with, it keeps the sweep's pace; after a failure, the sweep's
// backoff.
func (s *Syncer) discoveryPause(loggedOut bool, err error, failures int) time.Duration {
	switch {
	case err != nil:
		return s.delayFor(err, failures)
	case loggedOut || !s.attended.Load():
		return s.Opt().PollInterval
	}
	return attendedDiscoveryPause
}

// SetAttended tells discovery whether somebody is looking at what it finds.
// While they are, it runs cycle after cycle with only the attended floor
// between them; while they are not, it keeps the sweep's pace, since a
// message nobody is looking at is no later for landing a second after it was
// sent. Coming back starts a cycle at once. Only the process running Run
// discovers, so a TUI beside a daemon says it to nobody.
func (s *Syncer) SetAttended(on bool) {
	s.signals()
	if s.attended.Swap(on) || !on {
		return
	}
	select {
	case s.attend <- struct{}{}:
	default:
	}
}

// Attended reports what SetAttended last said.
func (s *Syncer) Attended() bool { return s.attended.Load() }

// loggedOut reports whether the last tick found no user token to call with.
func (s *Syncer) loggedOut(ctx context.Context) (bool, error) {
	status, _, err := s.Store.GetState(ctx, KeyStatus)
	return status == StatusNeedsLogin, err
}

// wakeSweep ends the sweep's pause, so what follows a new message — its
// pictures, whether it has been read — comes straight after it rather than a
// pause later.
func (s *Syncer) wakeSweep() {
	s.signals()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Wake ends the sweep's pause from outside, for a change the next tick is
// what applies — a silence rule edited in the TUI the sweep runs in. A sweep
// backing off after a failure is not woken: its pause is waiting on the API,
// not on the reader.
func (s *Syncer) Wake() { s.wakeSweep() }

// signals makes the channels the two loops wake each other on. It runs on
// first use rather than in a constructor, so a Syncer built as a literal has
// them before either loop waits on one.
func (s *Syncer) signals() {
	s.signalOnce.Do(func() {
		s.wake = make(chan struct{}, 1)
		s.attend = make(chan struct{}, 1)
	})
}
