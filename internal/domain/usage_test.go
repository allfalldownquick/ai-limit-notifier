package domain

import (
	"math"
	"testing"
	"time"
)

func TestNormalizeProviderUsage(t *testing.T) {
	if got := NormalizeCodexLeft(20); got != 80 {
		t.Fatalf("NormalizeCodexLeft(20) = %v, want 80", got)
	}
	if got := NormalizeClaudeUsed(80); got != 80 {
		t.Fatalf("NormalizeClaudeUsed(80) = %v, want 80", got)
	}
}

func TestShouldScheduleReset(t *testing.T) {
	if ShouldScheduleReset(79.9, 80) {
		t.Fatal("79.9% should not schedule at an 80% threshold")
	}
	if !ShouldScheduleReset(80, 80) {
		t.Fatal("80% should schedule at an 80% threshold")
	}
	if !ShouldScheduleReset(100, 80) {
		t.Fatal("100% should schedule at an 80% threshold")
	}
}

func TestWeeklyPace(t *testing.T) {
	resetAt := time.Date(2026, 9, 8, 11, 12, 0, 0, time.UTC)
	now := resetAt.Add(-4 * 24 * time.Hour) // 3 days elapsed in a 7-day window.
	pace := CalculateWeeklyPace(25, resetAt, now)

	wantExpected := 3.0 / 7.0 * 100
	if math.Abs(pace.ExpectedPercent-wantExpected) > 0.001 {
		t.Fatalf("ExpectedPercent = %v, want %v", pace.ExpectedPercent, wantExpected)
	}
	if pace.DeltaPercent >= 0 {
		t.Fatalf("DeltaPercent = %v, expected reserve/under-pace value", pace.DeltaPercent)
	}
	if pace.Remaining != 75 {
		t.Fatalf("Remaining = %v, want 75", pace.Remaining)
	}
}

func TestUsageWindowRejectsStaleReset(t *testing.T) {
	now := time.Now().UTC()
	window := UsageWindow{
		Kind:        WindowFiveHour,
		UsedPercent: 90,
		ResetAt:     now.Add(-10 * time.Minute),
	}
	if err := window.Validate(now); err == nil {
		t.Fatal("expected stale reset_at to be rejected")
	}
}
