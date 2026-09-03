package integration_test

// Full P4 local end-to-end proof, with nothing faked except the Telegram
// HTTP endpoint itself (the owner's explicit instruction: no real Telegram
// bot token yet). Every other component is real: a real SQLite file, a
// real api.Server over real loopback HTTP, a real telegrambot.Bot, a real
// scheduler, and — for the two CLI-facing steps — the actual compiled
// ai-limit-notifier binary run as a real subprocess (`link`, then
// `monitor` against a real Codex read on this machine), so the proof
// covers exactly what a real user's machine would do:
//
//	fake Telegram -> real bot worker -> /start Update -> real SQLite
//	pairing code -> real `ai-limit-notifier link CODE` subprocess ->
//	real POST /api/v1/pair -> real device credential -> real local
//	static config -> real `ai-limit-notifier monitor` subprocess (real
//	Codex reader, real HTTPSink) -> real server SQLite -> a simulated
//	restart -> scheduler -> fake Telegram sendMessage.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/api"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/scheduler"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/telegrambot"
)

// --- fake Telegram (the one and only faked component) ----------------------

type e2eSentMessage struct {
	ChatID string
	Text   string
}

type fakeTelegram struct {
	mu      sync.Mutex
	pending []json.RawMessage
	sent    []e2eSentMessage
	srv     *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	ft := &fakeTelegram{}
	ft.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			ft.mu.Lock()
			result := ft.pending
			ft.pending = nil
			ft.mu.Unlock()
			if len(result) == 0 {
				time.Sleep(20 * time.Millisecond)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body struct {
				ChatID string `json:"chat_id"`
				Text   string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			ft.mu.Lock()
			ft.sent = append(ft.sent, e2eSentMessage{ChatID: body.ChatID, Text: body.Text})
			ft.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ft.srv.Close)
	return ft
}

func (ft *fakeTelegram) queueStart(updateID, fromID, chatID int64) {
	raw, _ := json.Marshal(map[string]any{
		"update_id": updateID,
		"message": map[string]any{
			"from": map[string]any{"id": fromID},
			"chat": map[string]any{"id": chatID, "type": "private"},
			"text": "/start",
		},
	})
	ft.mu.Lock()
	ft.pending = append(ft.pending, raw)
	ft.mu.Unlock()
}

func (ft *fakeTelegram) messages() []e2eSentMessage {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	out := make([]e2eSentMessage, len(ft.sent))
	copy(out, ft.sent)
	return out
}

func waitForE2E(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

var codePattern = regexp.MustCompile(`[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{2}`)

// buildAgentBinary compiles the real ai-limit-notifier binary once for this
// test, so the `link` and `monitor` steps below are the actual shipped CLI,
// not a Go-level simulation of it.
func buildAgentBinary(t *testing.T) string {
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

func TestP4FullLocalEndToEnd(t *testing.T) {
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, probeErr := codex.New().Read(probeCtx)
	probeCancel()
	if probeErr != nil {
		t.Skipf("codex not available on this machine, skipping the full P4 E2E: %v", probeErr)
	}

	bin := buildAgentBinary(t)
	dbPath := filepath.Join(t.TempDir(), "p4-e2e.db")
	pairingSecret := []byte("p4-e2e-test-pairing-secret")

	// --- real server, real store, real API over real loopback HTTP ---
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	apiServer := api.New(st)
	apiServer.SetPairingSecret(pairingSecret)
	// The server itself no longer gates on used_percent at all — threshold
	// selection is entirely local/client-side (see the `monitor --threshold
	// 1` subprocess invocation below). Any schema-valid, authenticated
	// submission creates a durable event.
	apiHTTP := httptest.NewServer(apiServer.Handler())
	defer apiHTTP.Close()

	// --- real bot worker, pointed at the one faked component ---
	ft := newFakeTelegram(t)
	bot := telegrambot.New("e2e-test-bot-token", st, pairingSecret).WithAPIBase(ft.srv.URL)
	botCtx, cancelBot := context.WithCancel(context.Background())
	go bot.Run(botCtx)

	// --- real scheduler, delivering to the same fake Telegram ---
	realDeliveryToFake := delivery.NewTelegramDelivery("e2e-test-bot-token")
	realDeliveryToFake.SetAPIBase(ft.srv.URL)

	// 1. A real Telegram Update, as Telegram would send it.
	const telegramUserID, telegramChatID = 909090, 909090
	ft.queueStart(1, telegramUserID, telegramChatID)
	waitForE2E(t, 3*time.Second, func() bool { return len(ft.messages()) == 1 })
	code := codePattern.FindString(ft.messages()[0].Text)
	if code == "" {
		t.Fatalf("no pairing code found in bot reply: %q", ft.messages()[0].Text)
	}

	// 2. The real `link` CLI subprocess, exactly as a user would run it.
	configDir := t.TempDir()
	linkCmd := exec.Command(bin, "link", code, "--server-url", apiHTTP.URL)
	linkCmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configDir)
	linkOut, err := linkCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real `link` subprocess failed: %v\n%s", err, linkOut)
	}
	t.Logf("link output:\n%s", linkOut)

	cfgPath := filepath.Join(configDir, "ai-limit-notifier", "config.json")
	cfgFi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("link did not produce a config file: %v", err)
	}
	if perm := cfgFi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 0600", perm)
	}
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var localCfg localconfig.Config
	if err := json.Unmarshal(cfgBytes, &localCfg); err != nil {
		t.Fatal(err)
	}
	if localCfg.DeviceToken == "" || localCfg.DeviceID == "" {
		t.Fatalf("unexpected saved config: %+v", localCfg)
	}
	if strings.Contains(string(linkOut), localCfg.DeviceToken) {
		t.Fatal("the real device token must never be printed to stdout by `link`")
	}

	// 3. The real `monitor` subprocess: real Codex reader, real HTTPSink,
	// picking up server_url + device_token entirely from the config `link`
	// wrote — no manual token copying, matching the P4 goal statement.
	// --threshold is an explicit one-run override of the local notification
	// threshold (never persisted) so today's real (possibly low) live Codex
	// usage still demonstrates the pipeline; the reset_at monitor submits
	// is still the real one Codex reports.
	monitorCmd := exec.Command(bin, "monitor", "--dry-run=false", "--codex-interval", "2s", "--threshold", "1")
	monitorCmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configDir)
	monitorOut, err := monitorCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	monitorCmd.Stderr = monitorCmd.Stdout
	if err := monitorCmd.Start(); err != nil {
		t.Fatal(err)
	}
	monitorLog := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			n, err := monitorOut.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		monitorLog <- string(buf)
	}()
	var stopMonitorOnce sync.Once
	stopMonitor := func() {
		stopMonitorOnce.Do(func() {
			_ = monitorCmd.Process.Kill()
			_, _ = monitorCmd.Process.Wait()
			t.Logf("monitor output:\n%s", <-monitorLog)
		})
	}
	defer stopMonitor()

	// Wait for a durable event to land in the server's store — proof the
	// real subprocess chain (Codex -> HTTPSink -> real HTTP -> real
	// server -> real SQLite) worked, without needing to parse monitor's
	// own stdout.
	var eventUserID string
	waitForE2E(t, 15*time.Second, func() bool {
		row := st_queryLatestEventUser(t, st)
		if row == "" {
			return false
		}
		eventUserID = row
		return true
	})

	// Stop the subprocess before touching the store further: it holds an
	// open HTTP connection against apiHTTP (backed by this *store.Store),
	// and closing st out from under a still-running client would just add
	// noise, not signal.
	stopMonitor()

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// 4. Simulated restart: close and reopen the same database file — the
	// same faithful restart proof used throughout P2/P3, since durability
	// lives in the SQLite file, not the OS process boundary.
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()

	dueBeforeFastForward, err := st2.DueEvents(ctx, time.Now().Add(24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dueBeforeFastForward) == 0 {
		t.Fatal("expected the durable event to survive the simulated restart")
	}
	realResetAt := dueBeforeFastForward[0].ResetAt

	sch := scheduler.New(st2, realDeliveryToFake)
	fastForwardedNow := realResetAt.Add(2 * time.Minute)
	sch.Now = func() time.Time { return fastForwardedNow }
	sch.Tick(ctx)

	waitForE2E(t, 3*time.Second, func() bool { return len(ft.messages()) >= 2 })
	notification := ft.messages()[len(ft.messages())-1]

	// 5. The destination came from server-side user association alone —
	// the exact chat id resolved from the /start Update in step 1 — never
	// from the local agent, which never even learns a chat id.
	wantChatID := "909090"
	if notification.ChatID != wantChatID {
		t.Fatalf("notification chat id = %q, want %q (resolved server-side from the original /start)", notification.ChatID, wantChatID)
	}
	if !strings.Contains(notification.Text, "available again now") {
		t.Fatalf("unexpected notification text: %q", notification.Text)
	}

	cancelBot()
	_ = eventUserID
	t.Logf("real Codex %s/%s at %.0f%% used, real reset_at %s (the server's own threshold, not the submitted percent, was lowered to 1%% so this run doesn't depend on live usage already being at/above the real 80%% default)",
		dueBeforeFastForward[0].Provider, dueBeforeFastForward[0].WindowKind, dueBeforeFastForward[0].UsedPercent, realResetAt.Format(time.RFC3339))
}

func st_queryLatestEventUser(t *testing.T, st *store.Store) string {
	t.Helper()
	due, err := st.DueEvents(context.Background(), time.Now().Add(24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) == 0 {
		return ""
	}
	return due[0].UserID
}
