package installer

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- classification ---------------------------------------------------------

func TestClassifyStatusLineAbsent(t *testing.T) {
	for _, in := range []string{"", "   "} {
		got := ClassifyStatusLine(in)
		if got.State != StatusLineAbsent {
			t.Fatalf("ClassifyStatusLine(%q) = %v, want absent", in, got.State)
		}
	}
}

func TestClassifyStatusLineExistingNonNotifier(t *testing.T) {
	got := ClassifyStatusLine("bash /home/user/.claude/statusline-command.sh")
	if got.State != StatusLineExistingNonNotifier {
		t.Fatalf("State = %v, want existing-non-notifier", got.State)
	}
	if got.OriginalCommand != "bash /home/user/.claude/statusline-command.sh" {
		t.Fatalf("OriginalCommand = %q", got.OriginalCommand)
	}
}

func TestClassifyStatusLineMalformed(t *testing.T) {
	got := ClassifyStatusLine("something statusline-wrapper but not our shape at all")
	if got.State != StatusLineMalformed {
		t.Fatalf("State = %v, want malformed", got.State)
	}
}

func TestClassifyStatusLineRoundTripsBuildStatusLineCommand(t *testing.T) {
	cases := []struct {
		name     string
		original string
		wantWith State
	}{
		{"with original", "bash /home/user/.claude/statusline-command.sh", StatusLineNotifierWithOriginal},
		{"case B, no original", "", StatusLineNotifierWithoutOriginal},
		{"original with single quotes", `echo 'hi there'`, StatusLineNotifierWithOriginal},
		{"original with $() and $VAR", `echo $(whoami) $HOME`, StatusLineNotifierWithOriginal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			built := BuildStatusLineCommand("/home/user/.local/bin/ai-limit-notifier", c.original)
			got := ClassifyStatusLine(built)
			if got.State != c.wantWith {
				t.Fatalf("State = %v, want %v (built=%q)", got.State, c.wantWith, built)
			}
			if got.OriginalCommand != c.original {
				t.Fatalf("OriginalCommand = %q, want %q", got.OriginalCommand, c.original)
			}
		})
	}
}

func TestClassifyStatusLineLegacyManualWrapper(t *testing.T) {
	legacy := `/home/zyvka/ai-limit-notifier/ai-limit-notifier statusline-wrapper --original-command 'bash /home/zyvka/.claude/statusline-command.sh'`
	got := ClassifyStatusLine(legacy)
	if got.State != StatusLineNotifierWithOriginal {
		t.Fatalf("State = %v, want notifier-with-original", got.State)
	}
	if got.OriginalCommand != "bash /home/zyvka/.claude/statusline-command.sh" {
		t.Fatalf("OriginalCommand = %q", got.OriginalCommand)
	}
}

func TestClassifyStatusLineLegacyManualWrapperArbitraryQuoting(t *testing.T) {
	original := `echo 'it'\''s a test' | tr a-z A-Z`
	legacy := "/opt/bin/ai-limit-notifier statusline-wrapper --original-command " + ShellQuote(original)
	got := ClassifyStatusLine(legacy)
	if got.State != StatusLineNotifierWithOriginal || got.OriginalCommand != original {
		t.Fatalf("State=%v OriginalCommand=%q, want notifier-with-original / %q", got.State, got.OriginalCommand, original)
	}
}

// --- parseShellQuotedWords ---------------------------------------------------

func TestParseShellQuotedWordsRoundTrip(t *testing.T) {
	words := []string{"simple", "with space", "with'quote", "", "$(cmd) $VAR \\backslash", "unicode: héllo 日本語"}
	var built []byte
	for i, w := range words {
		if i > 0 {
			built = append(built, ' ')
		}
		built = append(built, ShellQuote(w)...)
	}
	got, err := parseShellQuotedWords(string(built))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(words) {
		t.Fatalf("got %d words, want %d: %#v", len(got), len(words), got)
	}
	for i := range words {
		if got[i] != words[i] {
			t.Fatalf("word %d = %q, want %q", i, got[i], words[i])
		}
	}
}

func TestParseShellQuotedWordsRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"unquoted", "'unterminated", "'a''b'no space between", "'a'  'b'"} {
		if _, err := parseShellQuotedWords(bad); err == nil {
			t.Fatalf("expected an error for %q", bad)
		}
	}
}

// --- real sh -c round-trip: fail-open fallback branch (binary missing) -----

// These prove the *fallback* branch (`exec sh -c "$1"`) reproduces exactly
// what running the original command directly would produce -- the case
// that matters most: the notifier binary is gone/broken, and the user's
// pre-existing statusLine must behave completely unaffected.
func TestBuildStatusLineCommandFallbackMatchesDirectExecution(t *testing.T) {
	missingBinary := filepath.Join(t.TempDir(), "does-not-exist")

	cases := []struct {
		name     string
		original string
	}{
		{"single quotes", `echo 'hello world'`},
		{"double quotes", `echo "hello world"`},
		{"spaces", `echo   spaced   words`},
		{"command substitution", `echo $(echo nested)`},
		{"variable expansion", `X=bar; echo $X`},
		{"backslashes", `printf 'a\\nb\n'`},
		{"pipes", `echo hello | tr a-z A-Z`},
		{"redirects", `echo redirected >/dev/null; echo done`},
		{"unicode", `echo 'héllo wörld 日本語'`},
		{"empty output", `true`},
		{"non-zero exit", `exit 42`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantOut, wantExit := runSh(t, c.original)

			generated := BuildStatusLineCommand(missingBinary, c.original)
			gotOut, gotExit := runSh(t, generated)

			if gotOut != wantOut {
				t.Fatalf("stdout mismatch:\n direct:    %q\n generated: %q", wantOut, gotOut)
			}
			if gotExit != wantExit {
				t.Fatalf("exit code mismatch: direct=%d generated=%d", wantExit, gotExit)
			}
		})
	}
}

func TestBuildStatusLineCommandCaseBFallbackIsSilent(t *testing.T) {
	missingBinary := filepath.Join(t.TempDir(), "does-not-exist")
	generated := BuildStatusLineCommand(missingBinary, "")
	out, exit := runSh(t, generated)
	if out != "" || exit != 0 {
		t.Fatalf("Case B with missing binary: out=%q exit=%d, want empty/0", out, exit)
	}
}

// runSh executes s via a real `sh -c` and returns combined stdout+stderr
// and the exit code -- exactly how Claude Code is understood to invoke a
// configured statusLine command string.
func runSh(t *testing.T, s string) (output string, exitCode int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", s)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err == nil {
		return buf.String(), 0
	}
	var exitErr *exec.ExitError
	if ok := asExitError(err, &exitErr); ok {
		return buf.String(), exitErr.ExitCode()
	}
	t.Fatalf("failed to run %q: %v", s, err)
	return "", -1
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// --- real sh -c round-trip: binary present (real compiled wrapper) --------

func buildWrapperBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "ai-limit-notifier")
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/ai-limit-notifier")
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building ai-limit-notifier failed: %v\n%s", err, out)
	}
	return binPath
}

func TestBuildStatusLineCommandCaseAWithBinaryPresent(t *testing.T) {
	bin := buildWrapperBinary(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // isolate the capture socket

	originalScript := filepath.Join(t.TempDir(), "original.sh")
	if err := os.WriteFile(originalScript, []byte("#!/bin/sh\ncat\nprintf ' [tail]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "sh " + originalScript

	generated := BuildStatusLineCommand(bin, original)
	cmd := exec.Command("sh", "-c", generated)
	cmd.Stdin = bytes.NewBufferString(`{}`)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\noutput: %s", err, buf.String())
	}
	if got := buf.String(); got != "{} [tail]" {
		t.Fatalf("stdout = %q, want %q", got, "{} [tail]")
	}
}

func TestBuildStatusLineCommandCaseBWithBinaryPresent(t *testing.T) {
	bin := buildWrapperBinary(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	generated := BuildStatusLineCommand(bin, "")
	cmd := exec.Command("sh", "-c", generated)
	cmd.Stdin = bytes.NewBufferString(`{}`)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\noutput: %s", err, buf.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("capture-only output = %q, want empty", buf.String())
	}
}
