package integration_test

// Regression coverage for a real cleanup bug: signal.NotifyContext in
// runMonitor only caught os.Interrupt (SIGINT) and syscall.SIGTERM, but a
// closed terminal/dropped WSL session delivers SIGHUP to the foreground
// process group instead, and SIGHUP's default disposition is immediate
// termination with no deferred cleanup at all -- ServeClaudeSocket's
// `defer os.Remove(path)` never runs, leaving a stale (harmless, but
// undocumented-as-such) socket file behind. Reproduced directly against the
// real compiled binary with a real SIGHUP before the fix (SIGINT always
// cleaned up correctly; SIGHUP did not), fixed by adding syscall.SIGHUP to
// the notified signal set.
//
// These tests exercise the actual subprocess + real OS signal delivery --
// not just internal/agent.ServeClaudeSocket's own already-covered
// ctx-cancellation unit test -- because the bug was specifically in how
// cmd/ai-limit-notifier's signal wiring did (or didn't) turn an OS signal
// into that same graceful ctx cancellation.

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMonitorSignalShutdownRemovesSocket(t *testing.T) {
	bin := buildAgentBinary(t)

	for _, sig := range []struct {
		name   string
		signal syscall.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
		{"SIGHUP", syscall.SIGHUP}, // the signal that reproduced the bug
	} {
		t.Run(sig.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			configDir := t.TempDir()

			cmd := exec.Command(bin, "monitor", "--dry-run")
			cmd.Env = append(os.Environ(),
				"XDG_RUNTIME_DIR="+runtimeDir,
				"XDG_CONFIG_HOME="+configDir,
			)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Drain output so the subprocess is never blocked on a full pipe.
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := out.Read(buf); err != nil {
						return
					}
				}
			}()

			// The socket filename is uid-based (claudesock.Path); rather than
			// re-deriving it, just find whatever single file the subprocess
			// created in its own isolated XDG_RUNTIME_DIR.
			socketPath := firstMatch(t, runtimeDir, 2*time.Second)
			if socketPath == "" {
				killAndWait(cmd)
				t.Fatal("socket file was never created")
			}

			if err := cmd.Process.Signal(sig.signal); err != nil {
				killAndWait(cmd)
				t.Fatalf("failed to send %s: %v", sig.name, err)
			}

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				killAndWait(cmd)
				t.Fatalf("process did not exit within 5s after %s", sig.name)
			}

			if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
				t.Fatalf("%s: expected the socket file to be removed after graceful shutdown, stat err = %v", sig.name, err)
			}
		})
	}
}

func firstMatch(t *testing.T, dir string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				return filepath.Join(dir, e.Name())
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

func killAndWait(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}
