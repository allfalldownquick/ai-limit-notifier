package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func mustPendingEvent(t *testing.T, s *Store) (eventID string, resetAt time.Time) {
	t.Helper()
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	resetAt = time.Now().Add(2 * time.Hour).UTC()
	id, _, err := s.UpsertPendingEvent(ctx, EventInput{
		UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour",
		ResetAt: resetAt, UsedPercent: 90,
	}, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	return id, resetAt
}

func TestClaimEventSucceedsOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	eventID, resetAt := mustPendingEvent(t, s)
	now := resetAt.Add(2 * time.Minute)

	claimed, err := s.ClaimEvent(ctx, eventID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("first claim of a pending event should succeed")
	}

	claimed, err = s.ClaimEvent(ctx, eventID, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("a second claim of an already-claimed (sending, not stale) event must fail")
	}
}

func TestClaimEventRecoversStaleSending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	eventID, resetAt := mustPendingEvent(t, s)
	now := resetAt.Add(2 * time.Minute)

	if claimed, err := s.ClaimEvent(ctx, eventID, now); err != nil || !claimed {
		t.Fatalf("initial claim: claimed=%v err=%v", claimed, err)
	}

	// Simulate the claimer crashing: still "sending", never marked
	// sent/pending again. Advance well past staleSendingTimeout.
	stale := now.Add(staleSendingTimeout + time.Second)
	claimed, err := s.ClaimEvent(ctx, eventID, stale)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("a claim stuck in sending past staleSendingTimeout must become claimable again")
	}
}

func TestClaimEventDoesNotRecoverFreshSending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	eventID, resetAt := mustPendingEvent(t, s)
	now := resetAt.Add(2 * time.Minute)

	if claimed, err := s.ClaimEvent(ctx, eventID, now); err != nil || !claimed {
		t.Fatalf("initial claim: claimed=%v err=%v", claimed, err)
	}

	stillInFlight := now.Add(5 * time.Second) // well under staleSendingTimeout
	claimed, err := s.ClaimEvent(ctx, eventID, stillInFlight)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("a claim that isn't stale yet must not be reclaimed — the original claimer may still be mid-attempt")
	}
}

func TestDueEventsSurfacesStaleSendingNotFreshSending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	eventID, resetAt := mustPendingEvent(t, s)
	claimTime := resetAt.Add(2 * time.Minute)
	if claimed, err := s.ClaimEvent(ctx, eventID, claimTime); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}

	dueSoon, err := s.DueEvents(ctx, claimTime.Add(5*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dueSoon) != 0 {
		t.Fatalf("a fresh in-flight 'sending' event must not appear as due, got %d", len(dueSoon))
	}

	dueAfterStale, err := s.DueEvents(ctx, claimTime.Add(staleSendingTimeout+time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dueAfterStale) != 1 {
		t.Fatalf("a stale 'sending' event must reappear as due for recovery, got %d", len(dueAfterStale))
	}
}

func TestClaimEventOnlyOneWinnerUnderRealConcurrency(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "claim-race.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	eventID, resetAt := mustPendingEvent(t, s)
	now := resetAt.Add(2 * time.Minute)

	const attempts = 20
	results := make([]bool, attempts)
	errs := make([]error, attempts)

	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			claimed, err := s.ClaimEvent(context.Background(), eventID, now)
			results[i] = claimed
			errs[i] = err
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, claimed := range results {
		if errs[i] != nil {
			t.Fatalf("claim attempt %d errored: %v", i, errs[i])
		}
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one winner among %d concurrent real claim attempts (genuinely concurrent DB connections against a shared file), got %d", attempts, winners)
	}
}
