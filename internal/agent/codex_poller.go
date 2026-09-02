package agent

import (
	"context"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
)

// MinCodexPollInterval bounds how often the Codex app-server may be
// polled, regardless of configuration, so a misconfigured interval can't
// hammer it.
const MinCodexPollInterval = 30 * time.Second

// PollCodex polls reader on a bounded interval and feeds every successful
// read into core, until ctx is cancelled. A failed/timed-out read is left
// as-is (no fabricated snapshot) and simply retried on the next tick.
func PollCodex(ctx context.Context, core *Core, reader *codex.Reader, interval time.Duration) {
	if interval < MinCodexPollInterval {
		interval = MinCodexPollInterval
	}

	poll := func() {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		snap, err := reader.Read(readCtx)
		if err != nil {
			return
		}
		core.Observe(ctx, snap)
	}

	poll() // an immediate first read so `monitor` shows state without waiting a full interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
