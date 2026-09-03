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

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// SetStatusLineCommand atomically sets statusLine.command in
// ~/.claude/settings.json to command. Every other top-level field is
// decoded generically as map[string]json.RawMessage -- never into a
// narrower struct that could silently drop a field this package doesn't
// know about -- and only the "statusLine" entry is replaced. Two cosmetic,
// non-semantic changes are possible in the rewritten file: top-level key
// order (Go's encoding/json marshals maps in alphabetical key order; JSON
// object key order carries no meaning per spec) and the internal
// indentation of a nested object/array value (Go's Indent pass reformats
// embedded raw JSON to the surrounding style rather than preserving its
// original whitespace verbatim). No field's actual JSON content --
// scalars byte-for-byte, objects/arrays semantically -- is ever altered.
// Creates the file (and ~/.claude) if it doesn't exist yet.
func SetStatusLineCommand(command string) error {
	return patchSettings(func(m map[string]json.RawMessage) error {
		raw, err := json.Marshal(struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		}{Type: "command", Command: command})
		if err != nil {
			return err
		}
		m["statusLine"] = raw
		return nil
	})
}

// RemoveStatusLineCommand atomically removes the statusLine key entirely
// -- install Case B's uninstall path, returning the file to "no statusLine
// configured" exactly as it would be if notifier had never touched it.
// Removing an already-absent key is not an error. Every other field is
// preserved the same way SetStatusLineCommand preserves them.
func RemoveStatusLineCommand() error {
	return patchSettings(func(m map[string]json.RawMessage) error {
		delete(m, "statusLine")
		return nil
	})
}

func patchSettings(mutate func(map[string]json.RawMessage) error) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	m := map[string]json.RawMessage{}
	mode := os.FileMode(0o644)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Starting fresh: no existing settings.json at all.
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		if fi, statErr := os.Stat(path); statErr == nil {
			mode = fi.Mode().Perm()
		}
	}

	if err := mutate(m); err != nil {
		return err
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp settings file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp settings file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp settings file: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("set settings file permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	committed = true
	return nil
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
