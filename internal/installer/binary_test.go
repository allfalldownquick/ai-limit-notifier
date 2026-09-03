package installer

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallBinaryPathIsUserLocalBin(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")
	got, err := InstallBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/testuser/.local/bin/ai-limit-notifier"
	if got != want {
		t.Fatalf("InstallBinaryPath() = %q, want %q", got, want)
	}
}

func TestInstallBinaryAtomicReplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	src1 := writeTempBinary(t, "content-v1")
	installed1, err := InstallBinary(src1)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, installed1, "content-v1")
	assertExecutable(t, installed1)

	// Update: install a second version over the first. The target must
	// end up with exactly the new content -- never truncated, never a
	// leftover .tmp file (proves rename-based replace, not remove-then-copy).
	src2 := writeTempBinary(t, "content-v2-longer")
	installed2, err := InstallBinary(src2)
	if err != nil {
		t.Fatal(err)
	}
	if installed1 != installed2 {
		t.Fatalf("install path changed between installs: %q vs %q", installed1, installed2)
	}
	assertFileContent(t, installed2, "content-v2-longer")
	assertExecutable(t, installed2)

	entries, err := os.ReadDir(filepath.Dir(installed2))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ai-limit-notifier" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected exactly ai-limit-notifier in the install dir, got %v", names)
	}
}

func TestInstallBinaryCreatesDirIfMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := os.Stat(filepath.Join(home, ".local", "bin")); !os.IsNotExist(err) {
		t.Fatalf("setup: .local/bin should not exist yet")
	}
	src := writeTempBinary(t, "content")
	installed, err := InstallBinary(src)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, installed, "content")
}

func writeTempBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src-binary")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("content of %s = %q, want %q (sha256 got=%x want=%x)", path, got, want, sha256.Sum256(got), sha256.Sum256([]byte(want)))
	}
}

func assertExecutable(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("%s is not owner-executable: mode=%v", path, fi.Mode())
	}
}
