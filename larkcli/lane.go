package larkcli

import "context"

// Lane is which line of subprocesses a call takes. Background is the syncer's
// sweeps, which nobody is waiting on; Beat is the timer-driven refresh of what
// is already on screen; Interactive is what a person pressed a key for. The
// daemon ticks every few seconds and the open chat beats every 1.5s, so a
// keystroke sharing either line waits out whatever that line is doing.
type Lane int

const (
	// LaneBackground is the zero value, so a caller that says nothing cannot
	// take a faster line by accident.
	LaneBackground Lane = iota
	LaneBeat
	LaneInteractive
)

func (l Lane) String() string {
	switch l {
	case LaneInteractive:
		return "interactive"
	case LaneBeat:
		return "beat"
	default:
		return "background"
	}
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

// Lane widths, which are this program's only rate control: lark-cli has no
// client-side limiter, so whatever a lane admits reaches the gateway. Feishu
// meters per API per app per tenant rather than per user, and the tier the IM
// endpoints sit in allows far more than these widths can produce, so the
// background line is sized for the fan-out of the per-chat pulls rather than
// held at one. Beat holds the open chat's listing, the threads it follows and
// the refresh riding along with it; interactive is left free for the person.
const (
	backgroundLane  = 4
	beatLane        = 3
	interactiveLane = 3
)
