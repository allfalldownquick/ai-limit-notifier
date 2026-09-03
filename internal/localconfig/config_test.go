package localconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathUsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "ai-limit-notifier", "config.json")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestSaveWritesCorrectPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := &Config{SchemaVersion: SchemaVersion, ServerURL: "https://example.com", DeviceID: "dev_test", DeviceToken: "alnd_test-token"}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 0600", perm)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config directory mode = %o, want 0700", perm)
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := &Config{SchemaVersion: SchemaVersion, ServerURL: "https://example.com", DeviceID: "dev_abc", DeviceToken: "alnd_secret"}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadMissingFileReturnsZeroValueNotError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error for a missing config file: %v", err)
	}
	if cfg.ServerURL != "" || cfg.DeviceID != "" || cfg.DeviceToken != "" {
		t.Fatalf("expected a zero-value Config, got %+v", cfg)
	}
}

func TestSaveAtomicNoLeftoverTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := Save(&Config{SchemaVersion: SchemaVersion, ServerURL: "https://example.com", DeviceID: "d", DeviceToken: "t"}); err != nil {
		t.Fatal(err)
	}

	configDir := filepath.Join(dir, "ai-limit-notifier")
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected exactly config.json in the directory, got %v", names)
	}
}

func TestSaveFailureLeavesNoCredentialFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Pre-create the config directory as a *file* so MkdirAll fails,
	// simulating a save that can't complete.
	configPath, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(configPath), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = Save(&Config{SchemaVersion: SchemaVersion, ServerURL: "https://example.com", DeviceID: "d", DeviceToken: "t"})
	if err == nil {
		t.Fatal("expected Save to fail when its directory can't be created")
	}
	if _, statErr := os.Stat(configPath); statErr == nil {
		t.Fatal("a failed save must not leave a config/credential file behind")
	}
}
