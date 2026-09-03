package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManifestPathUsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := ManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "ai-limit-notifier", "install.json")
	if got != want {
		t.Fatalf("ManifestPath() = %q, want %q", got, want)
	}
}

func TestLoadManifestMissingFileReturnsNilNotError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	m, err := LoadManifest()
	if err != nil {
		t.Fatalf("unexpected error for a missing manifest: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil manifest, got %+v", m)
	}
}

func TestSaveThenLoadManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := &Manifest{
		SchemaVersion:       ManifestSchemaVersion,
		InstalledBinaryPath: "/home/user/.local/bin/ai-limit-notifier",
		InstalledAt:         "2026-09-03T12:00:00Z",
		StatusLineBeforeInstall: StatusLineBeforeInstall{
			Existed:         true,
			OriginalCommand: "bash /home/user/.claude/statusline-command.sh",
		},
		AutostartUnitInstalled: true,
		AutostartUnitPath:      "/home/user/.config/systemd/user/ai-limit-notifier.service",
		CodexBinDir:            "/home/user/.nvm/versions/node/v22.23.1/bin",
	}
	if err := SaveManifest(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Fatalf("LoadManifest() = %+v, want %+v", got, want)
	}
}

func TestSaveManifestWritesCorrectPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := SaveManifest(&Manifest{SchemaVersion: ManifestSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	path, _ := ManifestPath()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("manifest file mode = %o, want 0600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("manifest directory mode = %o, want 0700", perm)
	}
}

func TestSaveManifestNoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := SaveManifest(&Manifest{SchemaVersion: ManifestSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "ai-limit-notifier"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "install.json" {
		t.Fatalf("expected exactly install.json, got %v", entries)
	}
}

func TestRemoveManifestMissingIsNotError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := RemoveManifest(); err != nil {
		t.Fatalf("unexpected error removing a nonexistent manifest: %v", err)
	}
}

func TestRemoveManifestDeletesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := SaveManifest(&Manifest{SchemaVersion: ManifestSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveManifest(); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatalf("expected manifest gone, got %+v", m)
	}
}
