package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitName = "ai-limit-notifier.service"

// baseServicePATH is the minimal PATH a systemd --user unit would already
// have. codexBinDir (if resolved) is prepended to it, never replacing it,
// so anything else `monitor` might need to exec still resolves normally.
const baseServicePATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// SystemdUnitPath returns ~/.config/systemd/user/ai-limit-notifier.service
// -- the standard user-unit location, root not required.
func SystemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", unitName), nil
}

// ResolveCodexBinDir looks up "codex" via the CALLING process's own PATH --
// meant to be invoked from within `install`'s normal, interactive,
// operator-run invocation (where a shell has already sourced whatever
// makes codex resolve, e.g. nvm via .bashrc), never from inside the
// long-running service itself. Returns ("", false) if codex isn't
// resolvable here either; that's reported as an install --plan warning,
// never silently ignored, and never worked around with a shell-startup
// hack at service-run time.
func ResolveCodexBinDir() (dir string, ok bool) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", false
	}
	// exec.LookPath may return a relative path if "codex" was found via a
	// relative PATH entry (unusual, but POSIX-legal); resolve to absolute
	// so the generated unit's Environment=PATH= entry is unambiguous
	// regardless of the installer's own working directory.
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return filepath.Dir(abs), true
}

// GenerateUnit renders the systemd --user unit file content. ExecStart
// uses the portable %h specifier (systemd expands it to the user's home
// directory) rather than a baked-in absolute path. codexBinDir may be ""
// (codex wasn't resolved at install time): the Environment= line is then
// simply omitted, and the service still starts -- Claude tracking works
// either way, only autostart Codex polling would fail silently-to-the-service
// (still visible via `doctor`).
//
// No After=/Wants= dependency is added: this is a plain user-level
// long-running process with no ordering requirement on any other unit,
// and default.target is reached long before user units like this one
// would meaningfully need to wait on it.
func GenerateUnit(codexBinDir string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=AI Limit Notifier monitor\n")
	b.WriteString("\n[Service]\n")
	b.WriteString("ExecStart=%h/.local/bin/ai-limit-notifier monitor\n")
	if codexBinDir != "" {
		fmt.Fprintf(&b, "Environment=PATH=%s:%s\n", codexBinDir, baseServicePATH)
	}
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	// Correction 3: notifier must not supply a runtime log stream for
	// persistent journald storage, even though (verified against the
	// current code) production `monitor` never prints usage percentages
	// itself -- don't rely on that alone holding forever.
	b.WriteString("StandardOutput=null\n")
	b.WriteString("StandardError=null\n")
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// WriteUnit atomically writes the unit file content to SystemdUnitPath():
// temp file in the same directory, synced, then renamed into place.
func WriteUnit(content string) (string, error) {
	path, err := SystemdUnitPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".unit-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp unit file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write temp unit file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("sync temp unit file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp unit file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return "", fmt.Errorf("set unit file permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("replace unit file: %w", err)
	}
	committed = true
	return path, nil
}

// RemoveUnit deletes the unit file, if any. A missing file is not an error.
func RemoveUnit() error {
	path, err := SystemdUnitPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// --- systemctl --user / systemd-analyze wrappers ---------------------------

// SystemdUserAvailable reports whether `systemctl --user` is usable at
// all in this environment -- checked once so the rest of install/uninstall
// can degrade to "unit not installed, diagnostic shown" instead of
// failing the whole command when systemd --user genuinely isn't there.
func SystemdUserAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "show-environment")
	return cmd.Run() == nil
}

// VerifyUnit runs `systemd-analyze --user verify <path>` if that
// subcommand is supported by the installed systemd-analyze (older
// versions lack the --user flag); returns (true, nil) when verification
// ran and passed, (false, nil) when verification isn't supported here
// (not a failure -- just unavailable), and (false, err) when verification
// ran and found a real problem.
func VerifyUnit(ctx context.Context, path string) (verified bool, err error) {
	out, err := exec.CommandContext(ctx, "systemd-analyze", "--user", "verify", path).CombinedOutput()
	if err == nil {
		return true, nil
	}
	if strings.Contains(string(out), "Unknown operation") || strings.Contains(string(out), "unrecognized option") {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, fmt.Errorf("systemd-analyze --user verify: %s", strings.TrimSpace(string(out)))
	}
	return false, nil // systemd-analyze itself not found etc.: treat as unavailable, not a hard failure
}

func systemctlUser(ctx context.Context, args ...string) error {
	full := append([]string{"--user"}, args...)
	out, err := exec.CommandContext(ctx, "systemctl", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DaemonReloadUser re-reads unit files after WriteUnit/RemoveUnit.
func DaemonReloadUser(ctx context.Context) error {
	return systemctlUser(ctx, "daemon-reload")
}

// EnableUnit enables (but does not start) the unit, matching install's
// "enabled, started only if a valid link exists" split.
func EnableUnit(ctx context.Context) error {
	return systemctlUser(ctx, "enable", unitName)
}

// DisableUnit disables the unit (used by uninstall, after stopping it).
func DisableUnit(ctx context.Context) error {
	return systemctlUser(ctx, "disable", unitName)
}

// StartUnit starts (or, if already running, restarts nothing -- systemctl
// start on an already-active unit is a no-op) the unit.
func StartUnit(ctx context.Context) error {
	return systemctlUser(ctx, "start", unitName)
}

// RestartUnit restarts the unit so a freshly-saved credential/threshold
// takes effect immediately, without waiting for the next natural exit.
func RestartUnit(ctx context.Context) error {
	return systemctlUser(ctx, "restart", unitName)
}

// StopUnit stops the unit if running; stopping an inactive unit is not an
// error.
func StopUnit(ctx context.Context) error {
	return systemctlUser(ctx, "stop", unitName)
}

// UnitIsActive reports whether the unit is currently running.
func UnitIsActive(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", unitName)
	return cmd.Run() == nil
}

// UnitIsEnabled reports whether the unit is enabled (independent of
// whether it's currently running).
func UnitIsEnabled(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-enabled", "--quiet", unitName)
	return cmd.Run() == nil
}

// LingerEnabled reports the current linger state for the given username,
// read-only -- install/uninstall never change it (Correction 2: "Linger:
// unchanged" is the P5 default), this exists only so `install --plan`/
// `doctor` can show the current value.
func LingerEnabled(ctx context.Context, username string) bool {
	out, err := exec.CommandContext(ctx, "loginctl", "show-user", username, "--property=Linger", "--value").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "yes"
}
