package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
)

func writeCodexFixture(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const codexHappyPathScript = `#!/bin/sh
read -r line1
printf '{"id":1,"result":{"userAgent":"fake"}}\n'
read -r line2
printf '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":85,"windowDurationMins":300,"resetsAt":9999999999},"secondary":{"usedPercent":90,"windowDurationMins":10080,"resetsAt":9999999999}}}}\n'
`

func TestPollCodexProviderUnavailableNeverCallsSink(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	reader := &codex.Reader{Binary: filepath.Join(t.TempDir(), "does-not-exist-codex")}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	PollCodex(ctx, core, reader, time.Hour)

	if got := sink.count(); got != 0 {
		t.Fatalf("an unavailable Codex reader must never produce an event, got %d", got)
	}
}

func TestPollCodexTimeoutNeverCallsSink(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	// Never responds; PollCodex's own 10s per-read timeout should still let
	// the surrounding test context end this promptly.
	bin := writeCodexFixture(t, "#!/bin/sh\ncat >/dev/null &\nsleep 30\n")
	reader := &codex.Reader{Binary: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	PollCodex(ctx, core, reader, time.Hour)
	elapsed := time.Since(start)

	if got := sink.count(); got != 0 {
		t.Fatalf("a timed-out Codex read must never produce an event, got %d", got)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("PollCodex did not return promptly after context cancellation: %v", elapsed)
	}
}

func TestPollCodexHappyPathFeedsCore(t *testing.T) {
	sink := &recordingSink{}
	core := NewCore(sink, 80)
	bin := writeCodexFixture(t, codexHappyPathScript)
	reader := &codex.Reader{Binary: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// interval is intentionally below MinCodexPollInterval to prove it gets
	// clamped: within this short-lived context only the immediate first
	// poll should ever run, not a tight loop.
	PollCodex(ctx, core, reader, time.Millisecond)

	if got := sink.count(); got != 2 { // both windows are above threshold
		t.Fatalf("expected exactly 2 events from the single clamped poll, got %d", got)
	}
}
