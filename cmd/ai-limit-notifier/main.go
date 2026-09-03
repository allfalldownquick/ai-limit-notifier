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
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/agent"
	"github.com/allfalldownquick/ai-limit-notifier/internal/claudesock"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
	"github.com/allfalldownquick/ai-limit-notifier/internal/installer"
	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/claude"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
	"github.com/allfalldownquick/ai-limit-notifier/internal/sink"
	"github.com/allfalldownquick/ai-limit-notifier/internal/wrapper"
)

const readTimeout = 10 * time.Second

// unsetThreshold is monitor's --threshold flag default. NaN rather than a
// sentinel like -1: a real number (even an invalid one, like -1 or 0) is
// something a user might actually type and expect a clear rejection for,
// and -1 is exactly the kind of "obviously invalid" value someone would
// try — colliding with it would make --threshold -1 silently behave as
// "not given" instead of a validation error. NaN can't collide with a
// float64 flag value in practice.
var unsetThreshold = math.NaN()

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
	case "uninstall":
		code = runUninstall(os.Args[2:])
	case "link":
		code = runLink(os.Args[2:])
	case "config":
		code = runConfig(os.Args[2:])
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
  config              Show the local notification threshold.
  config threshold N  Set the local notification threshold (0 < N <= 100; default 80).
  statusline-wrapper  Claude Code statusLine chaining wrapper (installed, not run by hand).
  install --plan      Show the full install plan (binary/statusLine/autostart), read-only.
  install             Install: durable binary, systemd --user autostart, Claude statusLine.
  uninstall --plan    Show the uninstall plan, read-only.
  uninstall           Undo install (never touches link/device config).`)
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
	} else {
		cur := ""
		if info.Configured {
			cur = info.Command
		}
		classified := installer.ClassifyStatusLine(cur)
		switch classified.State {
		case installer.StatusLineAbsent:
			fmt.Println("claude statusLine: NOT CONFIGURED (passive capture requires a configured statusLine command)")
			ok = false
		case installer.StatusLineNotifierWithOriginal:
			fmt.Println("claude statusLine: configured, wrapper installed (chains to the original statusLine)")
		case installer.StatusLineNotifierWithoutOriginal:
			fmt.Println("claude statusLine: configured, wrapper installed (capture-only, no pre-existing statusLine)")
		case installer.StatusLineMalformed:
			fmt.Println("claude statusLine: DRIFT (mentions statusline-wrapper but doesn't match a recognized shape)")
			ok = false
		default: // StatusLineExistingNonNotifier
			fmt.Println("claude statusLine: configured, wrapper NOT installed (passive capture inactive — see `install --plan`)")
		}
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

	cfg, cfgErr := localconfig.Load()
	switch {
	case cfgErr != nil:
		fmt.Println("link config: FAIL (local config unreadable/malformed)")
		ok = false
	case cfg.DeviceToken == "" || cfg.ServerURL == "":
		fmt.Println("link config: not linked (run `ai-limit-notifier link <CODE>`)")
	default:
		fmt.Println("link config: linked")
		client := doctorHTTPClient()

		if health, err := fetchHealth(ctx, client, cfg.ServerURL); err != nil {
			fmt.Printf("server reachability: FAIL (%v)\n", err)
			fmt.Println("protocol compatibility: unknown (server unreachable)")
			ok = false
		} else {
			fmt.Println("server reachability: OK")
			if health.ProtocolVersion == doctorProtocolVersion {
				fmt.Printf("protocol compatibility: OK (server=%d, client=%d)\n", health.ProtocolVersion, doctorProtocolVersion)
			} else {
				fmt.Printf("protocol compatibility: MISMATCH (server=%d, client=%d)\n", health.ProtocolVersion, doctorProtocolVersion)
				ok = false
			}
		}

		switch valid, err := fetchDeviceValid(ctx, client, cfg.ServerURL, cfg.DeviceToken); {
		case err != nil:
			fmt.Printf("device credential: FAIL (%v)\n", err)
			ok = false
		case !valid:
			fmt.Println("device credential: REVOKED or INVALID (server rejected it — run `ai-limit-notifier link <CODE>` again)")
			ok = false
		default:
			fmt.Println("device credential: valid")
		}
	}

	if m, err := installer.LoadManifest(); err != nil {
		fmt.Println("install: FAIL (manifest unreadable/malformed)")
		ok = false
	} else if m == nil {
		fmt.Println("install: not installed (run `ai-limit-notifier install --plan`)")
	} else {
		if _, statErr := os.Stat(m.InstalledBinaryPath); statErr == nil {
			fmt.Printf("install: binary present (%s)\n", m.InstalledBinaryPath)
		} else {
			fmt.Printf("install: binary MISSING (%s) — the shell-level fail-open keeps the original statusLine working; run `ai-limit-notifier install` to restore it\n", m.InstalledBinaryPath)
			ok = false
		}
		if installer.SystemdUserAvailable(ctx) {
			fmt.Printf("autostart: enabled=%v active=%v\n", installer.UnitIsEnabled(ctx), installer.UnitIsActive(ctx))
		} else {
			fmt.Println("autostart: systemd --user not available in this environment")
		}
	}

	if ok {
		fmt.Println("result: OK")
		return 0
	}
	fmt.Println("result: one or more checks failed")
	return 1
}

// doctorProtocolVersion mirrors docs/PROTOCOL_V1.md's schema_version, the
// same number internal/sink and internal/server/api independently define
// locally rather than share via an import (client and server are
// deliberately separate products — see docs/INSTALLER_CONTRACT.md).
const doctorProtocolVersion = 1

func doctorHTTPClient() *http.Client {
	return &http.Client{
		Timeout: readTimeout,
		// Same redirect policy as internal/sink and pairWithServer: a
		// bearer token must never be forwarded to a redirect target this
		// diagnostic didn't choose.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type doctorHealthWire struct {
	ProtocolVersion int `json:"protocol_version"`
}

// fetchHealth calls the server's unauthenticated /healthz -- reachability
// and protocol compatibility only, no credential involved.
func fetchHealth(ctx context.Context, client *http.Client, serverURL string) (doctorHealthWire, error) {
	var out doctorHealthWire
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(serverURL, "/")+"/healthz", nil)
	if err != nil {
		return out, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if err != nil {
		return out, err
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("malformed response: %w", err)
	}
	return out, nil
}

// fetchDeviceValid checks the device credential against the server's
// authenticated /api/v1/status. A (false, nil) return means the server
// affirmatively rejected the credential (401 -- revoked or invalid),
// distinct from a transport/protocol failure, which comes back as an error.
func fetchDeviceValid(ctx context.Context, client *http.Client, serverURL, token string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(serverURL, "/")+"/api/v1/status", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8*1024))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized:
		return false, nil
	default:
		return false, fmt.Errorf("server returned %d", resp.StatusCode)
	}
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
	printInstallStatus(ctx)
	printLinkStatus()
	printThreshold()

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

	return 0
}

// printInstallStatus reports installed/autostart state, read-only, no
// tokens: whether `install` has run, and (if systemd --user is available)
// whether the autostart unit is currently enabled/active.
func printInstallStatus(ctx context.Context) {
	m, err := installer.LoadManifest()
	if err != nil {
		fmt.Println("Installed: unknown (install manifest unreadable — run `ai-limit-notifier doctor` for details)")
		return
	}
	if m == nil {
		fmt.Println("Installed: no")
		return
	}
	fmt.Println("Installed: yes")
	if !installer.SystemdUserAvailable(ctx) {
		fmt.Println("Autostart: unavailable (systemd --user not present in this environment)")
		return
	}
	state := "disabled"
	if installer.UnitIsEnabled(ctx) {
		state = "enabled"
	}
	if installer.UnitIsActive(ctx) {
		state += " / running"
	} else {
		state += " / not running"
	}
	fmt.Printf("Autostart: %s\n", state)
}

// printLinkStatus reports link state from the saved config only -- what
// `ai-limit-notifier link` last wrote, read-only -- and never the device
// token's value, only whether one is present.
func printLinkStatus() {
	cfg, err := localconfig.Load()
	switch {
	case err != nil:
		fmt.Println("Linked: unknown (local config unreadable — run `ai-limit-notifier doctor` for details)")
	case cfg.DeviceToken == "" || cfg.ServerURL == "":
		fmt.Println("Linked: no")
	default:
		fmt.Println("Linked: yes")
		fmt.Println("Device: configured")
		fmt.Printf("Server: %s\n", cfg.ServerURL)
	}
}

// --- monitor ---------------------------------------------------------------

// printSink is the --dry-run destination: it only ever prints what would be
// sent, never writes to disk, and never contacts a server.
type printSink struct {
	w io.Writer
}

func (s *printSink) Send(_ context.Context, ev agent.Event) error {
	fmt.Fprintf(s.w, "[would send] %s %s: %.0f%% used, resets %s\n",
		ev.Provider, ev.Window, ev.UsedPercent, ev.ResetAt.UTC().Format(time.RFC3339))
	return nil
}

func runMonitor(args []string) int {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	codexInterval := fs.Duration("codex-interval", 5*time.Minute, "bounded Codex polling interval (clamped to a minimum)")
	threshold := fs.Float64("threshold", unsetThreshold, "override the local notification threshold for this run only (not saved); default: use `ai-limit-notifier config`'s saved value")
	dryRun := fs.Bool("dry-run", false, "never contact a server; print would-be events only")
	serverURL := fs.String("server-url", "", "hosted/self-hosted server base URL (e.g. https://api.example.com); requires AI_LIMIT_NOTIFIER_DEVICE_TOKEN")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	// SIGHUP matters here specifically because it isn't just "another
	// signal to add for completeness": a closed terminal/dropped WSL
	// session delivers it (not SIGINT) to the foreground process group,
	// and SIGHUP's default disposition is immediate termination with no
	// deferred cleanup at all — reproduced directly against this exact
	// binary: a real SIGINT always let ServeClaudeSocket's cleanup remove
	// the socket file, but a real SIGHUP killed the process before any
	// defer ran, leaving a stale (harmless, but not what the doc comment
	// on ServeClaudeSocket promises) socket file behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// Precedence for the server URL: explicit flag > environment > saved
	// `link` config. The device token never comes from a flag (it would
	// appear in `ps`/shell history) — only environment or the config
	// `link` wrote, environment taking precedence so an operator can
	// override the config without editing it.
	//
	// Semantics are deliberately unambiguous, no silent fallback between
	// them: --dry-run never sends, regardless of what's configured; a
	// configured server without --dry-run always sends; no configured
	// server without --dry-run is a fail-closed error, not a quiet
	// downgrade to dry-run (a plain `monitor` after `link` must actually
	// report, or plainly refuse to run).
	resolvedServerURL := resolveServerURL(*serverURL)

	if !math.IsNaN(*threshold) && (*threshold <= 0 || *threshold > 100) {
		// domain.ShouldScheduleReset silently treats threshold<=0 (and
		// >100) as "never schedule" rather than "always" — an explicit
		// --threshold 0 would otherwise look like it's running but never
		// send anything, with no error anywhere. Reject it here instead.
		fmt.Fprintln(os.Stderr, "monitor: --threshold must be a number > 0 and <= 100")
		return 3
	}

	monitorSink, statusLine, err := resolveMonitorSink(*dryRun, resolvedServerURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "monitor: %v\n", err)
		return 3
	}
	effectiveThreshold := resolveMonitorThreshold(*threshold)
	core := agent.NewCore(monitorSink, effectiveThreshold)
	fmt.Printf("monitor: threshold=%.0f%% codex-interval=%s %s\n", effectiveThreshold, *codexInterval, statusLine)

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

// resolveMonitorSink is monitor's fail-closed sink selection, split out
// from runMonitor so it can be tested without spinning up the blocking
// poll/serve loop. dryRun never sends, regardless of resolvedServerURL; an
// empty resolvedServerURL without dryRun is a fail-closed error rather than
// a silent downgrade to dry-run; a configured server without dryRun always
// sends (missing device token is an error, not a fallback either).
func resolveMonitorSink(dryRun bool, resolvedServerURL string) (agent.Sink, string, error) {
	if dryRun {
		return &printSink{w: os.Stdout}, "dry-run=true", nil
	}
	if resolvedServerURL == "" {
		return nil, "", errors.New("not linked; run `ai-limit-notifier link <CODE>` or use --dry-run")
	}
	token := resolveDeviceToken()
	if token == "" {
		return nil, "", errors.New("a server is configured but no device token was found (AI_LIMIT_NOTIFIER_DEVICE_TOKEN, or run `ai-limit-notifier link CODE` first)")
	}
	httpSink, err := sink.New(resolvedServerURL, token)
	if err != nil {
		return nil, "", err
	}
	return httpSink, "server=" + resolvedServerURL, nil
}

// resolveMonitorThreshold implements --threshold's override semantics: an
// explicit flag value (anything but the unsetThreshold sentinel) wins for
// this run only and is never saved; otherwise the saved local config's
// threshold applies (falling back to the default on a missing/malformed
// config, same as every other config-reading helper in this file).
func resolveMonitorThreshold(flagValue float64) float64 {
	if !math.IsNaN(flagValue) {
		return flagValue
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return localconfig.DefaultNotificationThreshold
	}
	return cfg.Threshold()
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

	// A relink must not silently reset a previously configured
	// notification threshold: carry it forward if a config already exists
	// (a fresh Load on any error just yields the zero value, which
	// Threshold() already treats as "unset" — same as a brand-new device).
	var priorThreshold float64
	if existing, err := localconfig.Load(); err == nil {
		priorThreshold = existing.NotificationThreshold
	}

	cfg := &localconfig.Config{
		SchemaVersion:         localconfig.SchemaVersion,
		ServerURL:             serverURL,
		DeviceID:              deviceID,
		DeviceToken:           token,
		NotificationThreshold: priorThreshold,
	}
	if err := localconfig.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "link: failed to save local config: %v\n", err)
		return 1
	}

	path, _ := localconfig.Path()
	fmt.Printf("link: linked (device_id=%s)\n", deviceID)
	fmt.Printf("link: config saved to %s\n", path)

	activateInstalledService()
	return 0
}

// activateInstalledService is Correction 4's second half: once a valid
// credential is durably saved (already done by the time this runs), start
// or restart the P5-installed autostart service so it picks up the new
// credential -- but only if `install` actually set one up. The credential
// save above already succeeded and returned 0 regardless of anything
// here: a systemd problem must never make `link` itself look like it
// failed, only print a diagnostic and fall back to the manual-monitor
// instruction.
func activateInstalledService() {
	manifest, err := installer.LoadManifest()
	if err != nil || manifest == nil {
		fmt.Println("link: run `ai-limit-notifier monitor` to start reporting usage")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !installer.SystemdUserAvailable(ctx) {
		fmt.Println("link: installed, but systemd --user is not available here -- run `ai-limit-notifier monitor` manually")
		return
	}

	if installer.UnitIsActive(ctx) {
		if err := installer.RestartUnit(ctx); err != nil {
			fmt.Printf("link: installed service restart failed (%v) -- run `ai-limit-notifier monitor` manually\n", err)
			return
		}
		fmt.Println("link: restarted the installed monitor service with the new credential")
		return
	}

	if err := installer.StartUnit(ctx); err != nil {
		fmt.Printf("link: installed service start failed (%v) -- run `ai-limit-notifier monitor` manually\n", err)
		return
	}
	fmt.Println("link: started the installed monitor service")
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

// --- config ------------------------------------------------------------

// runConfig is deliberately narrow: today the only setting is the local
// notification threshold. `config` alone shows it (safe: never touches the
// device token); `config threshold N` sets it. Does not require an existing
// link — a threshold-only config is valid, and `link` never overwrites it
// (see runLink, which preserves it across a relink).
func runConfig(args []string) int {
	if len(args) == 0 {
		printThreshold()
		return 0
	}
	if args[0] != "threshold" {
		fmt.Fprintln(os.Stderr, "usage: ai-limit-notifier config [threshold N]")
		return 3
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ai-limit-notifier config threshold N")
		return 3
	}
	n, err := strconv.ParseFloat(args[1], 64)
	if err != nil || n <= 0 || n > 100 {
		fmt.Fprintln(os.Stderr, "config: threshold must be a number > 0 and <= 100")
		return 3
	}

	cfg, err := localconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: local config unreadable/malformed: %v\n", err)
		return 1
	}
	cfg.NotificationThreshold = n
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = localconfig.SchemaVersion
	}
	if err := localconfig.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config: failed to save local config: %v\n", err)
		return 1
	}
	fmt.Printf("config: notification threshold set to %.0f%%\n", n)
	return 0
}

func printThreshold() {
	cfg, err := localconfig.Load()
	if err != nil {
		fmt.Println("Notification threshold: unknown (local config unreadable — run `ai-limit-notifier doctor` for details)")
		return
	}
	fmt.Printf("Notification threshold: %.0f%%\n", cfg.Threshold())
}

// --- statusline-wrapper ------------------------------------------------

func runStatuslineWrapper(args []string) int {
	fs := flag.NewFlagSet("statusline-wrapper", flag.ContinueOnError)
	original := fs.String("original-command", "", "the user's original statusLine command to chain to")
	captureOnly := fs.Bool("capture-only", false, "no original statusLine to chain to (install Case B); capture only, never an error")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	// If Claude Code kills a slow statusLine invocation, propagate that so
	// the chained original command is cancelled too rather than orphaned.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return wrapper.Run(ctx, os.Stdin, os.Stdout, os.Stderr, *original, *captureOnly)
}

// --- install / uninstall --------------------------------------------------

func currentStatusLineCommand() (string, error) {
	info, err := claude.DetectStatusLine()
	if err != nil {
		return "", err
	}
	if !info.Configured {
		return "", nil
	}
	return info.Command, nil
}

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	planOnly := fs.Bool("plan", false, "show the install plan without applying it (read-only, zero writes)")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	current, err := currentStatusLineCommand()
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: reading Claude statusLine: %v\n", err)
		return 1
	}

	p, err := installer.ComputeInstallPlan(ctx, current)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		return 1
	}
	printInstallPlan(p)

	if *planOnly {
		return 0
	}
	if p.Blocked {
		return 1
	}
	if p.AlreadyInstalled {
		fmt.Println("install: already installed and up to date, nothing to do")
		return 0
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: resolve current executable: %v\n", err)
		return 1
	}
	if err := installer.ApplyInstall(ctx, p, exe); err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		return 1
	}
	// Last step, only once binary+unit+manifest are all fully in place:
	// flip the live statusLine to the notifier-managed command. If this
	// fails or the process is killed before it runs, the user's original
	// statusLine is completely untouched and a repeated `install` safely
	// recovers (see ComputeInstallPlan's idempotency handling).
	if p.StatusLineProposedCommand != "" {
		if err := claude.SetStatusLineCommand(p.StatusLineProposedCommand); err != nil {
			fmt.Fprintf(os.Stderr, "install: binary/service installed, but failed to update Claude statusLine: %v\n", err)
			return 1
		}
	}

	fmt.Println("install: complete")
	if !p.Linked {
		fmt.Println("install: not linked yet -- run `ai-limit-notifier link <CODE>` to start monitoring")
	}
	return 0
}

func runUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	planOnly := fs.Bool("plan", false, "show the uninstall plan without applying it (read-only, zero writes)")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	current, err := currentStatusLineCommand()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: reading Claude statusLine: %v\n", err)
		return 1
	}

	p, err := installer.ComputeUninstallPlan(ctx, current)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		return 1
	}
	printUninstallPlan(p)

	if *planOnly || p.NotInstalled {
		return 0
	}
	if p.Blocked {
		return 1
	}

	// Restore/remove the statusLine first: the live config should never
	// keep pointing at a binary that's about to be removed.
	if p.StatusLineWillBeRemoved {
		if err := claude.RemoveStatusLineCommand(); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
			return 1
		}
	} else if p.StatusLineProposedCommand != "" {
		if err := claude.SetStatusLineCommand(p.StatusLineProposedCommand); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
			return 1
		}
	}

	if err := installer.ApplyUninstall(ctx, p); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		return 1
	}

	fmt.Println("uninstall: complete (link/device credential and notification threshold were not touched)")
	return 0
}

func printInstallPlan(p *installer.Plan) {
	fmt.Println("Plan: ai-limit-notifier install")
	fmt.Println()
	if p.Blocked {
		fmt.Printf("BLOCKED: %s\n", p.BlockedReason)
		return
	}
	if p.AlreadyInstalled {
		fmt.Println("Already installed and up to date. Nothing to do.")
		fmt.Println()
	}
	fmt.Printf("Binary path: %s (currently exists: %v)\n", p.BinaryPath, p.BinaryCurrentlyExists)
	fmt.Println()
	fmt.Println("Claude statusLine:")
	fmt.Printf("  Current state: %s\n", p.StatusLineCurrentState)
	if p.StatusLineOriginalExisted {
		fmt.Printf("  Existing statusLine will be preserved and chained to: %s\n", p.StatusLineOriginalCommand)
	} else {
		fmt.Println("  No pre-existing statusLine: a transport-only capture command will be")
		fmt.Println("  installed (Case B) -- no new visible status bar is added.")
	}
	fmt.Printf("  Proposed statusLine.command: %s\n", p.StatusLineProposedCommand)
	fmt.Println()
	fmt.Println("Codex:")
	if p.CodexResolved {
		fmt.Printf("  Resolved: %s (added to the service's PATH)\n", p.CodexBinDir)
	} else {
		fmt.Println("  WARNING: codex was not found in this shell's PATH. Autostart Codex")
		fmt.Println("  polling will not work until this is fixed; Claude tracking is unaffected.")
	}
	fmt.Println()
	fmt.Println("systemd --user:")
	fmt.Printf("  Available: %v\n", p.SystemdAvailable)
	if p.SystemdAvailable {
		fmt.Printf("  Unit path: %s\n", p.UnitPath)
		fmt.Println("  --- unit content ---")
		fmt.Print(p.UnitContent)
		fmt.Println("  --- end unit content ---")
		fmt.Printf("  Will enable: %v\n", p.WillEnableUnit)
		fmt.Printf("  Will start: %v (%s)\n", p.WillStartUnit, p.StartReason)
	} else {
		fmt.Println("  Not available in this environment -- autostart will not be configured.")
	}
	fmt.Printf("  Current linger state for this user: %v (install never changes this)\n", p.LingerCurrentlyEnabled)
	fmt.Println()
	fmt.Printf("Linked: %v\n", p.Linked)
}

func printUninstallPlan(p *installer.Plan) {
	fmt.Println("Plan: ai-limit-notifier uninstall")
	fmt.Println()
	if p.NotInstalled {
		fmt.Println("Not installed. Nothing to do.")
		return
	}
	if p.Blocked {
		fmt.Printf("BLOCKED: %s\n", p.BlockedReason)
		return
	}
	fmt.Printf("Will stop/disable the systemd --user unit: %v\n", p.WillDisableUnit)
	fmt.Printf("Will remove unit file: %s\n", p.UnitPath)
	fmt.Printf("Will remove installed binary: %s\n", p.BinaryPath)
	fmt.Println()
	if p.StatusLineWillBeRemoved {
		fmt.Println("statusLine had no pre-existing command: it will be removed entirely (back to absent).")
	} else {
		fmt.Printf("statusLine will be restored to: %s\n", p.StatusLineProposedCommand)
	}
	fmt.Println()
	fmt.Println("Link/device credential and notification threshold will NOT be touched.")
}
