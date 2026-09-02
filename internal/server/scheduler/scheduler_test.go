package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

const combineWindow = 10 * time.Minute

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func setup(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	userID, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTelegramChatID(ctx, userID, "chat-123"); err != nil {
		t.Fatal(err)
	}
	deviceID, _, err := s.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	return s, userID, deviceID
}

func TestTickDeliversDueEventAndMarksSent(t *testing.T) {
	s, userID, deviceID := setup(t)
	ctx := context.Background()
	resetAt := time.Now().Add(time.Hour).UTC()

	_, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 85,
	}, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	fake := delivery.NewFakeDelivery()
	sch := New(s, fake)
	sch.Now = func() time.Time { return resetAt.Add(2 * time.Minute) }

	sch.Tick(ctx)

	sent := fake.All()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one delivery, got %d", len(sent))
	}
	if sent[0].Destination != "chat-123" {
		t.Fatalf("unexpected destination: %q", sent[0].Destination)
	}

	due, err := s.DueEvents(ctx, sch.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("a sent event must not still be due, got %d", len(due))
	}

	// A second tick (simulating a restart or the next poll) must not resend.
	sch.Tick(ctx)
	if len(fake.All()) != 1 {
		t.Fatalf("expected no duplicate delivery on the next tick, got %d total", len(fake.All()))
	}
}

func TestTickCombinesMessageForCoveredEvent(t *testing.T) {
	s, userID, deviceID := setup(t)
	ctx := context.Background()
	base := time.Now().Add(time.Hour).UTC()

	if _, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: base, UsedPercent: 85,
	}, combineWindow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "claude", WindowKind: "five_hour",
		ResetAt: base.Add(3 * time.Minute), UsedPercent: 90,
	}, combineWindow); err != nil {
		t.Fatal(err)
	}

	fake := delivery.NewFakeDelivery()
	sch := New(s, fake)
	sch.Now = func() time.Time { return base.Add(10 * time.Minute) }
	sch.Tick(ctx)

	sent := fake.All()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one combined delivery, got %d: %+v", len(sent), sent)
	}
	if !strings.Contains(sent[0].Message, "Codex") || !strings.Contains(sent[0].Message, "Claude") {
		t.Fatalf("combined message should name both providers, got %q", sent[0].Message)
	}
}

func TestCombinedNotificationNotResentAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "combine-restart.db")
	ctx := context.Background()

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTelegramChatID(ctx, userID, "chat-123"); err != nil {
		t.Fatal(err)
	}
	deviceID, _, err := s.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(time.Hour).UTC()

	if _, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: base, UsedPercent: 85,
	}, combineWindow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "claude", WindowKind: "five_hour",
		ResetAt: base.Add(3 * time.Minute), UsedPercent: 90,
	}, combineWindow); err != nil {
		t.Fatal(err)
	}

	fake := delivery.NewFakeDelivery()
	sch := New(s, fake)
	sch.Now = func() time.Time { return base.Add(10 * time.Minute) }
	sch.Tick(ctx)

	sent := fake.All()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one combined delivery, got %d: %+v", len(sent), sent)
	}
	t.Logf("rendered combined message: %q", sent[0].Message)
	if !strings.Contains(sent[0].Message, "Codex") || !strings.Contains(sent[0].Message, "Claude") {
		t.Fatalf("combined message must name both providers, got %q", sent[0].Message)
	}

	// "restart": close and reopen the store, run a fresh scheduler+delivery
	// against it, and tick again at the same (and later) times.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	fake2 := delivery.NewFakeDelivery()
	sch2 := New(s2, fake2)
	sch2.Now = func() time.Time { return base.Add(time.Hour) }
	sch2.Tick(ctx)
	sch2.Tick(ctx) // a second tick, to also rule out a duplicate within one recovered run

	if got := len(fake2.All()); got != 0 {
		t.Fatalf("the covered event must never be sent separately after restart, got %d extra deliveries: %+v", got, fake2.All())
	}
}

func TestTickRetriesOnTransientFailure(t *testing.T) {
	s, userID, deviceID := setup(t)
	ctx := context.Background()
	resetAt := time.Now().Add(time.Hour).UTC()

	_, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 85,
	}, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	fake := delivery.NewFakeDelivery()
	fake.FailNext(1, errors.New("transient"))
	sch := New(s, fake)
	now := resetAt.Add(2 * time.Minute)
	sch.Now = func() time.Time { return now }

	sch.Tick(ctx) // fails, schedules a backoff retry

	due, err := s.DueEvents(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatal("a backed-off event must not be immediately due again")
	}

	now = now.Add(time.Hour) // comfortably past the bounded backoff
	sch.Tick(ctx)
	if len(fake.All()) != 1 {
		t.Fatalf("expected the retry to eventually succeed, got %d deliveries", len(fake.All()))
	}
}

func TestTickHonorsRetryAfter(t *testing.T) {
	s, userID, deviceID := setup(t)
	ctx := context.Background()
	resetAt := time.Now().Add(time.Hour).UTC()

	_, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 85,
	}, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	rl := &retryAfterOnceDelivery{after: 30 * time.Second}
	sch := New(s, rl)
	now := resetAt.Add(2 * time.Minute)
	sch.Now = func() time.Time { return now }
	sch.Tick(ctx)

	// Just under the retry_after: must not be due yet.
	due, err := s.DueEvents(ctx, now.Add(20*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatal("must not retry before the server-specified retry_after")
	}

	due, err = s.DueEvents(ctx, now.Add(31*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatal("expected the event due again after retry_after elapses")
	}
}

func TestTickDefersWhenNoDestination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID, err := s.CreateUser(ctx) // no SetTelegramChatID: pairing not done
	if err != nil {
		t.Fatal(err)
	}
	deviceID, _, err := s.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	resetAt := time.Now().Add(time.Hour).UTC()
	if _, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 85,
	}, combineWindow); err != nil {
		t.Fatal(err)
	}

	fake := delivery.NewFakeDelivery()
	sch := New(s, fake)
	now := resetAt.Add(2 * time.Minute)
	sch.Now = func() time.Time { return now }
	sch.Tick(ctx)

	if len(fake.All()) != 0 {
		t.Fatal("must never deliver without a linked destination")
	}
	due, err := s.DueEvents(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatal("expected the event deferred rather than immediately due again")
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	s, _, _ := setup(t)
	fake := delivery.NewFakeDelivery()
	sch := New(s, fake)
	sch.PollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sch.Run(ctx)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after context cancellation")
	}
}

type retryAfterOnceDelivery struct {
	after time.Duration
	sent  bool
}

func (r *retryAfterOnceDelivery) Send(_ context.Context, _, _ string) error {
	if r.sent {
		return nil
	}
	r.sent = true
	return &delivery.RetryableError{Err: errors.New("rate limited"), After: r.after}
}
