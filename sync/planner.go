// Package sync pulls the user's IM data from Feishu into the local store.
//
// Discovery is polling: a cross-chat message search over a sliding window
// (fast path) plus a periodic reconciliation of the most active chats (slow
// path). The pure functions in this file decide what to fetch; Syncer does the
// IO.
package sync

import (
	"time"
)

// MinWindow is the smallest search window worth bisecting down to.
const MinWindow = time.Minute

// Window is a closed time range.
type Window struct {
	Start, End time.Time
}

// FastWindow computes the search window for a tick. A zero watermark (first
// run) looks back only one overlap; backfill covers older history.
func FastWindow(watermark, now time.Time, overlap time.Duration) Window {
	if watermark.IsZero() || now.Before(watermark) {
		// Never synced, or the clock went backwards: rebuild from now.
		return Window{Start: now.Add(-overlap), End: now}
	}
	return Window{Start: watermark.Add(-overlap), End: now}
}

// Halves splits a window at its midpoint; ok is false when it is already at
// MinWindow, in which case the caller accepts the truncated result.
func Halves(w Window) (first, second Window, ok bool) {
	d := w.End.Sub(w.Start)
	if d <= MinWindow {
		return w, w, false
	}
	mid := w.Start.Add(d / 2)
	return Window{Start: w.Start, End: mid}, Window{Start: mid, End: w.End}, true
}

// Due reports whether a periodic job last run at last should run now.
func Due(last time.Time, every time.Duration, now time.Time) bool {
	return last.IsZero() || !now.Before(last.Add(every))
}

// Backoff returns the delay for the n-th consecutive failure (n >= 1),
// doubling from base up to limit.
func Backoff(n int, base, limit time.Duration) time.Duration {
	d := base
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// UniqueStrings returns xs without duplicates, first occurrence wins.
func UniqueStrings(xs []string) []string {
	seen := make(map[string]struct{}, len(xs))
	out := xs[:0:0]
	for _, x := range xs {
		if x == "" {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

// ActiveDelta names the chats worth listing after an active-time ordering
// moved from prev to now. A chat that moved up has just received a message:
// Feishu puts one that did at position 1. A chat absent from prev counts as
// moved, since it can only have entered the page from below.
//
// The chat at the head is always named, whether or not it moved. It is already
// as high as the ordering goes, so a second message into it changes nothing
// the comparison can see — and the chat somebody just wrote in is the likeliest
// to be written in again, which makes this the blind spot that would show up
// most: every reply after the first in a conversation.
//
// An empty prev names nothing: a first run has no ordering to compare against,
// and backfill is what covers a cold store.
func ActiveDelta(prev, now []string) []string {
	if len(prev) == 0 || len(now) == 0 {
		return nil
	}
	was := make(map[string]int, len(prev))
	for i, id := range prev {
		was[id] = i
	}
	out := []string{now[0]}
	for i, id := range now[1:] {
		if before, seen := was[id]; !seen || i+1 < before {
			out = append(out, id)
		}
	}
	return out
}
