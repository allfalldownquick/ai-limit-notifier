package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
)

// isolate sets HOME/XDG_CONFIG_HOME/XDG_RUNTIME_DIR to fresh temp dirs and
// installs a fake systemctl/systemd-analyze/loginctl on PATH, so nothing
// here can ever touch this machine's real systemd, home directory, or
// Claude config.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		t.Fatal(err)
	}

	// A stateful fake systemctl: enable/disable/start/stop/restart leave
	// marker files behind so a later is-enabled/is-active reflects what
	// ApplyInstall/ApplyUninstall actually did, not a hardcoded answer --
	// real end-to-end round-tripping through the same code path, never
	// touching this machine's real systemd.
	state := t.TempDir()
	t.Setenv("FAKE_SYSTEMCTL_STATE", state)
	installFakeTool(t, "systemctl", `STATE="$FAKE_SYSTEMCTL_STATE"
verb="$2"
case "$verb" in
  enable) touch "$STATE/enabled" ;;
  disable) rm -f "$STATE/enabled" ;;
  start|restart) touch "$STATE/active" ;;
  stop) rm -f "$STATE/active" ;;
  is-enabled) [ -f "$STATE/enabled" ] || exit 1 ;;
  is-active) [ -f "$STATE/active" ] || exit 1 ;;
esac
exit 0
`)
	installFakeTool(t, "systemd-analyze", `exit 0`)
	installFakeTool(t, "loginctl", `echo no`)
}

func TestApplyInstallFreshNoStatusLine(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	plan, err := ComputeInstallPlan(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked {
		t.Fatalf("unexpected block: %s", plan.BlockedReason)
	}

	src := writeTempBinary(t, "fake-binary-content")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(plan.BinaryPath); err != nil {
		t.Fatalf("binary not installed: %v", err)
	}
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected a manifest to be saved")
	}
	if m.StatusLineBeforeInstall.Existed {
		t.Fatalf("expected no prior statusLine recorded, got %+v", m.StatusLineBeforeInstall)
	}
	if m.InstalledBinaryPath != plan.BinaryPath {
		t.Fatalf("manifest binary path = %q, want %q", m.InstalledBinaryPath, plan.BinaryPath)
	}
}

func TestApplyInstallWithExistingStatusLine(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	original := "bash /home/user/.claude/statusline-command.sh"
	plan, err := ComputeInstallPlan(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	if plan.StatusLineProposedCommand == "" || plan.StatusLineOriginalCommand != original {
		t.Fatalf("unexpected plan: %+v", plan)
	}

	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}

	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if !m.StatusLineBeforeInstall.Existed || m.StatusLineBeforeInstall.OriginalCommand != original {
		t.Fatalf("manifest didn't record the real original: %+v", m.StatusLineBeforeInstall)
	}
}

func TestApplyInstallMigratesLegacyManualWrapper(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	trueOriginal := "bash /home/zyvka/.claude/statusline-command.sh"
	legacy := "/home/zyvka/ai-limit-notifier/ai-limit-notifier statusline-wrapper --original-command " + ShellQuote(trueOriginal)

	plan, err := ComputeInstallPlan(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if plan.StatusLineOriginalCommand != trueOriginal {
		t.Fatalf("expected the extracted true original, got %q", plan.StatusLineOriginalCommand)
	}
	// Must not wrap the wrapper: the proposed command should be built from
	// trueOriginal, never from the legacy string itself.
	reclassified := ClassifyStatusLine(plan.StatusLineProposedCommand)
	if reclassified.OriginalCommand != trueOriginal {
		t.Fatalf("proposed command doesn't chain to the true original: got %q", reclassified.OriginalCommand)
	}

	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadManifest()
	if m.StatusLineBeforeInstall.OriginalCommand != trueOriginal {
		t.Fatalf("manifest recorded the wrong original: %+v", m.StatusLineBeforeInstall)
	}
}

func TestApplyInstallIdempotentSecondRun(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	original := "bash /home/user/.claude/statusline-command.sh"
	plan1, err := ComputeInstallPlan(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan1, src); err != nil {
		t.Fatal(err)
	}

	// Re-run with the now-current (already-wrapped) statusLine command.
	plan2, err := ComputeInstallPlan(ctx, plan1.StatusLineProposedCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.AlreadyInstalled {
		t.Fatalf("expected AlreadyInstalled on a clean re-run, got %+v", plan2)
	}
	if err := ApplyInstall(ctx, plan2, src); err != nil {
		t.Fatalf("idempotent re-apply must not error: %v", err)
	}
}

func TestComputeInstallPlanDetectsDrift(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	original := "bash /home/user/.claude/statusline-command.sh"
	plan1, err := ComputeInstallPlan(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan1, src); err != nil {
		t.Fatal(err)
	}

	// User hand-edits statusLine after install.
	handEdited := "bash /home/user/some-other-script.sh"
	plan2, err := ComputeInstallPlan(ctx, handEdited)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Drift || !plan2.Blocked {
		t.Fatalf("expected drift to be detected, got %+v", plan2)
	}
	if err := ApplyInstall(ctx, plan2, src); err == nil {
		t.Fatal("expected ApplyInstall to refuse a blocked/drifted plan")
	}
}

func TestComputeInstallPlanMalformedIsBlocked(t *testing.T) {
	isolate(t)
	plan, err := ComputeInstallPlan(context.Background(), "something statusline-wrapper weird --unrecognized-shape")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Blocked {
		t.Fatal("expected a malformed existing statusLine to block the plan")
	}
}

func TestComputeInstallPlanInstallBeforeLink(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	// No link config exists in this isolated environment.
	plan, err := ComputeInstallPlan(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Linked {
		t.Fatal("setup: expected not linked")
	}
	if plan.WillStartUnit {
		t.Fatal("must not start the service before a valid link exists")
	}
	if !plan.WillEnableUnit {
		t.Fatal("enable is independent of link state")
	}
}

func TestComputeInstallPlanStartsIfAlreadyLinked(t *testing.T) {
	isolate(t)
	if err := localconfig.Save(&localconfig.Config{
		SchemaVersion: localconfig.SchemaVersion,
		ServerURL:     "https://example.com",
		DeviceID:      "dev_x",
		DeviceToken:   "alnd_secret",
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := ComputeInstallPlan(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.WillStartUnit {
		t.Fatal("expected the plan to start the service when already linked")
	}
}

// --- uninstall ---------------------------------------------------------

func TestApplyUninstallRestoresOriginal(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	original := "bash /home/user/.claude/statusline-command.sh"
	plan, err := ComputeInstallPlan(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}

	uplan, err := ComputeUninstallPlan(ctx, plan.StatusLineProposedCommand)
	if err != nil {
		t.Fatal(err)
	}
	if uplan.NotInstalled || uplan.Blocked {
		t.Fatalf("unexpected uninstall plan: %+v", uplan)
	}
	if uplan.StatusLineProposedCommand != original {
		t.Fatalf("expected restore to %q, got %q", original, uplan.StatusLineProposedCommand)
	}

	if err := ApplyUninstall(ctx, uplan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.BinaryPath); !os.IsNotExist(err) {
		t.Fatalf("expected binary removed, stat err = %v", err)
	}
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatalf("expected manifest removed, got %+v", m)
	}
}

func TestApplyUninstallCaseBReturnsToAbsent(t *testing.T) {
	isolate(t)
	ctx := context.Background()

	plan, err := ComputeInstallPlan(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}

	uplan, err := ComputeUninstallPlan(ctx, plan.StatusLineProposedCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !uplan.StatusLineWillBeRemoved {
		t.Fatalf("expected Case B uninstall to remove statusLine entirely, got %+v", uplan)
	}
}

func TestComputeUninstallPlanNotInstalled(t *testing.T) {
	isolate(t)
	plan, err := ComputeUninstallPlan(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NotInstalled {
		t.Fatal("expected NotInstalled with no manifest present")
	}
}

func TestComputeUninstallPlanDetectsDrift(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	original := "bash /home/user/.claude/statusline-command.sh"
	plan, err := ComputeInstallPlan(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}

	handEdited := "bash /home/user/something-else-entirely.sh"
	uplan, err := ComputeUninstallPlan(ctx, handEdited)
	if err != nil {
		t.Fatal(err)
	}
	if !uplan.Drift || !uplan.Blocked {
		t.Fatalf("expected uninstall drift detection, got %+v", uplan)
	}
	if err := ApplyUninstall(ctx, uplan); err == nil {
		t.Fatal("expected ApplyUninstall to refuse a blocked/drifted plan")
	}
	// The binary must survive an aborted, blocked uninstall attempt.
	if _, err := os.Stat(plan.BinaryPath); err != nil {
		t.Fatalf("blocked uninstall must not have removed the binary: %v", err)
	}
}

func TestApplyUninstallNeverTouchesLinkConfig(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	if err := localconfig.Save(&localconfig.Config{
		SchemaVersion:         localconfig.SchemaVersion,
		ServerURL:             "https://example.com",
		DeviceID:              "dev_x",
		DeviceToken:           "alnd_secret",
		NotificationThreshold: 42,
	}); err != nil {
		t.Fatal(err)
	}

	plan, err := ComputeInstallPlan(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	src := writeTempBinary(t, "fake-binary")
	if err := ApplyInstall(ctx, plan, src); err != nil {
		t.Fatal(err)
	}
	uplan, err := ComputeUninstallPlan(ctx, plan.StatusLineProposedCommand)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyUninstall(ctx, uplan); err != nil {
		t.Fatal(err)
	}

	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceToken != "alnd_secret" || cfg.ServerURL != "https://example.com" || cfg.NotificationThreshold != 42 {
		t.Fatalf("uninstall must never touch link config, got %+v", cfg)
	}
}
