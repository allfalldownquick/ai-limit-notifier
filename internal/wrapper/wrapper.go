// Package wrapper implements the Claude Code statusLine chaining wrapper.
//
// It never modifies the user's original statusLine script. Instead it sits
// in front of it: Claude Code is configured (at install time, with the
// user's explicit approval) to invoke this wrapper instead of the original
// command; the wrapper forwards the exact same stdin to the original
// command unchanged and copies its stdout/stderr/exit code straight back,
// so the visible statusLine is byte-for-byte identical to before. In
// parallel, best-effort and without ever slowing down or affecting that
// passthrough, it extracts only the four normalized rate-limit fields and
// sends them to the local monitoring agent over a Unix socket.
//
// The original command string itself is the only "install-time
// configuration" this needs (it is passed in by the caller — see
// docs/PROJECT_STATUS.md for where that string comes from) — the wrapper
// holds no other state and writes nothing to disk.
package wrapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/claude"
)

// SocketSendTimeout bounds how long the best-effort agent notification may
// take. It runs concurrently with the original command, so in practice it
// only adds latency when the original command is faster than this.
const SocketSendTimeout = 150 * time.Millisecond

// Run executes the chained statusLine command: it reads all of stdin,
// forwards it verbatim to originalCommand (run via "sh -c", matching how
// Claude Code itself invokes a configured statusLine command string),
// copies that command's stdout/stderr straight through, and returns its
// exit code. If originalCommand fails to start or exits non-zero, that
// failure is surfaced exactly as if the wrapper were not present.
//
// captureOnly is the install Case B mode (no pre-existing statusLine to
// chain to): no original command is run at all -- an empty originalCommand
// is the intended, normal state, not a misconfiguration, so nothing is
// printed and the exit code is always 0. It exists specifically so an
// empty originalCommand is never ambiguous between "nothing configured by
// mistake" (captureOnly false: an error) and "nothing to chain to by
// design" (captureOnly true: silently fine).
//
// A read failure, a malformed/unparsable payload, or an unreachable agent
// socket are all swallowed silently — none of them may change stdout,
// stderr, or the returned exit code.
func Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, originalCommand string, captureOnly bool) int {
	raw, readErr := io.ReadAll(stdin)

	if captureOnly {
		if readErr == nil {
			captureAndSend(ctx, raw)
		}
		return 0
	}

	resultCh := make(chan int, 1)
	go func() {
		resultCh <- runOriginal(ctx, raw, stdout, stderr, originalCommand)
	}()

	if readErr == nil {
		captureAndSend(ctx, raw)
	}

	return <-resultCh
}

func runOriginal(ctx context.Context, stdin []byte, stdout, stderr io.Writer, command string) int {
	if command == "" {
		fmt.Fprintln(stderr, "ai-limit-notifier: statusline-wrapper has no --original-command configured")
		return 1
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "ai-limit-notifier: failed to run original statusLine command: %v\n", err)
		return 1
	}
	return 0
}

// captureAndSend never touches disk and never returns an error the caller
// needs to act on: a missing/refused socket must be indistinguishable, from
// the outside, from the agent not existing at all.
func captureAndSend(ctx context.Context, raw []byte) {
	snap, err := claude.ParsePayload(raw)
	if err != nil {
		return
	}
	if snap.FiveHour == nil && snap.Weekly == nil {
		return // nothing to report; avoid waking the agent for no reason
	}

	sendCtx, cancel := context.WithTimeout(ctx, SocketSendTimeout)
	defer cancel()
	_ = claudesock.Send(sendCtx, snap, SocketSendTimeout)
}
