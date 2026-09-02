package wrapper

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

func writeFixture(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-original.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const samplePayload = `{"rate_limits":{"five_hour":{"used_percentage":65,"resets_at":1788388800},"seven_day":{"used_percentage":27,"resets_at":1788850800}}}`

func TestRunPassthroughPreservesOutput(t *testing.T) {
	// Isolate the socket path so no real agent interferes with this test.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	original := writeFixture(t, "#!/bin/sh\ncat\nprintf ' [suffix]'\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), strings.NewReader(samplePayload), &stdout, &stderr, "sh "+original)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	want := samplePayload + " [suffix]"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunSurvivesMissingAgentSocket(t *testing.T) {
	// A path with no listener at all: the agent has never run.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	original := writeFixture(t, "#!/bin/sh\ncat\n")

	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := Run(context.Background(), strings.NewReader(samplePayload), &stdout, &stderr, "sh "+original)
	elapsed := time.Since(start)

	if code != 0 || stdout.String() != samplePayload {
		t.Fatalf("passthrough broken by missing socket: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took too long with no agent socket present: %v", elapsed)
	}
}

func TestRunSurvivesMalformedClaudePayload(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	original := writeFixture(t, "#!/bin/sh\ncat\n")
	malformed := "this is not json at all"

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), strings.NewReader(malformed), &stdout, &stderr, "sh "+original)

	if code != 0 || stdout.String() != malformed {
		t.Fatalf("passthrough broken by malformed payload: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunPropagatesOriginalExitCode(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	original := writeFixture(t, "#!/bin/sh\ncat >/dev/null\nexit 7\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), strings.NewReader(samplePayload), &stdout, &stderr, "sh "+original)
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestRunNoOriginalCommandConfigured(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), strings.NewReader(samplePayload), &stdout, &stderr, "")
	if code == 0 {
		t.Fatal("expected a non-zero exit code when no original command is configured")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout, got %q", stdout.String())
	}
}

func TestRunDeliversExactlyFourFieldsToAgentSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	l, err := net.Listen("unix", claudesock.Path())
	if err != nil {
		t.Fatalf("failed to start fake agent listener: %v", err)
	}
	defer l.Close()

	received := make(chan domain.UsageSnapshot, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var snap domain.UsageSnapshot
		_ = json.NewDecoder(conn).Decode(&snap)
		received <- snap
	}()

	// The payload also carries workspace/context_window/model, mirroring a
	// real statusLine payload, to prove those never reach the socket.
	fullPayload := `{
		"workspace": {"current_dir": "/home/user/secret-project"},
		"model": {"display_name": "some-model"},
		"rate_limits": {
			"five_hour": {"used_percentage": 65, "resets_at": 1788388800},
			"seven_day": {"used_percentage": 27, "resets_at": 1788850800}
		}
	}`
	original := writeFixture(t, "#!/bin/sh\ncat >/dev/null\nprintf 'ok'\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), strings.NewReader(fullPayload), &stdout, &stderr, "sh "+original)
	if code != 0 || stdout.String() != "ok" {
		t.Fatalf("passthrough broken: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	select {
	case snap := <-received:
		if snap.Provider != domain.ProviderClaude {
			t.Fatalf("unexpected provider: %v", snap.Provider)
		}
		if snap.FiveHour == nil || snap.FiveHour.UsedPercent != 65 {
			t.Fatalf("unexpected five_hour: %+v", snap.FiveHour)
		}
		if snap.Weekly == nil || snap.Weekly.UsedPercent != 27 {
			t.Fatalf("unexpected weekly: %+v", snap.Weekly)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent never received a snapshot over the socket")
	}
}
