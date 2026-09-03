package installer

import (
	"context"
	"os"
	"os/user"

	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
)

// Plan is the complete, read-only description of what `install` or
// `uninstall` would do. `--plan` prints exactly this, and apply acts on
// exactly this (computed fresh, not cached), so display and behavior can
// never drift apart.
type Plan struct {
	Uninstall bool // false = install plan, true = uninstall plan

	AlreadyInstalled bool // install: nothing left to do, fully idempotent
	NotInstalled     bool // uninstall: no manifest, nothing to do

	// Drift (Correction 5): a manifest exists and the live
	// statusLine.command no longer matches what it should be if notifier
	// still owns it untouched since the last install/uninstall. When
	// true, nothing in this plan is safe to apply -- Blocked is also true.
	Drift       bool
	DriftDetail string

	Blocked       bool // true if Drift, or the current statusLine is StatusLineMalformed
	BlockedReason string

	BinaryPath            string
	BinaryCurrentlyExists bool

	StatusLineCurrentState    State
	StatusLineCurrentCommand  string
	StatusLineProposedCommand string // "" if nothing will be written
	StatusLineWillBeRemoved   bool   // uninstall Case B: notifier-added statusLine goes back to absent
	StatusLineOriginalExisted bool
	StatusLineOriginalCommand string

	CodexBinDir   string
	CodexResolved bool

	SystemdAvailable     bool
	UnitPath             string
	UnitContent          string
	UnitCurrentlyEnabled bool
	UnitCurrentlyActive  bool
	WillEnableUnit       bool
	WillDisableUnit      bool // uninstall
	WillStartUnit        bool
	StartReason          string

	LingerCurrentlyEnabled bool // report only -- install/uninstall never change it

	Linked bool

	Manifest *Manifest // nil for a fresh install plan with no prior manifest
}

func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

func isLinked() bool {
	cfg, err := localconfig.Load()
	if err != nil || cfg == nil {
		return false
	}
	return cfg.ServerURL != "" && cfg.DeviceToken != ""
}

// expectedManagedCommand reconstructs the exact statusLine.command value
// that should be live if notifier's last install/uninstall action is
// still untouched -- the single source of truth both drift detection and
// idempotency checks compare the real current value against.
func expectedManagedCommand(binaryPath string, before StatusLineBeforeInstall) string {
	original := ""
	if before.Existed {
		original = before.OriginalCommand
	}
	return BuildStatusLineCommand(binaryPath, original)
}

// ComputeInstallPlan builds the install plan. currentStatusLineCommand is
// the raw current ~/.claude/settings.json statusLine.command value ("" if
// absent) -- read by the caller, since this package doesn't touch Claude
// settings directly (see cmd/ai-limit-notifier's install command).
func ComputeInstallPlan(ctx context.Context, currentStatusLineCommand string) (*Plan, error) {
	p := &Plan{}

	binaryPath, err := InstallBinaryPath()
	if err != nil {
		return nil, err
	}
	p.BinaryPath = binaryPath
	if _, err := os.Stat(binaryPath); err == nil {
		p.BinaryCurrentlyExists = true
	}

	manifest, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	p.Manifest = manifest

	p.SystemdAvailable = SystemdUserAvailable(ctx)
	p.UnitPath, _ = SystemdUnitPath()
	p.UnitCurrentlyEnabled = p.SystemdAvailable && UnitIsEnabled(ctx)
	p.UnitCurrentlyActive = p.SystemdAvailable && UnitIsActive(ctx)
	p.LingerCurrentlyEnabled = p.SystemdAvailable && LingerEnabled(ctx, currentUsername())
	p.Linked = isLinked()

	classified := ClassifyStatusLine(currentStatusLineCommand)
	p.StatusLineCurrentState = classified.State
	p.StatusLineCurrentCommand = currentStatusLineCommand

	if manifest != nil {
		expected := expectedManagedCommand(manifest.InstalledBinaryPath, manifest.StatusLineBeforeInstall)
		if currentStatusLineCommand == expected {
			if p.BinaryCurrentlyExists && (!p.SystemdAvailable || p.UnitCurrentlyEnabled) {
				p.AlreadyInstalled = true
			}
			// Still fully idempotent to re-run even if not "already
			// installed" in every respect (e.g. the unit got disabled
			// externally) -- fall through and recompute a normal plan
			// using the manifest's own recorded original, so re-install
			// repairs drift-free state without re-classifying anything.
			p.StatusLineOriginalExisted = manifest.StatusLineBeforeInstall.Existed
			p.StatusLineOriginalCommand = manifest.StatusLineBeforeInstall.OriginalCommand
			p.StatusLineProposedCommand = expected
		} else {
			p.Drift = true
			p.Blocked = true
			p.DriftDetail = "a previous install recorded statusLine.command as " +
				quoteForDisplay(expected) + " but it is currently " + quoteForDisplay(currentStatusLineCommand) +
				" -- looks hand-edited since install; refusing to overwrite it automatically"
			p.BlockedReason = p.DriftDetail
			return p, nil
		}
	} else {
		switch classified.State {
		case StatusLineMalformed:
			p.Blocked = true
			p.BlockedReason = "current statusLine.command mentions statusline-wrapper but doesn't match a shape this installer recognizes -- refusing to guess"
			return p, nil
		case StatusLineAbsent:
			p.StatusLineOriginalExisted = false
			p.StatusLineProposedCommand = BuildStatusLineCommand(binaryPath, "")
		default: // ExistingNonNotifier, NotifierWithOriginal (new or legacy), NotifierWithoutOriginal
			p.StatusLineOriginalExisted = classified.State == StatusLineExistingNonNotifier || classified.State == StatusLineNotifierWithOriginal
			p.StatusLineOriginalCommand = classified.OriginalCommand
			p.StatusLineProposedCommand = BuildStatusLineCommand(binaryPath, classified.OriginalCommand)
		}
	}

	if dir, ok := ResolveCodexBinDir(); ok {
		p.CodexBinDir = dir
		p.CodexResolved = true
	}
	p.UnitContent = GenerateUnit(p.CodexBinDir)
	p.WillEnableUnit = p.SystemdAvailable
	// Correction 4: install-before-link. A brand new service with no
	// saved device credential would fail closed on every start and
	// Restart=on-failure would just loop; only start it once a valid link
	// already exists. `enable` still happens either way, so a later
	// `link` only needs to start/restart, never enable, the unit.
	if p.SystemdAvailable && p.Linked {
		p.WillStartUnit = true
		p.StartReason = "a valid link config already exists"
	} else if p.SystemdAvailable {
		p.StartReason = "not linked yet -- service will be enabled but left inactive until `link` succeeds"
	} else {
		p.StartReason = "systemd --user is not available in this environment"
	}

	return p, nil
}

// ComputeUninstallPlan builds the uninstall plan.
func ComputeUninstallPlan(ctx context.Context, currentStatusLineCommand string) (*Plan, error) {
	p := &Plan{Uninstall: true}

	manifest, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	p.Manifest = manifest
	if manifest == nil {
		p.NotInstalled = true
		return p, nil
	}

	p.BinaryPath = manifest.InstalledBinaryPath
	if _, err := os.Stat(p.BinaryPath); err == nil {
		p.BinaryCurrentlyExists = true
	}
	p.SystemdAvailable = SystemdUserAvailable(ctx)
	p.UnitPath, _ = SystemdUnitPath()
	p.UnitCurrentlyEnabled = p.SystemdAvailable && UnitIsEnabled(ctx)
	p.UnitCurrentlyActive = p.SystemdAvailable && UnitIsActive(ctx)
	p.WillDisableUnit = p.SystemdAvailable && (p.UnitCurrentlyEnabled || p.UnitCurrentlyActive)

	p.StatusLineCurrentState = ClassifyStatusLine(currentStatusLineCommand).State
	p.StatusLineCurrentCommand = currentStatusLineCommand

	expected := expectedManagedCommand(manifest.InstalledBinaryPath, manifest.StatusLineBeforeInstall)
	if currentStatusLineCommand != expected {
		p.Drift = true
		p.Blocked = true
		p.DriftDetail = "statusLine.command no longer matches what install last set (expected " +
			quoteForDisplay(expected) + ", found " + quoteForDisplay(currentStatusLineCommand) +
			") -- looks hand-edited since install; refusing to restore/remove it automatically"
		p.BlockedReason = p.DriftDetail
		return p, nil
	}

	p.StatusLineOriginalExisted = manifest.StatusLineBeforeInstall.Existed
	p.StatusLineOriginalCommand = manifest.StatusLineBeforeInstall.OriginalCommand
	if manifest.StatusLineBeforeInstall.Existed {
		p.StatusLineProposedCommand = manifest.StatusLineBeforeInstall.OriginalCommand
	} else {
		p.StatusLineWillBeRemoved = true
	}

	return p, nil
}

func quoteForDisplay(s string) string {
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return "\"" + s + "\""
}
