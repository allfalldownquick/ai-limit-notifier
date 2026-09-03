package installer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
)

// ErrBlocked is returned by Apply* when the plan is Blocked (Drift or a
// malformed existing statusLine) -- callers must never attempt to apply a
// blocked plan.
var ErrBlocked = errors.New("installer: plan is blocked, see Plan.BlockedReason")

// ApplyInstall carries out an install plan produced by ComputeInstallPlan.
// srcBinaryPath is the currently-running executable (os.Executable(),
// resolved by the caller so this package stays easy to test without
// relying on the test binary's own identity).
//
// Deliberately does NOT touch ~/.claude/settings.json itself -- the
// statusLine flip is the caller's job, done last, after this returns
// successfully (see cmd/ai-limit-notifier's install command): if anything
// here fails or the process crashes before that last step, the user's
// live statusLine is completely unaffected, and a repeated `install` is a
// clean, idempotent redo (ClassifyStatusLine recognizes this package's own
// already-installed form just as readily as the pre-P5 legacy one).
func ApplyInstall(ctx context.Context, plan *Plan, srcBinaryPath string) error {
	if plan.Blocked {
		return fmt.Errorf("%w: %s", ErrBlocked, plan.BlockedReason)
	}
	if plan.AlreadyInstalled {
		return nil
	}

	if _, err := InstallBinary(srcBinaryPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}

	if plan.SystemdAvailable {
		unitPath, err := WriteUnit(plan.UnitContent)
		if err != nil {
			return fmt.Errorf("write systemd unit: %w", err)
		}
		if verified, err := VerifyUnit(ctx, unitPath); err != nil {
			return fmt.Errorf("generated systemd unit failed verification: %w", err)
		} else {
			_ = verified // unsupported-here is not a failure; see VerifyUnit's doc comment
		}
		if err := DaemonReloadUser(ctx); err != nil {
			return fmt.Errorf("systemctl --user daemon-reload: %w", err)
		}
		if err := EnableUnit(ctx); err != nil {
			return fmt.Errorf("enable systemd unit: %w", err)
		}
		if plan.WillStartUnit {
			if err := StartUnit(ctx); err != nil {
				return fmt.Errorf("start systemd unit: %w", err)
			}
		}
	}

	m := &Manifest{
		SchemaVersion:       ManifestSchemaVersion,
		InstalledBinaryPath: plan.BinaryPath,
		InstalledAt:         time.Now().UTC().Format(time.RFC3339),
		StatusLineBeforeInstall: StatusLineBeforeInstall{
			Existed:         plan.StatusLineOriginalExisted,
			OriginalCommand: plan.StatusLineOriginalCommand,
		},
		AutostartUnitInstalled: plan.SystemdAvailable,
		AutostartUnitPath:      plan.UnitPath,
		CodexBinDir:            plan.CodexBinDir,
	}
	if err := SaveManifest(m); err != nil {
		return fmt.Errorf("save install manifest: %w", err)
	}

	return nil
}

// ApplyUninstall carries out an uninstall plan produced by
// ComputeUninstallPlan. Like ApplyInstall, it does not touch
// ~/.claude/settings.json -- the caller restores/removes the statusLine
// (using plan.StatusLineProposedCommand or plan.StatusLineWillBeRemoved)
// first if it wants the safest ordering (statusLine restored before the
// service is torn down and the binary that might still be referenced by a
// not-yet-updated settings.json is removed), then calls ApplyUninstall for
// everything else. It never touches internal/localconfig's link
// credential/threshold file.
func ApplyUninstall(ctx context.Context, plan *Plan) error {
	if plan.NotInstalled {
		return nil
	}
	if plan.Blocked {
		return fmt.Errorf("%w: %s", ErrBlocked, plan.BlockedReason)
	}

	if plan.SystemdAvailable {
		if plan.UnitCurrentlyActive {
			if err := StopUnit(ctx); err != nil {
				return fmt.Errorf("stop systemd unit: %w", err)
			}
		}
		if plan.UnitCurrentlyEnabled {
			if err := DisableUnit(ctx); err != nil {
				return fmt.Errorf("disable systemd unit: %w", err)
			}
		}
	}
	if err := RemoveUnit(); err != nil {
		return fmt.Errorf("remove systemd unit file: %w", err)
	}
	if plan.SystemdAvailable {
		if err := DaemonReloadUser(ctx); err != nil {
			return fmt.Errorf("systemctl --user daemon-reload: %w", err)
		}
	}

	if err := removeStaleSocketIfAny(); err != nil {
		return fmt.Errorf("remove runtime socket: %w", err)
	}

	if err := removeInstalledBinary(plan.BinaryPath); err != nil {
		return fmt.Errorf("remove installed binary: %w", err)
	}

	if err := RemoveManifest(); err != nil {
		return fmt.Errorf("remove install manifest: %w", err)
	}

	return nil
}

// removeStaleSocketIfAny removes the RAM-backed capture socket file if it
// exists and nothing is actually listening on it -- the same crash-cleanup
// judgment ServeClaudeSocket itself makes on startup (see
// internal/agent.ServeClaudeSocket), applied here so `uninstall` leaves no
// stray socket entry behind either. Never removes a socket something is
// genuinely still listening on (that would mean a monitor is still
// running -- ApplyUninstall stops the service first, but a manually
// started `monitor` in a foreground terminal wouldn't be caught by that).
func removeStaleSocketIfAny() error {
	path := claudesock.Path()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if conn, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil // a real listener is still there; leave it alone
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// removeInstalledBinary deletes the installed binary, if any. A missing
// file is not an error (e.g. it was already manually removed, which is
// exactly the scenario that motivated the fail-open work in the first
// place).
func removeInstalledBinary(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
