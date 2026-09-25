package tui

import (
	"maps"
	"slices"
	"time"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
)

// reactTimeout is how long a press waits for Feishu. Taking a reaction back
// costs three calls, so it is generous.
const reactTimeout = 30 * time.Second

// reactSettle is how long a press is drawn ahead of Feishu's answer. It
// outlasts the call the press made, so a slow round trip is not taken off the
// strip while it is still on the wire; past it the stored summary is the
// truth even where it disagrees, because a reaction can also arrive from a
// client this process never hears from.
const reactSettle = 45 * time.Second

// reactPending is one press of a reaction Feishu has not answered yet: which
// emoji, and whether the press put it on or took it back.
//
// seq names the press itself. A rollback goes by it rather than by the emoji,
// so a press that failed cannot take back whichever press now stands in its
// place.
type reactPending struct {
	seq       int64
	messageID string
	// emojiType is the spelling Feishu itself uses, which is what goes back
	// over the wire: emoji_type is case-sensitive there and most of the keys
	// are not upper case, so the folded form is rejected outright. It is kept
	// exactly as the press handed it over, because a chip may carry a skin
	// tone or a key this build has no entry for and resolving it again would
	// take back a different reaction than the one on the message.
	emojiType string
	key       string // folded, which is how a chip's key is matched
	on        bool
	at        time.Time
	// answered marks a press Feishu has taken. React brings the summary up to
	// date before it returns, so from that point the store is the truth and
	// the press is held only until a reload has drawn it.
	answered bool
}

// reactStates keys the presses still on their way by the message they were
// made on, which is how the renderer lays them over the stored summary.
func (m Model) reactStates() map[string]map[string]bool {
	if len(m.reacts) == 0 {
		return nil
	}
	out := make(map[string]map[string]bool, len(m.reacts))
	for _, p := range m.reacts {
		if out[p.messageID] == nil {
			out[p.messageID] = map[string]bool{}
		}
		out[p.messageID][p.key] = p.on
	}
	return out
}

// pressReaction records a press against the message and returns it, so the
// caller can hand the same record to the command that sends it. Pressing the
// same emoji again replaces the record rather than queueing behind it: the
// last press is what the reader means.
func (m *Model) pressReaction(x store.Message, key string) reactPending {
	folded := emoji.Fold(key)
	m.reactSeq++
	p := reactPending{seq: m.reactSeq, messageID: x.MessageID, emojiType: key, key: folded,
		on: !mineOn(m.drawnChips(x), folded), at: time.Now()}
	m.reacts = slices.DeleteFunc(m.reacts, func(q reactPending) bool {
		return q.messageID == p.messageID && q.key == p.key
	})
	m.reacts = append(m.reacts, p)
	return p
}

// dropReact takes one press back off the strip.
func (m *Model) dropReact(seq int64) {
	m.reacts = slices.DeleteFunc(m.reacts, func(p reactPending) bool { return p.seq == seq })
}

// answerReact marks the press Feishu has taken, so the next reload retires it.
func (m *Model) answerReact(seq int64) {
	if i := slices.IndexFunc(m.reacts, func(p reactPending) bool { return p.seq == seq }); i >= 0 {
		m.reacts[i].answered = true
	}
}

// drawnChips is the message's strip as it stands on screen, which is what the
// next press is measured against: pressing twice in a row is on and then off
// even while the first press is still on the wire.
func (m Model) drawnChips(x store.Message) []emoji.Chip {
	return pendingChips(emoji.Summary(x.ReactionsJSON, m.deps.Self), m.reactStates()[x.MessageID], m.deps.Self)
}

// mineOn reports whether the reader's own reaction stands under key. It is
// asked of the drawn strip when deciding which way the next press goes, and
// of the stored summary when deciding whether a press has been caught up
// with — the same question against two different sets of chips.
func mineOn(chips []emoji.Chip, key string) bool {
	key = emoji.Fold(key)
	return slices.ContainsFunc(chips, func(c emoji.Chip) bool {
		return c.Mine && emoji.Fold(c.Key) == key
	})
}

// pendingChips is the strip as the reader's own presses have left it. A press
// draws at once: a chip that only moved once the round trip came back reads as
// a press that did not land, and the reader presses again.
//
// A press on an emoji nobody used yet opens a chip at the end of the strip,
// which is where Feishu's own ordering by first use will put it; a press that
// empties one closes it.
func pendingChips(chips []emoji.Chip, want map[string]bool, self string) []emoji.Chip {
	if len(want) == 0 {
		return chips
	}
	out := make([]emoji.Chip, 0, len(chips)+len(want))
	seen := make(map[string]bool, len(want))
	for _, c := range chips {
		key := emoji.Fold(c.Key)
		seen[key] = true
		on, pressed := want[key]
		// A press the strip already reflects is one the store has caught up
		// with; drawing it again would count the reader twice.
		if !pressed || on == c.Mine {
			out = append(out, c)
			continue
		}
		if c = pressChip(c, on, self); c.Count > 0 {
			out = append(out, c)
		}
	}
	// A press on an emoji the strip does not carry yet opens its chip. Only
	// an added one does: taking back what is not there is nothing to draw.
	// Sorted, so two presses waiting together draw in the same order on every
	// frame rather than swapping places as the map is walked.
	for _, key := range slices.Sorted(maps.Keys(want)) {
		if want[key] && !seen[key] {
			out = append(out, emoji.Chip{Key: key, Count: 1, Mine: true, Operators: []string{self}})
		}
	}
	return out
}

// pressChip is one chip with the reader added or taken off it. The reader joins
// the reactors at the end, where the press belongs: Feishu names them earliest
// first, so putting the newest one in front would move it again on the reload.
func pressChip(c emoji.Chip, on bool, self string) emoji.Chip {
	c.Mine = on
	c.Operators = slices.Clone(c.Operators)
	if on {
		c.Count++
		if self != "" {
			c.Operators = append(c.Operators, self)
		}
		return c
	}
	c.Count--
	c.Operators = slices.DeleteFunc(c.Operators, func(id string) bool { return id == self })
	return c
}

// settleReacts drops the presses a reload has drawn past, and the ones old
// enough that no reload ever will. It runs where both lists are assembled,
// which is the moment a reload has brought Feishu's own answer in.
//
// An answered press goes whatever the summary now says, rather than being held
// until the two agree. Feishu can take a press and still report the strip
// unchanged — taking back a reaction it no longer holds is the ordinary case —
// and a press held against that disagreement would keep drawing a reaction
// that is not there until the window ran out.
func settleReacts(ps []reactPending, now time.Time) []reactPending {
	if len(ps) == 0 {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(ps), func(p reactPending) bool {
		return p.answered || now.Sub(p.at) > reactSettle
	})
}
