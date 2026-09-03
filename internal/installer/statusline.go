// Package installer implements the P5 installation workflow: a durable
// user-local binary path, Claude statusLine chaining (with a shell-level
// fail-open that survives the notifier binary itself going missing), a
// systemd --user autostart unit, and a minimal install manifest for
// uninstall. Nothing here performs a model request, and nothing here
// writes monitored usage/history/prompt/response data — only the same
// class of static, install-time configuration internal/localconfig
// already writes.
package installer

import (
	"errors"
	"strings"
)

// StatusLineState classifies the current state of
// ~/.claude/settings.json's statusLine.command.
type StatusLineState int

const (
	// StatusLineAbsent means no statusLine is configured at all.
	StatusLineAbsent StatusLineState = iota
	// StatusLineExistingNonNotifier means a real user statusLine command
	// is configured and it is not ours (install Case A).
	StatusLineExistingNonNotifier
	// StatusLineNotifierWithOriginal means our fail-open command (or the
	// earlier manually-installed direct-wrapper form) is installed,
	// chaining to a real original command.
	StatusLineNotifierWithOriginal
	// StatusLineNotifierWithoutOriginal means our fail-open command is
	// installed in capture-only form (install Case B already applied).
	StatusLineNotifierWithoutOriginal
	// StatusLineMalformed means the command looks notifier-related (it
	// mentions statusline-wrapper) but doesn't parse as either of our
	// known generated shapes -- never guess at a mapping here.
	StatusLineMalformed
)

func (s StatusLineState) String() string {
	switch s {
	case StatusLineAbsent:
		return "absent"
	case StatusLineExistingNonNotifier:
		return "existing-non-notifier"
	case StatusLineNotifierWithOriginal:
		return "notifier-with-original"
	case StatusLineNotifierWithoutOriginal:
		return "notifier-without-original"
	case StatusLineMalformed:
		return "malformed"
	default:
		return "unknown"
	}
}

// ClassifiedStatusLine is the result of inspecting the current
// statusLine.command value.
type ClassifiedStatusLine struct {
	State State
	// OriginalCommand is set for StatusLineNotifierWithOriginal and
	// StatusLineExistingNonNotifier: the real command a rollback/uninstall
	// must restore, or that Case A must chain to. Empty for the other
	// three states.
	OriginalCommand string
}

// State is an alias kept for readability at call sites (installer.State).
type State = StatusLineState

// ClassifyStatusLine inspects the raw current statusLine.command value (""
// if statusLine is absent/not configured) and classifies it without
// guessing: only the two shapes this package itself can generate --
// today's fail-open command and the earlier manually-installed direct
// "<bin> statusline-wrapper --original-command '<original>'" form -- are
// recognized as notifier-managed.
func ClassifyStatusLine(currentCommand string) ClassifiedStatusLine {
	if strings.TrimSpace(currentCommand) == "" {
		return ClassifiedStatusLine{State: StatusLineAbsent}
	}

	if orig, ok := parseFailOpenCommand(currentCommand); ok {
		if orig == "" {
			return ClassifiedStatusLine{State: StatusLineNotifierWithoutOriginal}
		}
		return ClassifiedStatusLine{State: StatusLineNotifierWithOriginal, OriginalCommand: orig}
	}
	if orig, ok := parseLegacyManualWrapperCommand(currentCommand); ok {
		if orig == "" {
			return ClassifiedStatusLine{State: StatusLineNotifierWithoutOriginal}
		}
		return ClassifiedStatusLine{State: StatusLineNotifierWithOriginal, OriginalCommand: orig}
	}
	if strings.Contains(currentCommand, "statusline-wrapper") {
		return ClassifiedStatusLine{State: StatusLineMalformed}
	}
	return ClassifiedStatusLine{State: StatusLineExistingNonNotifier, OriginalCommand: currentCommand}
}

// --- fail-open command: build ---------------------------------------------

// wrapperScriptBody is a fixed POSIX sh script with zero dynamic content --
// $1 (original command) and $2 (notifier binary path) arrive as plain argv
// values from the positional arguments BuildStatusLineCommand appends
// after "sh -c '<this script>' sh", never concatenated into the script
// text itself, so nothing here needs to escape untrusted input.
const wrapperScriptBody = `if [ -x "$2" ]; then
  if [ -n "$1" ]; then
    exec "$2" statusline-wrapper --original-command "$1"
  else
    exec "$2" statusline-wrapper --capture-only
  fi
else
  if [ -n "$1" ]; then
    exec sh -c "$1"
  else
    exit 0
  fi
fi
`

// BuildStatusLineCommand returns the exact value to store as
// ~/.claude/settings.json's statusLine.command. Claude Code's own
// invocation of this string (itself presumed to go through a shell, since
// statusLine.command is a shell command string) resolves -- entirely at
// the shell level, with no dependency on the Go wrapper binary being
// reachable -- whether binaryPath is present and whether originalCommand
// is non-empty:
//
//	binary present, original present -> notifier wrapper chains to original
//	binary present, original absent  -> notifier capture-only (Case B)
//	binary missing,  original present -> original runs directly (fail-open)
//	binary missing,  original absent  -> exit 0, no output
//
// originalCommand == "" means Case B (no pre-existing statusLine to chain
// to). Round-trips exactly through ParseFailOpenCommand (this package's
// own tests exercise the string through a real sh -c, not just this
// round-trip).
func BuildStatusLineCommand(binaryPath, originalCommand string) string {
	tokens := []string{"sh", "-c", wrapperScriptBody, "sh", originalCommand, binaryPath}
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = ShellQuote(t)
	}
	return strings.Join(quoted, " ")
}

// parseFailOpenCommand reverses BuildStatusLineCommand. It only succeeds
// against exactly the shape this exact version of the package generates
// (fixed script body included) -- an older/different generated shape is
// deliberately left to fall through to StatusLineMalformed rather than
// guessed at.
func parseFailOpenCommand(cmd string) (original string, ok bool) {
	tokens, err := parseShellQuotedWords(cmd)
	if err != nil || len(tokens) != 6 {
		return "", false
	}
	if tokens[0] != "sh" || tokens[1] != "-c" || tokens[2] != wrapperScriptBody || tokens[3] != "sh" {
		return "", false
	}
	return tokens[4], true
}

// --- legacy manually-installed direct wrapper: parse -----------------------

const legacyMarker = " statusline-wrapper --original-command "

// parseLegacyManualWrapperCommand recognizes the pre-P5 form this project
// itself told an operator to install by hand: "<bin> statusline-wrapper
// --original-command '<original>'" -- no fail-open guard, a direct
// invocation. Needed so `install` migrates this machine's actual current
// state (see docs) instead of nesting a second wrapper around it.
func parseLegacyManualWrapperCommand(cmd string) (original string, ok bool) {
	idx := strings.Index(cmd, legacyMarker)
	if idx <= 0 {
		return "", false
	}
	tail := cmd[idx+len(legacyMarker):]
	tokens, err := parseShellQuotedWords(tail)
	if err != nil || len(tokens) != 1 {
		return "", false
	}
	return tokens[0], true
}

// --- POSIX single-quote shell word (de)serialization -----------------------

// ShellQuote renders s as one POSIX shell single-quoted word: wrapped in
// single quotes, with every literal single quote replaced by the standard
// '\” escape (close quote, escaped quote, reopen quote). Safe for
// arbitrary byte content -- nothing inside single quotes is expanded by a
// POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parseShellQuotedWords parses a string that is expected to be exactly a
// sequence of ShellQuote-produced words separated by single ASCII spaces --
// not general shell parsing, only the narrow, fully-specified grammar this
// package's own generator produces. Returns an error for anything that
// doesn't match that grammar exactly (unquoted content, tabs/newlines
// between words, an unterminated quote, and so on).
func parseShellQuotedWords(s string) ([]string, error) {
	var words []string
	i := 0
	for i < len(s) {
		if s[i] != '\'' {
			return nil, errors.New("expected a single-quoted word")
		}
		i++
		var b strings.Builder
		closed := false
		for i < len(s) {
			if s[i] == '\'' {
				// Either the closing quote, or the start of a '\''
				// escaped-literal-quote sequence.
				if strings.HasPrefix(s[i:], `'\''`) {
					b.WriteByte('\'')
					i += 4
					continue
				}
				i++
				closed = true
				break
			}
			b.WriteByte(s[i])
			i++
		}
		if !closed {
			return nil, errors.New("unterminated quoted word")
		}
		words = append(words, b.String())
		if i < len(s) {
			if s[i] != ' ' {
				return nil, errors.New("expected a single space between words")
			}
			i++
			if i >= len(s) {
				return nil, errors.New("trailing space after last word")
			}
		}
	}
	return words, nil
}
