package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

func TestWindowFromFiveHourMapping(t *testing.T) {
	used, resets, mins := 46.0, int64(1788387939), 300
	w := &rateWindow{UsedPercent: &used, ResetsAt: &resets, WindowDurationMin: &mins}

	got := windowFrom(w, domain.WindowFiveHour, fiveHourMins)
	if got == nil {
		t.Fatal("expected a five_hour window, got nil")
	}
	if got.Kind != domain.WindowFiveHour || got.UsedPercent != 46 {
		t.Fatalf("unexpected window: %+v", got)
	}
	if got.ResetAt.Unix() != resets {
		t.Fatalf("ResetAt = %v, want unix %d", got.ResetAt, resets)
	}
}

func TestWindowFromWeeklyMapping(t *testing.T) {
	used, resets, mins := 60.0, int64(1788855178), 10080
	w := &rateWindow{UsedPercent: &used, ResetsAt: &resets, WindowDurationMin: &mins}

	got := windowFrom(w, domain.WindowWeekly, weeklyMins)
	if got == nil || got.Kind != domain.WindowWeekly || got.UsedPercent != 60 {
		t.Fatalf("unexpected weekly window: %+v", got)
	}
}

func TestWindowFromUnknownDuration(t *testing.T) {
	used, resets, mins := 50.0, int64(1788855178), 1440 // neither 300 nor 10080
	w := &rateWindow{UsedPercent: &used, ResetsAt: &resets, WindowDurationMin: &mins}

	if got := windowFrom(w, domain.WindowFiveHour, fiveHourMins); got != nil {
		t.Fatalf("expected nil for mismatched duration, got %+v", got)
	}
}

func TestWindowFromMissingFieldsIsUnknown(t *testing.T) {
	if got := windowFrom(nil, domain.WindowFiveHour, fiveHourMins); got != nil {
		t.Fatalf("nil window should stay unknown, got %+v", got)
	}
	mins := fiveHourMins
	if got := windowFrom(&rateWindow{WindowDurationMin: &mins}, domain.WindowFiveHour, fiveHourMins); got != nil {
		t.Fatalf("missing usedPercent/resetsAt should stay unknown, got %+v", got)
	}
}

func TestWindowFromInvalidUsedPercent(t *testing.T) {
	used, resets, mins := 150.0, int64(1788855178), fiveHourMins
	w := &rateWindow{UsedPercent: &used, ResetsAt: &resets, WindowDurationMin: &mins}
	if got := windowFrom(w, domain.WindowFiveHour, fiveHourMins); got != nil {
		t.Fatalf("usedPercent > 100 must stay unknown, not clamped/fabricated, got %+v", got)
	}

	used = -5
	if got := windowFrom(w, domain.WindowFiveHour, fiveHourMins); got != nil {
		t.Fatalf("negative usedPercent must stay unknown, got %+v", got)
	}
}

func TestWindowFromInvalidResetTimestamp(t *testing.T) {
	used, resets, mins := 50.0, int64(0), fiveHourMins
	w := &rateWindow{UsedPercent: &used, ResetsAt: &resets, WindowDurationMin: &mins}
	if got := windowFrom(w, domain.WindowFiveHour, fiveHourMins); got != nil {
		t.Fatalf("resetsAt <= 0 must stay unknown, got %+v", got)
	}
}

// writeFixture creates an executable test script and returns its path.
func writeFixture(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadProviderUnavailable(t *testing.T) {
	r := &Reader{Binary: filepath.Join(t.TempDir(), "does-not-exist-codex")}
	_, err := r.Read(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestReadProcessTimeout(t *testing.T) {
	// Ignores stdin and never responds, forcing the caller's context deadline
	// to be the only thing that ends the read.
	script := "#!/bin/sh\ncat >/dev/null &\nsleep 30\n"
	bin := writeFixture(t, script)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	r := &Reader{Binary: bin}
	start := time.Now()
	_, err := r.Read(ctx)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Read did not return promptly after context deadline: %v", elapsed)
	}
}

func TestReadMalformedJSONRPC(t *testing.T) {
	script := "#!/bin/sh\ncat >/dev/null &\necho 'not json at all'\nexit 0\n"
	bin := writeFixture(t, script)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := &Reader{Binary: bin}
	_, err := r.Read(ctx)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected ErrProtocol for malformed output, got %v", err)
	}
}

func TestReadHappyPathNoRawMetadataLeak(t *testing.T) {
	// Mirrors the real observed shape (docs/REAL_MACHINE_VALIDATION.md) plus
	// extra account/credits/plan/upsell metadata that must never surface.
	script := `#!/bin/sh
read -r line1
printf '{"id":1,"result":{"userAgent":"fake"}}\n'
read -r line2
printf '{"id":2,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":46,"windowDurationMins":300,"resetsAt":1788387939},"secondary":{"usedPercent":60,"windowDurationMins":10080,"resetsAt":1788855178},"credits":{"balance":"123.45"},"accountId":"secret-account-id","planType":"plus"}}}\n'
`
	bin := writeFixture(t, script)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := &Reader{Binary: bin}
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FiveHour == nil || snap.FiveHour.UsedPercent != 46 {
		t.Fatalf("unexpected five_hour: %+v", snap.FiveHour)
	}
	if snap.Weekly == nil || snap.Weekly.UsedPercent != 60 {
		t.Fatalf("unexpected weekly: %+v", snap.Weekly)
	}

	// The domain.UsageSnapshot type structurally cannot carry credits/
	// accountId/planType, but assert it here so a future field addition to
	// UsageSnapshot is forced to justify itself against this test.
	if snap.Provider != domain.ProviderCodex {
		t.Fatalf("unexpected provider: %v", snap.Provider)
	}
}
