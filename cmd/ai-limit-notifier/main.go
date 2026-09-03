// Command ai-limit-notifier is the local diagnostics CLI defined by
// docs/INSTALLER_CONTRACT.md. This build implements the read-only P1
// surface (detect, doctor, show-payload, status); install/link/uninstall
// are not yet implemented.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/agent"
	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/claude"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
	"github.com/allfalldownquick/ai-limit-notifier/internal/sink"
	"github.com/allfalldownquick/ai-limit-notifier/internal/wrapper"
)

const readTimeout = 10 * time.Second

// clientVersion is reported to the server during pairing (docs/PROTOCOL_V1.md's
// client_version field) — informational/diagnostic only, never trusted for
// anything security-sensitive.
const clientVersion = "0.1.0-dev"

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
	case "monitor":
		code = runMonitor(os.Args[2:])
	case "statusline-wrapper":
		code = runStatuslineWrapper(os.Args[2:])
	case "install":
		code = runInstall(os.Args[2:])
	case "link":
		code = runLink(os.Args[2:])
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
  detect              Read-only environment/provider detection.
  doctor              Read-only diagnostics for configured provider readers.
  show-payload        Show the exact normalized data a future server call would send.
  status              Concise current local state.
  monitor             Run the RAM-only monitoring agent (Codex polling + Claude socket).
  link CODE           Redeem a Telegram-issued pairing code and save the server link locally.
  statusline-wrapper  Claude Code statusLine chaining wrapper (installed, not run by hand).
  install --plan      Show the Claude statusLine wrapper installation plan (read-only).`)
}

// --- shared helpers -------------------------------------------------------

// resolveServerURL applies the documented config precedence: an explicit
// CLI flag wins, then the environment, then whatever `link` last saved.
func resolveServerURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("AI_LIMIT_NOTIFIER_SERVER_URL"); v != "" {
		return v
	}
	if cfg, err := localconfig.Load(); err == nil {
		return cfg.ServerURL
	}
	return ""
}

// resolveDeviceToken never consults a CLI flag — a bearer token must not
// appear in `ps`/shell history the way a flag value would. Environment
// wins over the saved config so an operator can override it without
// editing (or without there being) a config file.
func resolveDeviceToken() string {
	if v := os.Getenv("AI_LIMIT_NOTIFIER_DEVICE_TOKEN"); v != "" {
		return v
	}
	if cfg, err := localconfig.Load(); err == nil {
		return cfg.DeviceToken
	}
	return ""
}

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
	} else if strings.Contains(info.Command, "statusline-wrapper") {
		fmt.Println("claude statusLine: configured, wrapper installed")
		if !strings.Contains(info.Command, "--original-command") {
			fmt.Println("claude statusLine: DRIFT (wrapper installed without --original-command; original statusLine output would be lost)")
			ok = false
		}
	} else {
		fmt.Println("claude statusLine: configured, wrapper NOT installed (passive capture inactive — see `install --plan`)")
	}

	if conn, err := net.DialTimeout("unix", claudesock.Path(), 200*time.Millisecond); err == nil {
		_ = conn.Close()
		fmt.Println("agent socket: reachable")
	} else {
		fmt.Println("agent socket: not reachable (monitor is not running)")
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

	if conn, err := net.DialTimeout("unix", claudesock.Path(), 200*time.Millisecond); err == nil {
		_ = conn.Close()
		fmt.Println("Agent: running (Claude socket reachable)")
	} else {
		fmt.Println("Agent: not running")
	}
	fmt.Println("Linked: not implemented (P3/P4)")

	r := codex.New()
	if snap, err := r.Read(ctx); err != nil {
		fmt.Printf("Codex: unavailable (%v)\n", err)
	} else {
		fmt.Printf("Codex: supported / active (five_hour=%s, weekly=%s)\n", windowStatus(snap.FiveHour), windowStatus(snap.Weekly))
	}

	if info, err := claude.DetectStatusLine(); err != nil {
		fmt.Printf("Claude Code: unavailable (%v)\n", err)
	} else if !info.Configured {
		fmt.Println("Claude Code: supported / statusLine not configured")
	} else if strings.Contains(info.Command, "statusline-wrapper") {
		fmt.Println("Claude Code: supported / wrapper installed (passive capture wired to agent socket)")
	} else {
		fmt.Println("Claude Code: supported / passive-capture-only (statusLine configured, wrapper not installed — run `install --plan`)")
	}

	fmt.Println("Local runtime persistence: disabled")
	fmt.Println("Server: not implemented (P3)")

	return 0
}

// --- monitor ---------------------------------------------------------------

// printSink is the only Sink that exists before P3: it prints what would be
// (or, once a real server exists, will be) delivered. It never writes to
// disk, so both modes satisfy "no persistent runtime state" — --dry-run
// only changes the label, since there is nothing else to send to yet.
type printSink struct {
	w      io.Writer
	dryRun bool
}

func (s *printSink) Send(_ context.Context, ev agent.Event) error {
	label := "would send"
	if !s.dryRun {
		label = "observed (no server configured yet — P3)"
	}
	fmt.Fprintf(s.w, "[%s] %s %s: %.0f%% used, resets %s\n",
		label, ev.Provider, ev.Window, ev.UsedPercent, ev.ResetAt.UTC().Format(time.RFC3339))
	return nil
}

func runMonitor(args []string) int {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	codexInterval := fs.Duration("codex-interval", 5*time.Minute, "bounded Codex polling interval (clamped to a minimum)")
	threshold := fs.Float64("threshold", domain.DefaultScheduleThreshold, "used_percent threshold that schedules a reset event")
	dryRun := fs.Bool("dry-run", true, "show would-be events only; never send to a configured server")
	serverURL := fs.String("server-url", "", "hosted/self-hosted server base URL (e.g. https://api.example.com); requires AI_LIMIT_NOTIFIER_DEVICE_TOKEN")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Precedence for the server URL: explicit flag > environment > saved
	// `link` config > (dry-run/no server). The device token never comes
	// from a flag (it would appear in `ps`/shell history) — only
	// environment or the config `link` wrote, environment taking
	// precedence so an operator can override the config without editing it.
	resolvedServerURL := resolveServerURL(*serverURL)

	var core *agent.Core
	switch {
	case *dryRun || resolvedServerURL == "":
		if resolvedServerURL != "" {
			fmt.Println("monitor: --dry-run set; ignoring the configured server and printing would-be events instead")
		}
		core = agent.NewCore(&printSink{w: os.Stdout, dryRun: true}, *threshold)
		fmt.Printf("monitor: threshold=%.0f%% codex-interval=%s dry-run=true\n", *threshold, *codexInterval)
	default:
		token := resolveDeviceToken()
		if token == "" {
			fmt.Fprintln(os.Stderr, "monitor: a server is configured but no device token was found (AI_LIMIT_NOTIFIER_DEVICE_TOKEN, or run `ai-limit-notifier link CODE` first)")
			return 3
		}
		httpSink, err := sink.New(resolvedServerURL, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "monitor: %v\n", err)
			return 3
		}
		core = agent.NewCore(httpSink, *threshold)
		fmt.Printf("monitor: threshold=%.0f%% codex-interval=%s server=%s\n", *threshold, *codexInterval, resolvedServerURL)
	}

	fmt.Println("monitor: local runtime persistence disabled; all state is in RAM")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		agent.PollCodex(ctx, core, codex.New(), *codexInterval)
	}()

	go func() {
		defer wg.Done()
		if err := agent.ServeClaudeSocket(ctx, core); err != nil {
			fmt.Fprintf(os.Stderr, "monitor: claude socket unavailable: %v\n", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("monitor: shutting down")
	wg.Wait()
	return 0
}

// --- link ------------------------------------------------------------------

func runLink(args []string) int {
	// The pairing code is always the first argument (matching the exact
	// usage the bot itself sends: "ai-limit-notifier link CODE"), sliced
	// off before flag.Parse — Go's flag package stops parsing at the first
	// non-flag token, so passing the whole args slice through would leave
	// any flag written *after* the code unparsed.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: ai-limit-notifier link <PAIRING_CODE> [--server-url URL]")
		return 3
	}
	code := args[0]

	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	serverURLFlag := fs.String("server-url", "", "hosted/self-hosted server base URL (e.g. https://api.example.com)")
	if err := fs.Parse(args[1:]); err != nil {
		return 3
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: ai-limit-notifier link <PAIRING_CODE> [--server-url URL]")
		return 3
	}

	serverURL := resolveServerURL(*serverURLFlag)
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "link: no server URL given (--server-url, AI_LIMIT_NOTIFIER_SERVER_URL, or an existing config)")
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	deviceID, token, err := pairWithServer(ctx, serverURL, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "link: %v\n", err)
		return 1
	}

	cfg := &localconfig.Config{
		SchemaVersion: localconfig.SchemaVersion,
		ServerURL:     serverURL,
		DeviceID:      deviceID,
		DeviceToken:   token,
	}
	if err := localconfig.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "link: failed to save local config: %v\n", err)
		return 1
	}

	path, _ := localconfig.Path()
	fmt.Printf("link: linked (device_id=%s)\n", deviceID)
	fmt.Printf("link: config saved to %s\n", path)
	fmt.Println("link: run `ai-limit-notifier monitor` to start reporting usage")
	return 0
}

type pairResponseWire struct {
	Linked      bool   `json:"linked"`
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
}

// pairWithServer implements the client side of POST /api/v1/pair with the
// same strict principles as internal/sink's HTTPSink: HTTPS required
// unless the host is loopback, no redirects followed, a bounded timeout,
// a bounded response read, and the pairing code/response token are never
// logged anywhere in this function.
func pairWithServer(ctx context.Context, serverURL, code string) (deviceID, token string, err error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "https" && !isLoopbackHostname(u.Hostname()) {
		return "", "", errors.New("server URL must use https:// (loopback http:// is allowed for local testing only)")
	}

	platform := runtime.GOOS
	if isWSL() {
		platform += "-wsl"
	}
	platform += "-" + runtime.GOARCH

	body, err := json.Marshal(map[string]string{
		"code":           code,
		"client_version": clientVersion,
		"platform":       platform,
	})
	if err != nil {
		return "", "", err
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		// Never follow a redirect: a pairing code is a one-use secret, and
		// this endpoint is unauthenticated, so refusing to chase a 3xx
		// anywhere else is the simplest correct policy — see internal/sink.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serverURL, "/")+"/api/v1/pair", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("pairing request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != http.StatusOK {
		// The server's error body is small and sanitized (see
		// internal/server/api's errorResponse); safe to surface as-is,
		// and it never contains a code or token by construction.
		return "", "", fmt.Errorf("server rejected pairing: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed pairResponseWire
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", fmt.Errorf("malformed server response: %w", err)
	}
	if !parsed.Linked || parsed.DeviceID == "" || parsed.DeviceToken == "" {
		return "", "", errors.New("server did not confirm linking")
	}
	return parsed.DeviceID, parsed.DeviceToken, nil
}

func isLoopbackHostname(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// --- statusline-wrapper ------------------------------------------------

func runStatuslineWrapper(args []string) int {
	fs := flag.NewFlagSet("statusline-wrapper", flag.ContinueOnError)
	original := fs.String("original-command", "", "the user's original statusLine command to chain to")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	// If Claude Code kills a slow statusLine invocation, propagate that so
	// the chained original command is cancelled too rather than orphaned.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return wrapper.Run(ctx, os.Stdin, os.Stdout, os.Stderr, *original)
}

// --- install --plan ------------------------------------------------------

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	plan := fs.Bool("plan", false, "show the installation plan without applying it")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	if !*plan {
		fmt.Fprintln(os.Stderr, "only `install --plan` is implemented; applying changes requires a separate explicit step that does not exist yet")
		return 3
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "ai-limit-notifier"
	}

	info, err := claude.DetectStatusLine()
	if err != nil {
		fmt.Printf("Claude statusLine detection failed: %v\n", err)
		return 1
	}

	fmt.Println("Plan: Claude Code statusLine chaining wrapper")
	fmt.Println()
	if !info.Configured {
		fmt.Println("Current ~/.claude/settings.json statusLine.command: (none configured)")
		fmt.Println("Nothing to chain to yet: install a statusLine command first, or this")
		fmt.Println("plan would need to install a bare (non-chaining) wrapper, which is not")
		fmt.Println("recommended since there would be no original output to preserve.")
		return 0
	}
	if strings.Contains(info.Command, "statusline-wrapper") {
		fmt.Println("Current ~/.claude/settings.json statusLine.command already runs the wrapper:")
		fmt.Printf("  %s\n", info.Command)
		fmt.Println("No change planned.")
		return 0
	}

	proposed := fmt.Sprintf("%s statusline-wrapper --original-command %s", exe, shellQuote(info.Command))

	fmt.Println("This does NOT apply anything. It only shows the one persistent change")
	fmt.Println("that installing Claude passive capture would make.")
	fmt.Println()
	fmt.Println("File: ~/.claude/settings.json")
	fmt.Println("Field: statusLine.command")
	fmt.Println()
	fmt.Printf("Current:  %s\n", info.Command)
	fmt.Printf("Proposed: %s\n", proposed)
	fmt.Println()
	fmt.Println("The wrapper always re-runs the exact current command above with the same")
	fmt.Println("stdin and copies its stdout/stderr/exit code straight through, so the")
	fmt.Println("visible statusLine is unaffected. It additionally sends only the four")
	fmt.Println("normalized rate-limit fields to a local RAM-backed Unix socket for the")
	fmt.Println("`monitor` agent to consume; if that fails for any reason, the original")
	fmt.Println("statusLine output is unaffected.")
	fmt.Println()
	fmt.Println("This build does not apply this change automatically. Ask the operator to")
	fmt.Println("edit ~/.claude/settings.json by hand (or wait for a real `install`).")
	return 0
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
