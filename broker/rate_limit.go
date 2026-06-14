package broker

import (
	"sync"
	"time"
)

// connRateLimiter enforces a maximum connection rate using a sliding window
// of timestamps. It is safe for concurrent use.
type connRateLimiter struct {
	mu         sync.Mutex
	window     time.Duration
	maxRate    float64 // connections per window (0 = unlimited)
	timestamps []time.Time
}

func newConnRateLimiter(window time.Duration) *connRateLimiter {
	return &connRateLimiter{
		window:     window,
		timestamps: make([]time.Time, 0, 1024),
	}
}

// Allow reports whether a new connection should be accepted under the rate
// limit. It records the attempt if allowed.
func (r *connRateLimiter) Allow() bool {
	if r.maxRate <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Prune timestamps outside the window
	firstValid := 0
	for i, ts := range r.timestamps {
		if ts.After(cutoff) {
			firstValid = i
			break
		}
		firstValid = i + 1
	}
	r.timestamps = r.timestamps[firstValid:]

	// Check rate limit
	maxInWindow := int(r.maxRate * r.window.Seconds())
	if len(r.timestamps) >= maxInWindow {
		return false
	}

	r.timestamps = append(r.timestamps, now)
	return true
}

// SetRate updates the maximum rate. 0 means unlimited.
func (r *connRateLimiter) SetRate(rate float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxRate = rate
}

// publishRateTracker tracks the publish rate for a single client using a
// simple sliding-window counter. It is NOT safe for concurrent use — callers
// must synchronize access (typically under session.mu or via atomic ops).
type publishRateTracker struct {
	window      time.Duration
	maxRate     int // messages per window (0 = unlimited)
	count       int
	windowStart time.Time
}

func newPublishRateTracker(window time.Duration) *publishRateTracker {
	return &publishRateTracker{
		window:      window,
		windowStart: time.Now(),
	}
}

// Allow reports whether a publish should be accepted. It records the attempt
// if allowed.
func (t *publishRateTracker) Allow() bool {
	if t.maxRate <= 0 {
		return true
	}
	now := time.Now()
	if now.Sub(t.windowStart) >= t.window {
		t.count = 0
		t.windowStart = now
	}
	if t.count >= t.maxRate {
		return false
	}
	t.count++
	return true
}

// SetMaxRate updates the maximum allowed publishes per window. 0 means unlimited.
func (t *publishRateTracker) SetMaxRate(rate int) {
	t.maxRate = rate
}
