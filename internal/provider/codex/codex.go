// Package codex implements the AI Limit Notifier read-only Codex adapter.
//
// It spawns the local `codex app-server --stdio` JSON-RPC process, calls
// only `initialize` followed by `account/rateLimits/read`, and extracts the
// minimal normalized rate-limit fields. No thread/turn/prompt/inference
// method is ever invoked. See docs/REAL_MACHINE_VALIDATION.md for the
// real-machine evidence this adapter is built from.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

const defaultBinary = "codex"

// Errors returned by Read. Callers should treat any of these as "unknown",
// never as a fake zero usage value.
var (
	ErrUnavailable = errors.New("codex: app-server not available")
	ErrProtocol    = errors.New("codex: unexpected or malformed app-server response")
	ErrTimeout     = errors.New("codex: app-server did not respond in time")
)

// Reader reads normalized Codex rate limits via the local app-server.
type Reader struct {
	// Binary overrides the "codex" executable lookup. Used by tests to point
	// at a fixture script; production callers should leave it empty.
	Binary string
}

func New() *Reader { return &Reader{} }

func (r *Reader) Provider() domain.Provider { return domain.ProviderCodex }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcMessage struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Message string `json:"message"`
}

type rateLimitsResult struct {
	RateLimits struct {
		Primary   *rateWindow `json:"primary"`
		Secondary *rateWindow `json:"secondary"`
	} `json:"rateLimits"`
}

// rateWindow intentionally has no fields beyond what normalization needs.
// The real app-server response also carries credits/plan/upsell/account
// metadata; leaving those out of this struct is what keeps them from ever
// being retained or logged.
type rateWindow struct {
	UsedPercent       *float64 `json:"usedPercent"`
	WindowDurationMin *int     `json:"windowDurationMins"`
	ResetsAt          *int64   `json:"resetsAt"`
}

const (
	fiveHourMins = 300
	weeklyMins   = 10080
)

// Read performs one read-only rate-limit query. It creates no model
// thread/turn/prompt and persists nothing outside process memory.
func (r *Reader) Read(ctx context.Context) (domain.UsageSnapshot, error) {
	bin := r.Binary
	if bin == "" {
		bin = defaultBinary
	}

	// exec.CommandContext only kills the direct child on cancellation, not
	// grandchildren it may spawn. A child holding our stdout pipe open after
	// the direct child dies would hang scanner.Scan() past the deadline, so
	// the process runs in its own group and is killed as a group below.
	cmd := exec.CommandContext(ctx, bin, "app-server", "--stdio")
	cmd.Cancel = nil // avoid a redundant single-process kill racing the group kill below
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// Never surface raw stderr/stdout provider output to logs.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	killGroup := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			killGroup()
		case <-done:
		}
	}()

	defer func() {
		_ = stdin.Close()
		killGroup()
		_, _ = cmd.Process.Wait()
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	send := func(id int, method string, params any) error {
		req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
		line, err := json.Marshal(req)
		if err != nil {
			return err
		}
		line = append(line, '\n')
		_, err = stdin.Write(line)
		return err
	}

	awaitResponse := func(id int) (json.RawMessage, error) {
		for scanner.Scan() {
			var msg rpcMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue // ignore unparsable/unexpected lines rather than aborting on noise
			}
			if msg.ID == nil || *msg.ID != id {
				continue // a notification, or a response to a different request
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("%w: %s", ErrProtocol, msg.Error.Message)
			}
			return msg.Result, nil
		}
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("%w: stream closed before id=%d response", ErrProtocol, id)
	}

	initParams := map[string]any{
		"clientInfo": map[string]string{
			"name":    "ai-limit-notifier",
			"version": "0",
		},
		"capabilities": map[string]any{},
	}
	if err := send(1, "initialize", initParams); err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if _, err := awaitResponse(1); err != nil {
		return domain.UsageSnapshot{}, err
	}

	if err := send(2, "account/rateLimits/read", map[string]any{}); err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	result, err := awaitResponse(2)
	if err != nil {
		return domain.UsageSnapshot{}, err
	}

	var parsed rateLimitsResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}

	snapshot := domain.UsageSnapshot{Provider: domain.ProviderCodex}
	// windowDurationMins identifies the window kind; primary/secondary is not
	// assumed to stay positionally fixed across app-server versions.
	for _, w := range []*rateWindow{parsed.RateLimits.Primary, parsed.RateLimits.Secondary} {
		if win := windowFrom(w, domain.WindowFiveHour, fiveHourMins); win != nil {
			snapshot.FiveHour = win
		}
		if win := windowFrom(w, domain.WindowWeekly, weeklyMins); win != nil {
			snapshot.Weekly = win
		}
	}

	return snapshot, nil
}

// windowFrom returns nil (unknown) rather than a fabricated value whenever a
// required field is missing or out of range.
func windowFrom(w *rateWindow, kind domain.WindowKind, expectedMins int) *domain.UsageWindow {
	if w == nil || w.UsedPercent == nil || w.ResetsAt == nil || w.WindowDurationMin == nil {
		return nil
	}
	if *w.WindowDurationMin != expectedMins {
		return nil
	}
	used := *w.UsedPercent
	if used < 0 || used > 100 {
		return nil
	}
	if *w.ResetsAt <= 0 {
		return nil
	}
	return &domain.UsageWindow{
		Kind:        kind,
		UsedPercent: used,
		ResetAt:     time.Unix(*w.ResetsAt, 0).UTC(),
	}
}
