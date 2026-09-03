package installer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- GenerateUnit (pure) -----------------------------------------------

func TestGenerateUnitNoAfterDependency(t *testing.T) {
	got := GenerateUnit("")
	if strings.Contains(got, "After=") || strings.Contains(got, "Wants=") {
		t.Fatalf("unit must not add an After=/Wants= dependency without justification, got:\n%s", got)
	}
}

func TestGenerateUnitShape(t *testing.T) {
	got := GenerateUnit("")
	for _, want := range []string{
		"ExecStart=%h/.local/bin/ai-limit-notifier monitor",
		"Restart=on-failure",
		"RestartSec=5",
		"StandardOutput=null",
		"StandardError=null",
		"WantedBy=default.target",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Environment=") {
		t.Fatalf("no Environment= line expected when codexBinDir is empty, got:\n%s", got)
	}
}

func TestGenerateUnitWithCodexBinDir(t *testing.T) {
	got := GenerateUnit("/home/user/.nvm/versions/node/v22.23.1/bin")
	want := "Environment=PATH=/home/user/.nvm/versions/node/v22.23.1/bin:" + baseServicePATH
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q in unit, got:\n%s", want, got)
	}
}

// --- WriteUnit / RemoveUnit (real file ops, isolated $HOME) ---------------

func TestWriteUnitThenRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	content := GenerateUnit("")
	path, err := WriteUnit(content)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, ".config", "systemd", "user", "ai-limit-notifier.service")
	if path != wantPath {
		t.Fatalf("WriteUnit path = %q, want %q", path, wantPath)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("written content mismatch")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Fatalf("unit file mode = %o, want 0644", perm)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ai-limit-notifier.service" {
		t.Fatalf("expected exactly the unit file, got %v", entries)
	}

	if err := RemoveUnit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected unit file removed, stat err = %v", err)
	}

	if err := RemoveUnit(); err != nil {
		t.Fatalf("removing an already-absent unit must not error: %v", err)
	}
}

// --- systemctl/systemd-analyze/loginctl wrappers, against a fake binary --

// installFakeTool writes an executable shell script named `name` into a
// fresh directory prepended to PATH for this test, so exec.Command(name,
// ...) in the code under test runs the fake instead of any real system
// tool -- these tests must never touch this machine's real systemd/login
// state. argsLogPath, if non-empty, gets one line of the fake's received
// argv per invocation appended to it.
func installFakeTool(t *testing.T, name, script string) (binDir string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

func TestSystemctlWrapperArgs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	installFakeTool(t, "systemctl", `echo "$*" >> `+logPath+`
exit 0
`)
	ctx := context.Background()
	if err := DaemonReloadUser(ctx); err != nil {
		t.Fatal(err)
	}
	if err := EnableUnit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := StartUnit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := RestartUnit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := StopUnit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := DisableUnit(ctx); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(raw))
	want := strings.Join([]string{
		"--user daemon-reload",
		"--user enable ai-limit-notifier.service",
		"--user start ai-limit-notifier.service",
		"--user restart ai-limit-notifier.service",
		"--user stop ai-limit-notifier.service",
		"--user disable ai-limit-notifier.service",
	}, "\n")
	if got != want {
		t.Fatalf("systemctl calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestSystemctlWrapperSurfacesFailure(t *testing.T) {
	installFakeTool(t, "systemctl", `echo "boom" >&2
exit 1
`)
	if err := StartUnit(context.Background()); err == nil {
		t.Fatal("expected an error when systemctl fails")
	} else if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the underlying failure output in the error, got: %v", err)
	}
}

func TestUnitIsActiveAndEnabled(t *testing.T) {
	installFakeTool(t, "systemctl", `exit 0`)
	if !UnitIsActive(context.Background()) {
		t.Fatal("expected UnitIsActive true when systemctl is-active succeeds")
	}
	if !UnitIsEnabled(context.Background()) {
		t.Fatal("expected UnitIsEnabled true when systemctl is-enabled succeeds")
	}
}

func TestUnitIsActiveAndEnabledFalse(t *testing.T) {
	installFakeTool(t, "systemctl", `exit 3`)
	if UnitIsActive(context.Background()) {
		t.Fatal("expected UnitIsActive false when systemctl is-active fails")
	}
	if UnitIsEnabled(context.Background()) {
		t.Fatal("expected UnitIsEnabled false when systemctl is-enabled fails")
	}
}

func TestSystemdUserAvailable(t *testing.T) {
	installFakeTool(t, "systemctl", `exit 0`)
	if !SystemdUserAvailable(context.Background()) {
		t.Fatal("expected SystemdUserAvailable true")
	}
}

func TestSystemdUserUnavailable(t *testing.T) {
	installFakeTool(t, "systemctl", `exit 1`)
	if SystemdUserAvailable(context.Background()) {
		t.Fatal("expected SystemdUserAvailable false")
	}
}

func TestVerifyUnitPasses(t *testing.T) {
	installFakeTool(t, "systemd-analyze", `exit 0`)
	verified, err := VerifyUnit(context.Background(), "/some/unit/path")
	if err != nil || !verified {
		t.Fatalf("verified=%v err=%v, want true/nil", verified, err)
	}
}

func TestVerifyUnitFails(t *testing.T) {
	installFakeTool(t, "systemd-analyze", `echo "bad unit: Environment= line invalid" >&2
exit 1
`)
	verified, err := VerifyUnit(context.Background(), "/some/unit/path")
	if err == nil || verified {
		t.Fatalf("verified=%v err=%v, want false/non-nil", verified, err)
	}
	if !strings.Contains(err.Error(), "Environment=") {
		t.Fatalf("expected verify failure detail in error, got: %v", err)
	}
}

func TestVerifyUnitUnsupportedIsNotAFailure(t *testing.T) {
	installFakeTool(t, "systemd-analyze", `echo "Unknown operation --user" >&2
exit 1
`)
	verified, err := VerifyUnit(context.Background(), "/some/unit/path")
	if err != nil {
		t.Fatalf("an unsupported --user flag must not surface as an error, got: %v", err)
	}
	if verified {
		t.Fatal("expected verified=false when verification isn't supported here")
	}
}

func TestLingerEnabled(t *testing.T) {
	installFakeTool(t, "loginctl", `echo "yes"
exit 0
`)
	if !LingerEnabled(context.Background(), "testuser") {
		t.Fatal("expected LingerEnabled true")
	}
}

func TestLingerDisabled(t *testing.T) {
	installFakeTool(t, "loginctl", `echo "no"
exit 0
`)
	if LingerEnabled(context.Background(), "testuser") {
		t.Fatal("expected LingerEnabled false")
	}
}
