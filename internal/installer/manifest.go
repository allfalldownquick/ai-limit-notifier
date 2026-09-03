package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ManifestSchemaVersion identifies the install manifest's shape.
const ManifestSchemaVersion = 1

// StatusLineBeforeInstall records only what uninstall needs to restore
// exact prior state -- never the payload content, never anything beyond
// the one command string that was (or wasn't) already configured.
type StatusLineBeforeInstall struct {
	Existed         bool   `json:"existed"`
	OriginalCommand string `json:"original_command,omitempty"`
}

// Manifest is the complete install-time state uninstall needs. It
// deliberately holds nothing beyond that: no usage, reset history,
// prompts, responses, terminal contents, project paths, or provider
// credentials -- the same class of static, install-time-only data
// internal/localconfig.Config already stores for the link credential.
type Manifest struct {
	SchemaVersion           int                     `json:"schema_version"`
	InstalledBinaryPath     string                  `json:"installed_binary_path"`
	InstalledAt             string                  `json:"installed_at"` // RFC3339
	StatusLineBeforeInstall StatusLineBeforeInstall `json:"statusline_before_install"`
	AutostartUnitInstalled  bool                    `json:"autostart_unit_installed"`
	AutostartUnitPath       string                  `json:"autostart_unit_path,omitempty"`
	// CodexBinDir is the directory containing the codex (and node, since
	// codex's shebang needs it on PATH too) executable, resolved once at
	// install time via exec.LookPath in the operator's own interactive
	// invocation of `install` -- never re-derived by sourcing shell
	// startup files at service-run time. Empty if codex could not be
	// resolved at install time.
	CodexBinDir string `json:"codex_bin_dir,omitempty"`
}

// ManifestPath resolves the manifest location: $XDG_CONFIG_HOME (falling
// back to ~/.config) /ai-limit-notifier/install.json -- the same directory
// internal/localconfig uses for config.json, a different file, so install
// state and link credentials are never accidentally read/written as if
// they were the same document.
func ManifestPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ai-limit-notifier", "install.json"), nil
}

// LoadManifest reads the manifest. A missing file is not an error -- it
// returns (nil, nil), matching "not installed".
func LoadManifest() (*Manifest, error) {
	path, err := ManifestPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// SaveManifest atomically writes m: directory 0700, file 0600, temp file
// in the same directory + fsync + rename -- the same durability pattern
// internal/localconfig.Save already uses for the link credential file.
func SaveManifest(m *Manifest) error {
	path, err := ManifestPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set config directory permissions: %w", err)
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".install-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp manifest file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp manifest file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp manifest file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp manifest file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("set manifest file permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace manifest file: %w", err)
	}
	committed = true
	return nil
}

// RemoveManifest deletes the manifest file, if any. A missing file is not
// an error.
func RemoveManifest() error {
	path, err := ManifestPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
