package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/installer"
)

// installFakeSystemctl gives activateInstalledService (and, indirectly,
// runInstall/runUninstall) a stateful fake systemctl on PATH -- enable/
// start/stop/disable leave marker files a later is-enabled/is-active
// check reflects -- so these tests never touch this machine's real
// systemd, and calls made can still be asserted on.
func installFakeSystemctl(t *testing.T) (callsLog string) {
	t.Helper()
	dir := t.TempDir()
	state := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	t.Setenv("FAKE_SYSTEMCTL_STATE", state)
	script := `#!/bin/sh
echo "$*" >> ` + log + `
STATE="$FAKE_SYSTEMCTL_STATE"
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
`
	binPath := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return log
}

func isolateInstall(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestLinkSucceedsWithNoServiceInstalled(t *testing.T) {
	isolateInstall(t)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")

	srv := fakePairServer(t, http.StatusOK, map[string]any{"linked": true, "device_id": "dev_x", "device_token": "alnd_y"})
	defer srv.Close()

	out := withCapturedStdout(t, func() {
		if code := runLink([]string{"ABCD-EFGH-JK", "--server-url", srv.URL}); code != 0 {
			t.Fatalf("runLink exit = %d", code)
		}
	})
	if !strings.Contains(out, "run `ai-limit-notifier monitor` to start reporting usage") {
		t.Fatalf("expected manual-monitor fallback message, got: %s", out)
	}
}

func TestLinkStartsInstalledServiceNotYetActive(t *testing.T) {
	isolateInstall(t)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")
	log := installFakeSystemctl(t)

	if err := installer.SaveManifest(&installer.Manifest{SchemaVersion: installer.ManifestSchemaVersion, InstalledBinaryPath: "/x"}); err != nil {
		t.Fatal(err)
	}

	srv := fakePairServer(t, http.StatusOK, map[string]any{"linked": true, "device_id": "dev_x", "device_token": "alnd_y"})
	defer srv.Close()

	out := withCapturedStdout(t, func() {
		if code := runLink([]string{"ABCD-EFGH-JK", "--server-url", srv.URL}); code != 0 {
			t.Fatalf("runLink exit = %d", code)
		}
	})
	if !strings.Contains(out, "started the installed monitor service") {
		t.Fatalf("expected start confirmation, got: %s", out)
	}
	calls := readFileOrEmpty(t, log)
	if !strings.Contains(calls, "--user start ai-limit-notifier.service") {
		t.Fatalf("expected a start call, got calls:\n%s", calls)
	}
	if strings.Contains(calls, "restart") {
		t.Fatalf("must not restart a service that wasn't active yet, got calls:\n%s", calls)
	}
}

func TestLinkRestartsInstalledServiceAlreadyActive(t *testing.T) {
	isolateInstall(t)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")
	log := installFakeSystemctl(t)

	if err := installer.SaveManifest(&installer.Manifest{SchemaVersion: installer.ManifestSchemaVersion, InstalledBinaryPath: "/x"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := installer.StartUnit(ctx); err != nil { // pre-activate via the fake
		t.Fatal(err)
	}

	srv := fakePairServer(t, http.StatusOK, map[string]any{"linked": true, "device_id": "dev_x", "device_token": "alnd_y"})
	defer srv.Close()

	out := withCapturedStdout(t, func() {
		if code := runLink([]string{"ABCD-EFGH-JK", "--server-url", srv.URL}); code != 0 {
			t.Fatalf("runLink exit = %d", code)
		}
	})
	if !strings.Contains(out, "restarted the installed monitor service") {
		t.Fatalf("expected restart confirmation, got: %s", out)
	}
	calls := readFileOrEmpty(t, log)
	if !strings.Contains(calls, "--user restart ai-limit-notifier.service") {
		t.Fatalf("expected a restart call, got calls:\n%s", calls)
	}
}

// --- install/uninstall CLI ----------------------------------------------

func TestRunInstallPlanMakesNoWrites(t *testing.T) {
	isolateInstall(t)
	installFakeSystemctl(t)
	home, _ := os.UserHomeDir()

	out := withCapturedStdout(t, func() {
		if code := runInstall([]string{"--plan"}); code != 0 {
			t.Fatalf("install --plan exit = %d", code)
		}
	})
	if !strings.Contains(out, "Plan: ai-limit-notifier install") {
		t.Fatalf("unexpected output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("install --plan must not write settings.json, stat err = %v", err)
	}
	if m, err := installer.LoadManifest(); err != nil || m != nil {
		t.Fatalf("install --plan must not write a manifest, got m=%+v err=%v", m, err)
	}
}

func TestRunUninstallPlanNotInstalledMakesNoWrites(t *testing.T) {
	isolateInstall(t)
	installFakeSystemctl(t)

	out := withCapturedStdout(t, func() {
		if code := runUninstall([]string{"--plan"}); code != 0 {
			t.Fatalf("uninstall --plan exit = %d", code)
		}
	})
	if !strings.Contains(out, "Not installed") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// TestRunInstallThenUninstallRoundTrip drives the real CLI entry points
// (runInstall, runUninstall) end to end: the currently-running test
// binary stands in for "the current executable" ApplyInstall copies (it
// only needs *some* readable file to copy/remove -- this test isn't
// exercising the copied file's own behavior, only the install/uninstall
// orchestration and its effect on settings.json).
func TestRunInstallThenUninstallRoundTrip(t *testing.T) {
	isolateInstall(t)
	installFakeSystemctl(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"model":"sonnet","statusLine":{"type":"command","command":"bash /home/user/.claude/statusline-command.sh"}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	installOut := withCapturedStdout(t, func() {
		if code := runInstall(nil); code != 0 {
			t.Fatalf("install exit = %d", code)
		}
	})
	if !strings.Contains(installOut, "install: complete") {
		t.Fatalf("unexpected install output: %s", installOut)
	}

	binPath, err := installer.InstallBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("binary not installed: %v", err)
	}
	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settingsRaw), "statusline-wrapper") {
		t.Fatalf("settings.json wasn't updated to the wrapper form: %s", settingsRaw)
	}
	if !strings.Contains(string(settingsRaw), "sonnet") {
		t.Fatalf("unrelated field 'model' lost: %s", settingsRaw)
	}

	uninstallOut := withCapturedStdout(t, func() {
		if code := runUninstall(nil); code != 0 {
			t.Fatalf("uninstall exit = %d", code)
		}
	})
	if !strings.Contains(uninstallOut, "uninstall: complete") {
		t.Fatalf("unexpected uninstall output: %s", uninstallOut)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("expected binary removed after uninstall, stat err = %v", err)
	}
	settingsRaw, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(settingsRaw) == original {
		// fine -- exact match not required (key order may differ), fall
		// through to the semantic check below
	}
	if !strings.Contains(string(settingsRaw), "bash /home/user/.claude/statusline-command.sh") || strings.Contains(string(settingsRaw), "statusline-wrapper") {
		t.Fatalf("original statusLine not restored after uninstall: %s", settingsRaw)
	}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
