// Package claude implements the AI Limit Notifier read-only Claude Code
// adapter, built from the real-machine statusLine capture recorded in
// docs/REAL_MACHINE_VALIDATION.md.
//
// Unlike Codex, Claude Code has no on-demand rate-limit query interface:
// data only arrives when Claude Code itself invokes the configured
// statusLine command on its own refresh cadence. This package therefore
// exposes a pure payload parser (for the data Claude Code hands to the
// statusLine command) plus read-only detection helpers, rather than a
// synchronous Read(ctx) like the Codex adapter. Turning that passive capture
// into a continuously available snapshot is P2 (RAM-only monitoring agent)
// work, not yet implemented.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

var (
	ErrUnavailable = errors.New("claude: claude binary not available")
	ErrMalformed   = errors.New("claude: malformed statusLine/settings payload")
)

type statusLinePayload struct {
	RateLimits struct {
		FiveHour *rateWindow `json:"five_hour"`
		SevenDay *rateWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

// rateWindow intentionally carries only the two proven fields. The real
// statusLine payload also includes workspace/context_window/model fields;
// leaving those out of this struct is what keeps them from ever being
// retained.
type rateWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

// ParsePayload extracts a normalized snapshot from a Claude Code statusLine
// JSON payload (the same shape Claude Code writes to the configured
// statusLine command's stdin). A missing window is left nil (unknown);
// out-of-range values are treated the same way rather than clamped or
// fabricated.
func ParsePayload(raw []byte) (domain.UsageSnapshot, error) {
	var p statusLinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.UsageSnapshot{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	snapshot := domain.UsageSnapshot{Provider: domain.ProviderClaude}
	snapshot.FiveHour = windowFrom(p.RateLimits.FiveHour, domain.WindowFiveHour)
	snapshot.Weekly = windowFrom(p.RateLimits.SevenDay, domain.WindowWeekly)
	return snapshot, nil
}

func windowFrom(w *rateWindow, kind domain.WindowKind) *domain.UsageWindow {
	if w == nil || w.UsedPercentage == nil || w.ResetsAt == nil {
		return nil
	}
	used := *w.UsedPercentage
	if used < 0 || used > 100 {
		return nil
	}
	resets := *w.ResetsAt
	if resets <= 0 {
		return nil
	}
	return &domain.UsageWindow{
		Kind:        kind,
		UsedPercent: used,
		ResetAt:     time.Unix(int64(resets), 0).UTC(),
	}
}

// StatusLineInfo reports whether Claude Code has a statusLine command
// configured, without modifying or executing it.
type StatusLineInfo struct {
	Configured bool
	Command    string
}

// DetectStatusLine reads ~/.claude/settings.json read-only. A missing file
// or missing statusLine block is reported as not configured, not an error.
func DetectStatusLine() (StatusLineInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return StatusLineInfo{}, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return StatusLineInfo{}, nil
	}
	if err != nil {
		return StatusLineInfo{}, err
	}

	var settings struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return StatusLineInfo{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if settings.StatusLine.Command == "" {
		return StatusLineInfo{}, nil
	}
	return StatusLineInfo{Configured: true, Command: settings.StatusLine.Command}, nil
}

// Version runs the installed Claude Code CLI's --version flag. This is a
// local process invocation, not a model/API call.
func Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return strings.TrimSpace(string(out)), nil
}
