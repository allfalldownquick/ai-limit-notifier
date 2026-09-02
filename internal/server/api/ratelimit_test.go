package api

import (
	"fmt"
	"testing"
	"time"
)

func TestIPLimiterEvictsStaleEntries(t *testing.T) {
	l := newIPLimiter(10, 10)
	l.allow("1.2.3.4:1111")
	l.allow("5.6.7.8:2222")
	if got := l.size(); got != 2 {
		t.Fatalf("size = %d, want 2", got)
	}

	// Force both entries to look idle past the TTL, and force the sweep to
	// be due, without waiting real wall-clock time.
	l.mu.Lock()
	for _, e := range l.limiters {
		e.lastSeen = time.Now().Add(-2 * ipLimiterTTL)
	}
	l.lastSweep = time.Now().Add(-2 * ipLimiterSweepEvery)
	l.mu.Unlock()

	l.allow("9.9.9.9:3333") // triggers a sweep as a side effect, then adds itself
	if got := l.size(); got != 1 {
		t.Fatalf("size after sweep = %d, want 1 (only the fresh entry should remain)", got)
	}
}

func TestIPLimiterFreshEntriesSurviveSweep(t *testing.T) {
	l := newIPLimiter(10, 10)
	l.allow("1.2.3.4:1111")

	l.mu.Lock()
	l.lastSweep = time.Now().Add(-2 * ipLimiterSweepEvery) // force a sweep, but entry is still fresh
	l.mu.Unlock()

	l.allow("1.2.3.4:1111")
	if got := l.size(); got != 1 {
		t.Fatalf("a recently-seen entry must survive a sweep, size = %d", got)
	}
}

func TestIPLimiterHardCapFailsClosedForNewAddresses(t *testing.T) {
	l := newIPLimiter(10, 10)
	l.max = 3

	for i := 0; i < 3; i++ {
		addr := fmt.Sprintf("10.0.0.%d:1234", i)
		if !l.allow(addr) {
			t.Fatalf("address %d should have been allowed under the cap", i)
		}
	}
	if got := l.size(); got != 3 {
		t.Fatalf("size = %d, want 3", got)
	}

	if l.allow("10.0.0.99:1234") {
		t.Fatal("a brand-new address beyond the hard cap must be rejected, not silently grow the map")
	}
	if got := l.size(); got != 3 {
		t.Fatalf("size after a rejected new address = %d, want unchanged 3", got)
	}

	// An address already tracked must still be rate-limited normally even
	// while at the cap (the cap only blocks *new* entries).
	if !l.allow("10.0.0.0:1234") {
		t.Fatal("an existing tracked address should still be allowed under its own limiter while at the cap")
	}
}
