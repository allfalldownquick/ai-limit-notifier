// Package claudesock defines the local IPC used to pass a Claude usage
// snapshot from the statusLine wrapper (client) to the RAM-only monitoring
// agent (server). It is a plain Unix domain socket under a RAM-backed
// directory — never a network socket, never a file the payload is written
// to.
package claudesock

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

// Path resolves the local Unix socket path. It always resolves under a
// RAM-backed directory: XDG_RUNTIME_DIR (tmpfs on a normal Linux/WSL2
// session) when set, otherwise /dev/shm. Never a persistent disk path.
func Path() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/dev/shm"
	}
	return filepath.Join(dir, fmt.Sprintf("ai-limit-notifier-%d.sock", os.Getuid()))
}

// Send delivers one Claude usage snapshot to the agent, best-effort, within
// timeout. The snapshot already carries only the four normalized
// used_percent/reset_at fields (domain.UsageSnapshot has no room for
// anything else), so this is also the full data-minimization boundary for
// the passive Claude capture path.
func Send(ctx context.Context, snap domain.UsageSnapshot, timeout time.Duration) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", Path())
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	return json.NewEncoder(conn).Encode(snap)
}
