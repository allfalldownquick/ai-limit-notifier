// Command ai-limit-notifier is the local diagnostics CLI defined by
// docs/INSTALLER_CONTRACT.md. This build implements the read-only P1
// surface (detect, doctor, show-payload, status); install/link/uninstall
// are not yet implemented.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/claude"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
)

const readTimeout = 10 * time.Second

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(3)
	}

	var code int
	switch os.Args[1] {
	case "detect":
		code = runDetect(os.Args[2:])
	case "doctor":
		code = runDoctor(os.Args[2:])
	case "show-payload":
		code = runShowPayload(os.Args[2:])
	case "status":
		code = runStatus(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		code = 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		code = 3
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, `ai-limit-notifier <command>

Commands:
  detect         Read-only environment/provider detection.
  doctor         Read-only diagnostics for configured provider readers.
  show-payload   Show the exact normalized data a future server call would send.
  status         Concise current local state.`)
}

// --- shared helpers -------------------------------------------------------

type windowJSON struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at"`
}

type snapshotJSON struct {
	Provider string      `json:"provider"`
	FiveHour *windowJSON `json:"five_hour,omitempty"`
	Weekly   *windowJSON `json:"weekly,omitempty"`
}

func toSnapshotJSON(s domain.UsageSnapshot) snapshotJSON {
	out := snapshotJSON{Provider: string(s.Provider)}
	if s.FiveHour != nil {
		out.FiveHour = &windowJSON{UsedPercent: s.FiveHour.UsedPercent, ResetAt: s.FiveHour.ResetAt.UTC().Format(time.RFC3339)}
	}
	if s.Weekly != nil {
		out.Weekly = &windowJSON{UsedPercent: s.Weekly.UsedPercent, ResetAt: s.Weekly.ResetAt.UTC().Format(time.RFC3339)}
	}
	return out
}

func printSnapshot(s domain.UsageSnapshot) {
	b, _ := json.MarshalIndent(toSnapshotJSON(s), "", "  ")
	fmt.Println(string(b))
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(data))
	return strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
}

func binaryVersion(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// --- detect ----------------------------------------------------------------

func runDetect(args []string) int {
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	fmt.Printf("host OS: %s\n", runtime.GOOS)
	fmt.Printf("WSL: %v\n", isWSL())

	ready := runtime.GOOS == "linux"
	needsFix := false

	if _, err := exec.LookPath("codex"); err != nil {
		fmt.Println("codex: not found")
		needsFix = true
	} else if v, err := binaryVersion(ctx, "codex", "--version"); err == nil {
		fmt.Printf("codex: found (%s)\n", v)
	} else {
		fmt.Println("codex: found, but --version failed")
		needsFix = true
	}

	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Println("claude: not found")
		needsFix = true
	} else if v, err := binaryVersion(ctx, "claude", "--version"); err == nil {
		fmt.Printf("claude: found (%s)\n", v)
	} else {
		fmt.Println("claude: found, but --version failed")
		needsFix = true
	}

	info, err := claude.DetectStatusLine()
	if err != nil {
		fmt.Printf("claude statusLine: detection error: %v\n", err)
		needsFix = true
	} else if info.Configured {
		fmt.Println("claude statusLine: configured")
	} else {
		fmt.Println("claude statusLine: not configured (required for passive Claude capture)")
		needsFix = true
	}

	switch {
	case !ready:
		fmt.Println("result: unsupported (only Linux/WSL is supported in this build)")
		return 2
	case needsFix:
		fmt.Println("result: supported, prerequisite/fix required")
		return 1
	default:
		fmt.Println("result: supported and ready")
		return 0
	}
}

// --- doctor ------------------------------------------------------------

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	ok := true

	fmt.Println("model requests consumed by this check: 0")

	r := codex.New()
	snap, err := r.Read(ctx)
	if err != nil {
		fmt.Printf("codex reader: FAIL (%v)\n", err)
		ok = false
	} else {
		fmt.Printf("codex reader: OK (five_hour=%s, weekly=%s)\n", windowStatus(snap.FiveHour), windowStatus(snap.Weekly))
	}

	info, err := claude.DetectStatusLine()
	if err != nil {
		fmt.Printf("claude statusLine: FAIL (%v)\n", err)
		ok = false
	} else if !info.Configured {
		fmt.Println("claude statusLine: NOT CONFIGURED (passive capture requires a configured statusLine command)")
		ok = false
	} else {
		fmt.Println("claude statusLine: configured")
	}

	if v, err := claude.Version(ctx); err != nil {
		fmt.Printf("claude binary: FAIL (%v)\n", err)
		ok = false
	} else {
		fmt.Printf("claude binary: OK (%s)\n", v)
	}

	fmt.Println("local runtime persistence: disabled (no usage/history/cache/log files written)")

	if ok {
		fmt.Println("result: OK")
		return 0
	}
	fmt.Println("result: one or more checks failed")
	return 1
}

func windowStatus(w *domain.UsageWindow) string {
	if w == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%% used", w.UsedPercent)
}

// --- show-payload --------------------------------------------------------

func runShowPayload(args []string) int {
	fs := flag.NewFlagSet("show-payload", flag.ContinueOnError)
	providerFlag := fs.String("provider", "all", "codex, claude, or all")
	claudeStdin := fs.Bool("claude-stdin", false, "read a real Claude Code statusLine JSON payload from stdin for the claude provider")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	wantCodex := *providerFlag == "all" || *providerFlag == "codex"
	wantClaude := *providerFlag == "all" || *providerFlag == "claude"

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	code := 0

	if wantCodex {
		r := codex.New()
		snap, err := r.Read(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codex: %v\n", err)
			code = 1
		} else {
			printSnapshot(snap)
		}
	}

	if wantClaude {
		if *claudeStdin {
			// statusLine payloads are a few KB; cap well above that so a
			// misdirected pipe can't exhaust memory.
			raw, err := io.ReadAll(io.LimitReader(bufio.NewReader(os.Stdin), 1<<20))
			if err != nil {
				fmt.Fprintf(os.Stderr, "claude: failed to read stdin: %v\n", err)
				code = 1
			} else {
				snap, err := claude.ParsePayload(raw)
				if err != nil {
					fmt.Fprintf(os.Stderr, "claude: %v\n", err)
					code = 1
				} else {
					printSnapshot(snap)
				}
			}
		} else {
			fmt.Println(`{"provider":"claude","note":"no live snapshot: Claude Code only hands rate-limit data to a configured statusLine command on its own refresh cadence; pipe a captured payload with --claude-stdin, or wait for the P2 monitoring agent"}`)
		}
	}

	return code
}

// --- status ----------------------------------------------------------------

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	fmt.Println("Agent: not implemented (P2)")
	fmt.Println("Linked: not implemented (P3/P4)")

	r := codex.New()
	if snap, err := r.Read(ctx); err != nil {
		fmt.Printf("Codex: unavailable (%v)\n", err)
	} else {
		fmt.Printf("Codex: supported / active (five_hour=%s, weekly=%s)\n", windowStatus(snap.FiveHour), windowStatus(snap.Weekly))
	}

	if info, err := claude.DetectStatusLine(); err != nil {
		fmt.Printf("Claude Code: unavailable (%v)\n", err)
	} else if info.Configured {
		fmt.Println("Claude Code: supported / passive-capture-only (statusLine configured, no live pull yet)")
	} else {
		fmt.Println("Claude Code: supported / statusLine not configured")
	}

	fmt.Println("Local runtime persistence: disabled")
	fmt.Println("Server: not implemented (P3)")

	return 0
}
