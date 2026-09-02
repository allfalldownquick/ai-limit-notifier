// Package delivery defines the transport boundary between the scheduler
// and Telegram. The scheduler never talks to Telegram directly, so P4's
// pairing/onboarding work can land without touching scheduling logic, and
// tests never need real network access.
package delivery

import (
	"context"
	"time"
)

// Delivery sends one already-built notification message to one
// already-resolved destination. Implementations must never let the caller
// (scheduler) influence destination or text beyond what's passed in — the
// server builds both from trusted templates and persisted fields; a device
// payload never reaches this far.
type Delivery interface {
	Send(ctx context.Context, destination, message string) error
}

// RetryableError signals a transient failure and, when After is non-zero,
// how long the caller should wait before retrying (e.g. Telegram's
// retry_after on HTTP 429).
type RetryableError struct {
	Err   error
	After time.Duration
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }
