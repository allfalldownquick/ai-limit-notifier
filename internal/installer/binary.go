package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// InstallBinaryPath returns the durable, user-local, non-root install
// target: ~/.local/bin/ai-limit-notifier -- never a path inside a git
// working tree or any other disposable build-artifact location a later
// cleanup pass might delete out from under a configured statusLine.
func InstallBinaryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin", "ai-limit-notifier"), nil
}

// InstallBinary atomically copies the file at srcPath (expected to be the
// currently-running executable, os.Executable()) to InstallBinaryPath: a
// temp file in the same directory, synced, chmod 0755, then renamed into
// place. A configured statusLine's shell-level check for "does the binary
// exist and is it executable" therefore never observes a half-written
// file -- the rename is the only moment the target path's content
// changes, and it changes atomically. This is the update-safe path: never
// remove the old binary before the new one is fully in place.
func InstallBinary(srcPath string) (installedPath string, err error) {
	dst, err := InstallBinaryPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open source binary: %w", err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp(dir, ".ai-limit-notifier-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp binary file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", fmt.Errorf("copy binary: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("sync temp binary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp binary file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", fmt.Errorf("set binary permissions: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return "", fmt.Errorf("replace installed binary: %w", err)
	}
	committed = true
	return dst, nil
}
