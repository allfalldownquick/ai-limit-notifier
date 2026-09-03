package agent

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

func TestServeClaudeSocketFeedsCore(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	sink := &recordingSink{}
	core := NewCore(sink, 80)

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- ServeClaudeSocket(ctx, core) }()

	waitForSocket(t, claudesock.Path())

	snap := domain.UsageSnapshot{
		Provider: domain.ProviderClaude,
		FiveHour: &domain.UsageWindow{Kind: domain.WindowFiveHour, UsedPercent: 90, ResetAt: time.Now().Add(2 * time.Hour)},
	}
	if err := claudesock.Send(context.Background(), snap, time.Second); err != nil {
		t.Fatalf("failed to send to agent socket: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for sink.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := sink.count(); got != 1 {
		t.Fatalf("expected the agent to observe and fire one event, got %d", got)
	}

	cancel()
	if err := <-serveErr; err != nil {
		t.Fatalf("ServeClaudeSocket returned an error on graceful shutdown: %v", err)
	}
	if _, err := os.Stat(claudesock.Path()); !os.IsNotExist(err) {
		t.Fatalf("expected the socket file to be removed after shutdown, stat err = %v", err)
	}
}

func TestServeClaudeSocketMalformedInputNeverCrashes(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	sink := &recordingSink{}
	core := NewCore(sink, 80)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeClaudeSocket(ctx, core) }()
	waitForSocket(t, claudesock.Path())

	conn, err := net.Dial("unix", claudesock.Path())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	_, _ = conn.Write([]byte("not json at all"))
	_ = conn.Close()

	// Give the (now-crashed-if-buggy) handler goroutine a moment, then prove
	// the listener is still alive by sending one valid message afterwards.
	time.Sleep(100 * time.Millisecond)

	snap := domain.UsageSnapshot{
		Provider: domain.ProviderClaude,
		Weekly:   &domain.UsageWindow{Kind: domain.WindowWeekly, UsedPercent: 95, ResetAt: time.Now().Add(48 * time.Hour)},
	}
	if err := claudesock.Send(context.Background(), snap, time.Second); err != nil {
		t.Fatalf("agent socket did not survive a malformed message: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for sink.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := sink.count(); got != 1 {
		t.Fatalf("expected the valid follow-up message to still be observed, got %d events", got)
	}
}

// TestServeClaudeSocketReplacesStaleSocketFile proves the "unexpected
// stale socket at next startup" recovery path still works: a socket file
// left behind by a killed/crashed process (no live listener behind it) must
// not block a fresh ServeClaudeSocket from starting, unlike a genuinely
// live conflicting listener (see TestServeClaudeSocketBindConflictReturnsError).
func TestServeClaudeSocketReplacesStaleSocketFile(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path := claudesock.Path()

	// Simulate a crash: a socket file left behind with nothing listening
	// on it. (A clean net.Listen+Close won't do — Go's UnixListener
	// unlinks its own socket file on Close by default, which would just
	// clean up after itself rather than reproduce a stale leftover.)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	sink := &recordingSink{}
	core := NewCore(sink, 80)
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- ServeClaudeSocket(ctx, core) }()

	snap := domain.UsageSnapshot{
		Provider: domain.ProviderClaude,
		FiveHour: &domain.UsageWindow{Kind: domain.WindowFiveHour, UsedPercent: 90, ResetAt: time.Now().Add(2 * time.Hour)},
	}
	deadline := time.Now().Add(2 * time.Second)
	var sendErr error
	for time.Now().Before(deadline) {
		if sendErr = claudesock.Send(context.Background(), snap, 200*time.Millisecond); sendErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sendErr != nil {
		cancel()
		<-serveErr
		t.Fatalf("a fresh agent never came up behind the stale socket file: %v", sendErr)
	}

	cancel()
	if err := <-serveErr; err != nil {
		t.Fatalf("ServeClaudeSocket returned an error: %v", err)
	}
}

func TestServeClaudeSocketBindConflictReturnsError(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	sink := &recordingSink{}
	core := NewCore(sink, 80)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	go func() { _ = ServeClaudeSocket(ctx1, core) }()
	waitForSocket(t, claudesock.Path())

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	err := ServeClaudeSocket(ctx2, core)
	if err == nil {
		t.Fatal("expected a bind error when the socket path is already in use")
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %s was never created", path)
}
