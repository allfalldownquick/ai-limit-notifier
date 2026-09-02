package scheduler

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

// TestTwoConcurrentSchedulersDeliverOnce simulates two independent
// ai-limit-server processes sharing one SQLite file (a real, if
// unintentional, deployment scenario) each running their own Scheduler.
// Without an atomic claim, both could observe the same due event via
// DueEvents and both call Delivery.Send for it.
func TestTwoConcurrentSchedulersDeliverOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "two-schedulers.db")
	ctx := context.Background()

	// One store creates the fixture data...
	seed, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := seed.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.SetTelegramChatID(ctx, userID, "chat-123"); err != nil {
		t.Fatal(err)
	}
	deviceID, _, err := seed.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	resetAt := time.Now().Add(time.Hour).UTC()
	if _, _, err := seed.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 90,
	}, combineWindow); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	// ...then two independent Store handles (standing in for two separate
	// processes) open the same file concurrently.
	s1, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	fake1 := delivery.NewFakeDelivery()
	fake2 := delivery.NewFakeDelivery()
	sch1 := New(s1, fake1)
	sch2 := New(s2, fake2)
	now := resetAt.Add(2 * time.Minute)
	sch1.Now = func() time.Time { return now }
	sch2.Now = func() time.Time { return now }

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sch1.Tick(ctx) }()
	go func() { defer wg.Done(); sch2.Tick(ctx) }()
	wg.Wait()

	total := len(fake1.All()) + len(fake2.All())
	if total != 1 {
		t.Fatalf("expected exactly one delivery across two concurrent schedulers, got %d (fake1=%v fake2=%v)", total, fake1.All(), fake2.All())
	}
}

func TestTickRecoversStaleSendingEvent(t *testing.T) {
	s, userID, deviceID := setup(t)
	ctx := context.Background()
	resetAt := time.Now().Add(time.Hour).UTC()

	eventID, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 90,
	}, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	claimTime := resetAt.Add(2 * time.Minute)
	if claimed, err := s.ClaimEvent(ctx, eventID, claimTime); err != nil || !claimed {
		t.Fatalf("simulated crash-mid-claim setup: claimed=%v err=%v", claimed, err)
	}
	// The event is now stuck in "sending", as if that claimer crashed
	// before ever calling Send.

	fake := delivery.NewFakeDelivery()
	sch := New(s, fake)
	recoveryTime := claimTime.Add(staleSendingRecoveryMargin())
	sch.Now = func() time.Time { return recoveryTime }
	sch.Tick(ctx)

	if got := len(fake.All()); got != 1 {
		t.Fatalf("expected the stale-sending event to be recovered and delivered, got %d deliveries", got)
	}
}

// TestCrashBetweenSendSuccessAndMarkSentCanDuplicate deliberately proves —
// rather than hides — the documented at-least-once tradeoff: if the
// process crashes after a successful Delivery.Send but before MarkSent
// commits, recovery resends. A rare duplicate here is accepted; silently
// losing the notification is not.
func TestCrashBetweenSendSuccessAndMarkSentCanDuplicate(t *testing.T) {
	s, userID, deviceID := setup(t)
	ctx := context.Background()
	resetAt := time.Now().Add(time.Hour).UTC()

	eventID, _, err := s.UpsertPendingEvent(ctx, store.EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 90,
	}, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	firstAttempt := resetAt.Add(2 * time.Minute)
	// Simulate exactly the crash window: claim, then a successful send that
	// never gets to call MarkSent (we call Send directly rather than going
	// through deliverOne, precisely to skip the MarkSent step).
	if claimed, err := s.ClaimEvent(ctx, eventID, firstAttempt); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	fake := delivery.NewFakeDelivery()
	if err := fake.Send(ctx, "chat-123", "first (pre-crash) attempt"); err != nil {
		t.Fatal(err)
	}
	// process "crashes" here — no MarkSent call.

	sch := New(s, fake)
	recoveryTime := firstAttempt.Add(staleSendingRecoveryMargin())
	sch.Now = func() time.Time { return recoveryTime }
	sch.Tick(ctx)

	sent := fake.All()
	if len(sent) != 2 {
		t.Fatalf("expected the pre-crash send plus exactly one recovery resend (documented rare duplicate), got %d: %+v", len(sent), sent)
	}
}

// staleSendingRecoveryMargin is deliberately generous rather than pinned to
// internal/server/store's unexported staleSendingTimeout constant: these
// tests only need "comfortably past whatever that threshold is", not its
// exact value.
func staleSendingRecoveryMargin() time.Duration {
	return 5 * time.Minute
}
