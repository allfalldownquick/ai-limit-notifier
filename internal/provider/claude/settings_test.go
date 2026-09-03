package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func settingsFilePath(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, ".claude", "settings.json")
}

func TestSetStatusLineCommandCreatesFileIfMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SetStatusLineCommand("bash /home/user/.claude/statusline-command.sh"); err != nil {
		t.Fatal(err)
	}
	info, err := DetectStatusLine()
	if err != nil {
		t.Fatal(err)
	}
	if !info.Configured || info.Command != "bash /home/user/.claude/statusline-command.sh" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

// TestSetStatusLineCommandPreservesOtherFields is the core drift-protection
// guarantee (Correction 5): every other top-level field's VALUE survives
// byte-for-byte, even though map-based JSON re-marshaling may reorder keys
// (JSON object key order carries no meaning per spec).
func TestSetStatusLineCommandPreservesOtherFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := settingsFilePath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
  "model": "sonnet",
  "hooks": {"UserPromptSubmit": [{"matcher": "", "hooks": [{"type": "command", "command": "python3 hook.py"}]}]},
  "statusLine": {"type": "command", "command": "bash /old/statusline.sh"},
  "theme": "auto",
  "autoMode": {"soft_deny": ["Bash(rm:*)"]}
}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetStatusLineCommand("/home/user/.local/bin/ai-limit-notifier statusline-wrapper --original-command 'bash /old/statusline.sh'"); err != nil {
		t.Fatal(err)
	}

	var before, after map[string]json.RawMessage
	if err := json.Unmarshal([]byte(original), &before); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}

	for k, v := range before {
		if k == "statusLine" {
			continue
		}
		gotV, ok := after[k]
		if !ok {
			t.Fatalf("field %q disappeared", k)
		}
		// Semantic equality, not raw-byte equality: a nested object/array
		// value's internal whitespace may be reformatted (see
		// SetStatusLineCommand's doc comment) without its JSON content
		// changing. Scalar fields (model, theme) are covered more
		// strictly below since for those the two coincide.
		var wantDecoded, gotDecoded any
		if err := json.Unmarshal(v, &wantDecoded); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(gotV, &gotDecoded); err != nil {
			t.Fatal(err)
		}
		wantJSON, _ := json.Marshal(wantDecoded)
		gotJSON, _ := json.Marshal(gotDecoded)
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("field %q changed semantically: before=%s after=%s", k, v, gotV)
		}
	}
	if string(after["model"]) != `"sonnet"` || string(after["theme"]) != `"auto"` {
		t.Fatalf("scalar fields must be byte-identical: model=%s theme=%s", after["model"], after["theme"])
	}
	if len(after) != len(before) {
		t.Fatalf("key count changed: before=%d after=%d", len(before), len(after))
	}

	info, err := DetectStatusLine()
	if err != nil {
		t.Fatal(err)
	}
	if info.Command != "/home/user/.local/bin/ai-limit-notifier statusline-wrapper --original-command 'bash /old/statusline.sh'" {
		t.Fatalf("statusLine.command not updated: %+v", info)
	}
}

func TestSetStatusLineCommandPreservesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := settingsFilePath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetStatusLineCommand("echo hi"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want preserved 0600", perm)
	}
}

func TestSetStatusLineCommandNoLeftoverTempFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := SetStatusLineCommand("echo hi"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatalf("expected exactly settings.json, got %v", entries)
	}
}

func TestRemoveStatusLineCommandReturnsToAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := SetStatusLineCommand("echo hi"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStatusLineCommand(); err != nil {
		t.Fatal(err)
	}
	info, err := DetectStatusLine()
	if err != nil {
		t.Fatal(err)
	}
	if info.Configured {
		t.Fatalf("expected statusLine absent, got %+v", info)
	}
}

func TestRemoveStatusLineCommandPreservesOtherFieldsAndMissingIsNotError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := settingsFilePath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"sonnet","theme":"auto"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// No statusLine present at all -- removing it must be a harmless no-op.
	if err := RemoveStatusLineCommand(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["model"]) != `"sonnet"` || string(got["theme"]) != `"auto"` {
		t.Fatalf("unexpected content: %s", raw)
	}
	if _, ok := got["statusLine"]; ok {
		t.Fatalf("statusLine should not have been added by a no-op removal")
	}
}
