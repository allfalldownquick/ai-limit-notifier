package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

// ServeClaudeSocket listens for passive Claude snapshots sent by the
// statusLine wrapper and feeds each into core. It blocks until ctx is
// cancelled, then closes the listener and removes the socket file.
//
// This function owns the agent's half of the guarantee "if the agent isn't
// running, the wrapper still works": it simply doesn't run then, and the
// wrapper's own send has its own short timeout so a missing/refused socket
// never affects the wrapper's statusLine passthrough.
func ServeClaudeSocket(ctx context.Context, core *Core) error {
	path := claudesock.Path()

	// A file at path could be a live listener (another agent already
	// running) or a stale leftover from a prior crash. Only the latter is
	// safe to clear before binding.
	if conn, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		return fmt.Errorf("claude socket %s is already in use by another running agent", path)
	}
	_ = os.Remove(path)

	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Owner-only: the socket carries no secrets, but a stray local user
	// connecting could inject bogus usage numbers, especially under the
	// world-writable /dev/shm fallback path.
	_ = os.Chmod(path, 0o600)
	defer func() {
		_ = l.Close()
		_ = os.Remove(path)
	}()

	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go handleClaudeConn(ctx, core, conn)
	}
}

func handleClaudeConn(ctx context.Context, core *Core, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var snap domain.UsageSnapshot
	if err := json.NewDecoder(conn).Decode(&snap); err != nil {
		return // malformed input must never crash the agent
	}
	if snap.Provider != domain.ProviderClaude {
		return
	}
	core.Observe(ctx, snap)
}
