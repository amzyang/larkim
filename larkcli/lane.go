package larkcli

import "context"

// Lane is which line of subprocesses a call takes. Background is the syncer's
// sweeps, which nobody is waiting on; Interactive is what a person is waiting
// on. The daemon ticks every few seconds, so one shared line is occupied more
// often than not, and a keystroke behind a sweep waits out the whole sweep.
type Lane int

const (
	// LaneBackground is the zero value, so a caller that says nothing cannot
	// take the fast line by accident.
	LaneBackground Lane = iota
	LaneInteractive
)

func (l Lane) String() string {
	if l == LaneInteractive {
		return "interactive"
	}
	return "background"
}

type laneKey struct{}

// WithLane marks ctx as belonging to a lane. The mark rides on the context so
// a call reaches its lane through Syncer and Client alike without either of
// them carrying the choice in its signature.
func WithLane(ctx context.Context, l Lane) context.Context {
	return context.WithValue(ctx, laneKey{}, l)
}

// LaneOf reads the mark back, so a caller can check that the context it built
// for a call really takes the line it meant.
func LaneOf(ctx context.Context) Lane {
	l, _ := ctx.Value(laneKey{}).(Lane)
	return l
}

// lane admits at most cap(l) subprocesses at once.
type lane chan struct{}

// acquire waits for room or for ctx to end. A sync.Mutex cannot be waited on
// with a deadline, which is what left a cancelled call pinned behind a sweep
// with no way out of its own.
func (l lane) acquire(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l lane) release() { <-l }

// Lane widths. Background is one because the sweeps are throughput-bound and
// the gateway rate limit is per user, so a second sweep buys nothing.
// Interactive is two so that sending a message does not queue behind a search
// the reader is still typing.
const (
	backgroundLane  = 1
	interactiveLane = 2
)
