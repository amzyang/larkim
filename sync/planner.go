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

// Chunk splits xs into slices of at most n elements.
func Chunk[T any](xs []T, n int) [][]T {
	if n <= 0 || len(xs) == 0 {
		return nil
	}
	out := make([][]T, 0, (len(xs)+n-1)/n)
	for i := 0; i < len(xs); i += n {
		out = append(out, xs[i:min(i+n, len(xs))])
	}
	return out
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
