package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
)

// --- config precedence -----------------------------------------------------

func TestResolveServerURLPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "https://from-env.example")
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, ServerURL: "https://from-config.example"}); err != nil {
		t.Fatal(err)
	}

	if got := resolveServerURL("https://from-flag.example"); got != "https://from-flag.example" {
		t.Fatalf("flag should win, got %q", got)
	}
	if got := resolveServerURL(""); got != "https://from-env.example" {
		t.Fatalf("env should win over config, got %q", got)
	}

	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")
	if got := resolveServerURL(""); got != "https://from-config.example" {
		t.Fatalf("config should be used when flag and env are both empty, got %q", got)
	}
}

func TestResolveServerURLDefaultsToEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")
	if got := resolveServerURL(""); got != "" {
		t.Fatalf("expected empty with nothing configured, got %q", got)
	}
}

func TestResolveDeviceTokenPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, DeviceToken: "alnd_from-config"}); err != nil {
		t.Fatal(err)
	}

	if got := resolveDeviceToken(); got != "alnd_from-config" {
		t.Fatalf("expected the config token, got %q", got)
	}

	t.Setenv("AI_LIMIT_NOTIFIER_DEVICE_TOKEN", "alnd_from-env")
	if got := resolveDeviceToken(); got != "alnd_from-env" {
		t.Fatalf("env should win over config, got %q", got)
	}
}

// --- link command ------------------------------------------------------

func withCapturedStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out)
}

func fakePairServer(t *testing.T, statusCode int, resp map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestRunLinkSuccessWritesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")
	t.Setenv("AI_LIMIT_NOTIFIER_DEVICE_TOKEN", "")

	srv := fakePairServer(t, http.StatusOK, map[string]any{
		"linked": true, "device_id": "dev_test123", "device_token": "alnd_should-not-print-in-stdout",
	})
	defer srv.Close()

	var code int
	stdout := withCapturedStdout(t, func() {
		code = runLink([]string{"ABCD-EFGH-JK", "--server-url", srv.URL})
	})
	if code != 0 {
		t.Fatalf("runLink exit code = %d, stdout=%s", code, stdout)
	}
	if strings.Contains(stdout, "alnd_should-not-print-in-stdout") {
		t.Fatalf("device token must never be printed, stdout=%s", stdout)
	}

	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceID != "dev_test123" || cfg.DeviceToken != "alnd_should-not-print-in-stdout" || cfg.ServerURL != srv.URL {
		t.Fatalf("unexpected saved config: %+v", cfg)
	}

	path, _ := localconfig.Path()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 0600", perm)
	}
}

func TestRunLinkFailedPairingLeavesNoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")

	srv := fakePairServer(t, http.StatusBadRequest, map[string]any{"error": "invalid_code"})
	defer srv.Close()

	code := runLink([]string{"ZZZZ-ZZZZ-ZZ", "--server-url", srv.URL})
	if code == 0 {
		t.Fatal("expected a non-zero exit code for a rejected pairing")
	}

	path := filepath.Join(dir, "ai-limit-notifier", "config.json")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a failed pairing must not leave a config/credential file behind")
	}
}

func TestFailedRelinkDoesNotCorruptExistingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")

	existing := &localconfig.Config{SchemaVersion: 1, ServerURL: "https://old.example", DeviceID: "dev_old", DeviceToken: "alnd_old-token"}
	if err := localconfig.Save(existing); err != nil {
		t.Fatal(err)
	}

	srv := fakePairServer(t, http.StatusBadRequest, map[string]any{"error": "invalid_code"})
	defer srv.Close()

	code := runLink([]string{"ZZZZ-ZZZZ-ZZ", "--server-url", srv.URL})
	if code == 0 {
		t.Fatal("expected the relink attempt to fail")
	}

	got, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *existing {
		t.Fatalf("existing config must be untouched by a failed relink: got %+v, want %+v", got, existing)
	}
}

func TestRunLinkRejectsNonLoopbackHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	code := runLink([]string{"ABCD-EFGH-JK", "--server-url", "http://example.com"})
	if code == 0 {
		t.Fatal("expected a non-zero exit code for a non-loopback plain-HTTP server URL")
	}
	path := filepath.Join(dir, "ai-limit-notifier", "config.json")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a rejected server URL must not leave a config file behind")
	}
}

func TestRunLinkDoesNotFollowRedirects(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target must never be contacted, got %s", r.URL.Path)
	}))
	defer target.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/pair", http.StatusFound)
	}))
	defer redirecting.Close()

	code := runLink([]string{"ABCD-EFGH-JK", "--server-url", redirecting.URL})
	if code == 0 {
		t.Fatal("expected a non-zero exit code when the pairing endpoint redirects rather than responds")
	}
}

func TestPairWithServerSendsExpectedRequest(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pair" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"linked": true, "device_id": "dev_x", "device_token": "alnd_y"})
	}))
	defer srv.Close()

	deviceID, token, err := pairWithServer(context.Background(), srv.URL, "ABCD-EFGH-JK")
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "dev_x" || token != "alnd_y" {
		t.Fatalf("unexpected result: %s %s", deviceID, token)
	}
	if gotBody["code"] != "ABCD-EFGH-JK" {
		t.Fatalf("unexpected code sent: %q", gotBody["code"])
	}
	if gotBody["client_version"] == "" || gotBody["platform"] == "" {
		t.Fatalf("expected non-empty client_version/platform, got %+v", gotBody)
	}
}
