// Package localconfig is the local agent's one persistent write: static
// link configuration — where the server is and this device's credential —
// never runtime usage/history/reset state. See SECURITY.md's local
// runtime write policy: install-time static configuration is explicitly
// allowed; periodically-rewritten monitored-usage state is not. This file
// is written once by `ai-limit-notifier link` and only read afterward.
package localconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion identifies this file's shape, in case a future version
// needs to change it without breaking an older client reading a newer
// (or vice versa) config file.
const SchemaVersion = 1

// Config is deliberately narrow: nothing here is monitored usage, reset
// history, a provider payload, a prompt, or a Claude/OpenAI credential —
// only what's needed to reconnect to the already-paired server after a
// reboot.
type Config struct {
	SchemaVersion int    `json:"schema_version"`
	ServerURL     string `json:"server_url"`
	DeviceID      string `json:"device_id"`
	DeviceToken   string `json:"device_token"`
}

// Path resolves the config file location following XDG convention:
// $XDG_CONFIG_HOME/ai-limit-notifier/config.json, falling back to
// ~/.config/ai-limit-notifier/config.json. Never hardcodes a particular
// user's home directory.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ai-limit-notifier", "config.json"), nil
}

// Load reads the config file. A missing file is not an error — it returns
// a zero-value Config (every field empty), matching "link was never run".
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// Save atomically writes cfg: config directory 0700, config file 0600,
// written to a temp file in the same directory and renamed into place —
// so a crash mid-write never leaves a corrupt or partially-written
// credential file where the real config is expected, and any failure
// before the rename leaves no leftover temp file behind.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	// MkdirAll doesn't correct the mode of a directory that already
	// existed with different permissions; set it explicitly regardless of
	// umask or prior state.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set config directory permissions: %w", err)
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath) // never leave a partial credential file behind
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("set config file permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	committed = true
	return nil
}
