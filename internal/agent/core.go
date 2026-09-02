// Package agent implements the RAM-only monitoring core: threshold
// detection, per-window dedup, and bounded-retry delivery to a Sink. No
// usage/history/cache/log state is ever written to disk; all dedup state
// lives in process memory and is expected to be re-derivable (with
// server-side idempotency) after a restart.
package agent

import (
	"context"
	"sync"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

// Event is a single reset-threshold crossing for one provider/window.
type Event struct {
	Provider    domain.Provider
	Window      domain.WindowKind
	UsedPercent float64
	ResetAt     time.Time
	ObservedAt  time.Time
}

// Sink delivers an Event. A future HTTPS sink (P3) implements this same
// interface; today only test/print sinks exist.
type Sink interface {
	Send(ctx context.Context, ev Event) error
}

// RetryPolicy bounds how hard Core retries a single Event delivery before
// giving up for this observation. Giving up does not mark the window as
// sent, so a later observation (next poll, or a fresh Claude capture) will
// try again as long as the threshold is still crossed.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 5 * time.Second}
}

type dedupKey struct {
	provider domain.Provider
	window   domain.WindowKind
	resetAt  int64
}

// Core tracks reset-threshold dedup state in RAM and delivers Events
// through Sink with bounded retry/backoff. It is safe for concurrent use by
// multiple provider feeds (Codex polling, Claude socket receiver).
type Core struct {
	Threshold float64
	Sink      Sink
	Retry     RetryPolicy

	sentMu sync.Mutex
	sent   map[dedupKey]bool
}

func NewCore(sink Sink, threshold float64) *Core {
	return &Core{
		Threshold: threshold,
		Sink:      sink,
		Retry:     DefaultRetryPolicy(),
		sent:      make(map[dedupKey]bool),
	}
}

// Observe evaluates one provider's usage snapshot. For each present window
// at or above Threshold, at most one Event is delivered per distinct
// (provider, window, reset_at) — 80% -> 90% -> 100% against the same
// reset_at never re-fires, but a new reset_at (a genuinely new window)
// does. A window that fails delivery after Retry's bounded attempts is left
// un-dedup'd so a later Observe for the same window tries again.
func (c *Core) Observe(ctx context.Context, snap domain.UsageSnapshot) {
	for _, w := range []*domain.UsageWindow{snap.FiveHour, snap.Weekly} {
		if w == nil {
			continue // unknown window: never treated as 0% and never fires.
		}
		if !domain.ShouldScheduleReset(w.UsedPercent, c.Threshold) {
			continue
		}
		key := dedupKey{provider: snap.Provider, window: w.Kind, resetAt: w.ResetAt.Unix()}
		if c.alreadySent(key) {
			continue
		}
		ev := Event{
			Provider:    snap.Provider,
			Window:      w.Kind,
			UsedPercent: w.UsedPercent,
			ResetAt:     w.ResetAt,
			ObservedAt:  time.Now().UTC(),
		}
		if c.deliver(ctx, ev) {
			c.markSent(key)
		}
	}
}

func (c *Core) alreadySent(key dedupKey) bool {
	c.sentMu.Lock()
	defer c.sentMu.Unlock()
	return c.sent[key]
}

func (c *Core) markSent(key dedupKey) {
	c.sentMu.Lock()
	defer c.sentMu.Unlock()
	c.sent[key] = true
}

// deliver returns true only on a confirmed successful Send.
func (c *Core) deliver(ctx context.Context, ev Event) bool {
	policy := c.Retry
	if policy.MaxAttempts <= 0 {
		policy = DefaultRetryPolicy()
	}
	delay := policy.BaseDelay
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := c.Sink.Send(ctx, ev); err == nil {
			return true
		}
		if attempt == policy.MaxAttempts || ctx.Err() != nil {
			return false
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return false
		}
		delay *= 2
		if delay > policy.MaxDelay {
			delay = policy.MaxDelay
		}
	}
	return false
}
