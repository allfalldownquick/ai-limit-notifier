// Package scheduler turns due, durably-persisted notification events into
// delivery attempts. SQLite (via internal/server/store) is the only source
// of truth for what's pending; this package holds no state of its own that
// would be lost on restart — a fresh process just queries the same table
// and picks up exactly where the last one left off, including anything
// that became overdue while it was down.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultBatchSize    = 50
	maxBackoff          = 15 * time.Minute
	noDestinationRetry  = time.Minute
)

// Scheduler polls store for due events and hands each to Delivery, exactly
// once per attempt — any retry/backoff crossing ticks or restarts is
// durable, driven by store's attempts/next_attempt_at, not by anything held
// in this struct.
type Scheduler struct {
	Store        *store.Store
	Delivery     delivery.Delivery
	PollInterval time.Duration
	BatchSize    int
	Now          func() time.Time // overridable for deterministic tests
}

func New(s *store.Store, d delivery.Delivery) *Scheduler {
	return &Scheduler{
		Store:        s,
		Delivery:     d,
		PollInterval: defaultPollInterval,
		BatchSize:    defaultBatchSize,
		Now:          func() time.Time { return time.Now().UTC() },
	}
}

// Run blocks, processing due events until ctx is cancelled. The first pass
// runs immediately (so restart recovery of overdue events doesn't wait a
// full poll interval) before falling back to the regular tick.
func (sch *Scheduler) Run(ctx context.Context) {
	sch.Tick(ctx)
	ticker := time.NewTicker(sch.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sch.Tick(ctx)
		}
	}
}

func (sch *Scheduler) pollInterval() time.Duration {
	if sch.PollInterval <= 0 {
		return defaultPollInterval
	}
	return sch.PollInterval
}

func (sch *Scheduler) batchSize() int {
	if sch.BatchSize <= 0 {
		return defaultBatchSize
	}
	return sch.BatchSize
}

// Tick processes one batch of currently-due events. Exported so tests (and
// a deterministic local-integration run) can drive delivery without waiting
// on a wall-clock ticker.
func (sch *Scheduler) Tick(ctx context.Context) {
	events, err := sch.Store.DueEvents(ctx, sch.Now(), sch.batchSize())
	if err != nil {
		return // transient store error; the next tick tries again
	}
	for _, ev := range events {
		sch.deliverOne(ctx, ev)
	}
}

func (sch *Scheduler) deliverOne(ctx context.Context, ev store.NotificationEvent) {
	// Atomically claim the event first: DueEvents is a plain read, so
	// without this, two concurrent Tick calls (two goroutines, or two
	// server processes sharing this database file) could both reach here
	// for the same event and both call Delivery.Send.
	claimed, err := sch.Store.ClaimEvent(ctx, ev.ID, sch.Now())
	if err != nil || !claimed {
		return
	}

	destination, err := sch.Store.TelegramChatID(ctx, ev.UserID)
	if err != nil {
		return
	}
	if destination == "" {
		// No linked delivery destination yet (P4 pairing isn't built).
		// Defer rather than hot-looping on this event every tick.
		_ = sch.Store.RecordAttemptFailure(ctx, ev.ID, sch.Now().Add(noDestinationRetry), sch.Now())
		return
	}

	coveredProviders, err := sch.Store.CoveredProviders(ctx, ev.ID)
	if err != nil {
		return
	}
	message := buildMessage(ev, coveredProviders)

	sendErr := sch.Delivery.Send(ctx, destination, message)
	if sendErr == nil {
		_ = sch.Store.MarkSent(ctx, ev.ID, sch.Now())
		return
	}

	next := sch.Now().Add(backoff(ev.Attempts))
	var retryable *delivery.RetryableError
	if errors.As(sendErr, &retryable) && retryable.After > 0 {
		next = sch.Now().Add(retryable.After)
	}
	_ = sch.Store.RecordAttemptFailure(ctx, ev.ID, next, sch.Now())
}

func backoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 10 { // avoid an oversized shift; caps well before maxBackoff anyway
		attempts = 10
	}
	d := (10 * time.Second) << attempts
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// buildMessage is the only place notification text is constructed. It uses
// nothing but persisted, server-validated fields — never anything supplied
// directly by a device payload.
// buildMessage is deliberately cautious: the server knows the provider's
// reported reset_at and that send_at (reset_at + 1 minute) has arrived, but
// it never re-queries the provider afterward to confirm the window actually
// rolled over. "should be available again now" reflects what was actually
// verified; "has reset" would overclaim a fact the server never checked.
func buildMessage(ev store.NotificationEvent, coveredProviders []string) string {
	windowLabel := windowLabels[ev.WindowKind]
	if windowLabel == "" {
		windowLabel = ev.WindowKind
	}

	names := make([]string, 0, 1+len(coveredProviders))
	names = append(names, providerLabel(ev.Provider))
	for _, p := range coveredProviders {
		names = append(names, providerLabel(p))
	}

	verb := "limit should be available again now"
	if len(names) > 1 {
		verb = "limits should be available again now"
	}
	return fmt.Sprintf("Your %s %s usage %s.", strings.Join(names, " and "), windowLabel, verb)
}

var windowLabels = map[string]string{
	"five_hour": "5-hour",
	"weekly":    "weekly",
}

func providerLabel(p string) string {
	switch p {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	default:
		return p
	}
}
