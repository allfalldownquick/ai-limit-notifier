package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

type recordingSink struct {
	mu        sync.Mutex
	events    []Event
	failNext  int // number of upcoming Send calls to fail before succeeding
	alwaysErr bool
}

func (s *recordingSink) Send(ctx context.Context, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alwaysErr {
		return errors.New("sink permanently unavailable")
	}
	if s.failNext > 0 {
		s.failNext--
		return errors.New("sink transiently unavailable")
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func snapshot(provider domain.Provider, fiveHour, weekly *domain.UsageWindow) domain.UsageSnapshot {
	return domain.UsageSnapshot{Provider: provider, FiveHour: fiveHour, Weekly: weekly}
}

func window(kind domain.WindowKind, used float64, resetAt time.Time) *domain.UsageWindow {
	return &domain.UsageWindow{Kind: kind, UsedPercent: used, ResetAt: resetAt}
}

func TestObserveBelowThresholdCreatesNoEvent(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	resetAt := time.Now().Add(2 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 79.9, resetAt), nil))

	if got := sink.count(); got != 0 {
		t.Fatalf("79.9%% must not schedule a reset, got %d event(s)", got)
	}
}

func TestObserveAtThresholdCreatesOneEvent(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	resetAt := time.Now().Add(2 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 80, resetAt), nil))

	if got := sink.count(); got != 1 {
		t.Fatalf("80%% must schedule exactly one event, got %d", got)
	}
}

func TestObserveSameResetWindowNeverDuplicates(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	resetAt := time.Now().Add(2 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 80, resetAt), nil))
	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 90, resetAt), nil))
	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 100, resetAt), nil))

	if got := sink.count(); got != 1 {
		t.Fatalf("80->90->100 against the same reset_at must produce exactly one event, got %d", got)
	}
}

func TestObserveNewResetAtCreatesNewEvent(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	firstReset := time.Now().Add(2 * time.Hour)
	secondReset := firstReset.Add(5 * time.Hour) // a genuinely new window after the first one rolled over

	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 85, firstReset), nil))
	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 85, secondReset), nil))

	if got := sink.count(); got != 2 {
		t.Fatalf("a new reset_at must create a new event, got %d total", got)
	}
}

func TestObserveMissingWindowNeverTriggersAsZero(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)

	// Both windows nil ("unknown"): must never be treated as 0% crossing an
	// inverted/negative threshold or otherwise firing.
	core.Observe(context.Background(), snapshot(domain.ProviderClaude, nil, nil))

	if got := sink.count(); got != 0 {
		t.Fatalf("missing windows must never trigger an event, got %d", got)
	}
}

func TestObserveMissingFiveHourDoesNotBlockWeekly(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	resetAt := time.Now().Add(48 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderClaude, nil, window(domain.WindowWeekly, 90, resetAt)))

	if got := sink.count(); got != 1 {
		t.Fatalf("weekly crossing threshold with five_hour unknown must still fire, got %d", got)
	}
	if sink.events[0].Window != domain.WindowWeekly {
		t.Fatalf("unexpected window kind: %v", sink.events[0].Window)
	}
}

func TestObserveMissingWeeklyDoesNotBlockFiveHour(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	resetAt := time.Now().Add(2 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderClaude, window(domain.WindowFiveHour, 90, resetAt), nil))

	if got := sink.count(); got != 1 {
		t.Fatalf("five_hour crossing threshold with weekly unknown must still fire, got %d", got)
	}
}

func TestDeliverRetriesThenSucceeds(t *testing.T) {
	sink := &recordingSink{failNext: 2}
	core := NewCore(sink, 80)
	core.Retry = RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	resetAt := time.Now().Add(2 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 80, resetAt), nil))

	if got := sink.count(); got != 1 {
		t.Fatalf("expected delivery to succeed after retries, got %d events", got)
	}
}

func TestDeliverGivesUpAndAllowsRetryOnNextObserve(t *testing.T) {
	sink := &recordingSink{alwaysErr: true}
	core := NewCore(sink, 80)
	core.Retry = RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
	resetAt := time.Now().Add(2 * time.Hour)

	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 80, resetAt), nil))
	if got := sink.count(); got != 0 {
		t.Fatalf("permanently failing sink must not record a delivered event, got %d", got)
	}

	// The window must not be marked "sent" purely because retries were
	// exhausted, so a later observation (e.g. the next poll) tries again.
	sink.mu.Lock()
	sink.alwaysErr = false
	sink.mu.Unlock()
	core.Observe(context.Background(), snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 85, resetAt), nil))

	if got := sink.count(); got != 1 {
		t.Fatalf("expected the retried observation to eventually deliver, got %d", got)
	}
}

func TestDeliverRespectsGracefulCancellation(t *testing.T) {
	sink := &recordingSink{alwaysErr: true}
	core := NewCore(sink, 80)
	core.Retry = RetryPolicy{MaxAttempts: 100, BaseDelay: 50 * time.Millisecond, MaxDelay: time.Second}
	resetAt := time.Now().Add(2 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	core.Observe(ctx, snapshot(domain.ProviderCodex, window(domain.WindowFiveHour, 80, resetAt), nil))
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("Observe did not stop promptly after context cancellation: %v", elapsed)
	}
}
