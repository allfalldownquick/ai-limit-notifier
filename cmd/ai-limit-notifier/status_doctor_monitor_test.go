package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allfalldownquick/ai-limit-notifier/internal/localconfig"
	"github.com/allfalldownquick/ai-limit-notifier/internal/sink"
)

// --- status / printLinkStatus -----------------------------------------

func TestPrintLinkStatusLinked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := localconfig.Save(&localconfig.Config{
		SchemaVersion: 1,
		ServerURL:     "https://notifier-api.example.com",
		DeviceID:      "dev_abc",
		DeviceToken:   "alnd_super-secret-value",
	}); err != nil {
		t.Fatal(err)
	}

	out := withCapturedStdout(t, printLinkStatus)
	if !strings.Contains(out, "Linked: yes") {
		t.Fatalf("expected Linked: yes, got %s", out)
	}
	if !strings.Contains(out, "Device: configured") {
		t.Fatalf("expected Device: configured, got %s", out)
	}
	if !strings.Contains(out, "Server: https://notifier-api.example.com") {
		t.Fatalf("expected server URL, got %s", out)
	}
	if strings.Contains(out, "alnd_super-secret-value") {
		t.Fatalf("device token must never be printed, got %s", out)
	}
}

func TestPrintLinkStatusNotLinked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	out := withCapturedStdout(t, printLinkStatus)
	if !strings.Contains(out, "Linked: no") {
		t.Fatalf("expected Linked: no with no config present, got %s", out)
	}
}

func TestPrintLinkStatusMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := localconfig.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := withCapturedStdout(t, printLinkStatus)
	if !strings.Contains(out, "Linked: unknown") {
		t.Fatalf("expected a safe 'unknown' diagnostic for a malformed config, got %s", out)
	}
	if strings.Contains(out, "not valid json") {
		t.Fatalf("must not echo file content, got %s", out)
	}
}

// --- doctor: authenticated server checks --------------------------------

func fakeDoctorServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "protocol_version": doctorProtocolVersion, "server_time": "2026-01-01T00:00:00Z"})
		case "/api/v1/status":
			if got := r.Header.Get("Authorization"); got != "Bearer alnd_doctor-test-token" {
				t.Errorf("unexpected Authorization header: %q", got)
			}
			w.WriteHeader(statusCode)
			if statusCode == http.StatusOK {
				_ = json.NewEncoder(w).Encode(map[string]any{"protocol_version": doctorProtocolVersion, "device_valid": true, "server_time": "2026-01-01T00:00:00Z"})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
			}
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
}

func TestRunDoctorAuthenticatedStatusValid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	srv := fakeDoctorServer(t, http.StatusOK)
	defer srv.Close()
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, ServerURL: srv.URL, DeviceID: "dev_x", DeviceToken: "alnd_doctor-test-token"}); err != nil {
		t.Fatal(err)
	}

	out := withCapturedStdout(t, func() { runDoctor(nil) })
	if !strings.Contains(out, "link config: linked") {
		t.Fatalf("expected linked, got %s", out)
	}
	if !strings.Contains(out, "server reachability: OK") {
		t.Fatalf("expected server reachability OK, got %s", out)
	}
	if !strings.Contains(out, "protocol compatibility: OK") {
		t.Fatalf("expected protocol compatibility OK, got %s", out)
	}
	if !strings.Contains(out, "device credential: valid") {
		t.Fatalf("expected device credential valid, got %s", out)
	}
	if strings.Contains(out, "alnd_doctor-test-token") {
		t.Fatalf("device token must never be printed, got %s", out)
	}
}

func TestRunDoctorRevokedTokenDiagnosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	srv := fakeDoctorServer(t, http.StatusUnauthorized)
	defer srv.Close()
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, ServerURL: srv.URL, DeviceID: "dev_x", DeviceToken: "alnd_doctor-test-token"}); err != nil {
		t.Fatal(err)
	}

	var code int
	out := withCapturedStdout(t, func() { code = runDoctor(nil) })
	if !strings.Contains(out, "device credential: REVOKED or INVALID") {
		t.Fatalf("expected revoked/invalid diagnosis, got %s", out)
	}
	if code == 0 {
		t.Fatal("a revoked credential must fail doctor's overall result")
	}
}

func TestRunDoctorNotLinked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	out := withCapturedStdout(t, func() { runDoctor(nil) })
	if !strings.Contains(out, "link config: not linked") {
		t.Fatalf("expected not-linked diagnostic, got %s", out)
	}
}

// --- monitor sink selection ----------------------------------------------

func TestResolveMonitorSinkDryRunNeverSendsEvenWhenLinked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_DEVICE_TOKEN", "alnd_token")

	s, label, err := resolveMonitorSink(true, "https://notifier-api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*printSink); !ok {
		t.Fatalf("expected *printSink for --dry-run, got %T", s)
	}
	if label != "dry-run=true" {
		t.Fatalf("unexpected status line: %q", label)
	}
}

func TestResolveMonitorSinkUnlinkedFailsClosed(t *testing.T) {
	s, _, err := resolveMonitorSink(false, "")
	if err == nil {
		t.Fatal("expected a fail-closed error for unlinked, non-dry-run monitor")
	}
	if s != nil {
		t.Fatalf("expected no sink on error, got %v", s)
	}
	if !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("expected a 'not linked' error, got %v", err)
	}
}

func TestResolveMonitorSinkLinkedUsesHTTPSink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_DEVICE_TOKEN", "alnd_token")

	s, label, err := resolveMonitorSink(false, "https://notifier-api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*sink.HTTPSink); !ok {
		t.Fatalf("expected *sink.HTTPSink when linked without --dry-run, got %T", s)
	}
	if !strings.Contains(label, "https://notifier-api.example.com") {
		t.Fatalf("unexpected status line: %q", label)
	}
}

// --- threshold config -----------------------------------------------------

func TestRunConfigThresholdSetAndShow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	out := withCapturedStdout(t, func() {
		if code := runConfig([]string{"threshold", "1"}); code != 0 {
			t.Fatalf("runConfig exit = %d", code)
		}
	})
	if !strings.Contains(out, "notification threshold set to 1%") {
		t.Fatalf("unexpected output: %s", out)
	}

	out = withCapturedStdout(t, func() { runConfig(nil) })
	if !strings.Contains(out, "Notification threshold: 1%") {
		t.Fatalf("expected the just-set threshold, got %s", out)
	}
}

func TestRunConfigThresholdDefaultWithNoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	out := withCapturedStdout(t, func() { runConfig(nil) })
	if !strings.Contains(out, "Notification threshold: 80%") {
		t.Fatalf("expected default 80%%, got %s", out)
	}
}

func TestRunConfigThresholdRejectsOutOfRange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	for _, bad := range []string{"0", "-5", "101", "abc"} {
		if code := runConfig([]string{"threshold", bad}); code == 0 {
			t.Fatalf("threshold %q should have been rejected", bad)
		}
	}
	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NotificationThreshold != 0 {
		t.Fatalf("a rejected threshold must not be saved, got %+v", cfg)
	}
}

func TestRunConfigDoesNotTouchExistingLinkFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, ServerURL: "https://example.com", DeviceID: "dev_x", DeviceToken: "alnd_secret"}); err != nil {
		t.Fatal(err)
	}

	if code := runConfig([]string{"threshold", "50"}); code != 0 {
		t.Fatalf("runConfig exit = %d", code)
	}

	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "https://example.com" || cfg.DeviceID != "dev_x" || cfg.DeviceToken != "alnd_secret" {
		t.Fatalf("config threshold must not touch link fields, got %+v", cfg)
	}
	if cfg.NotificationThreshold != 50 {
		t.Fatalf("threshold not saved: %+v", cfg)
	}
}

func TestRunLinkPreservesExistingThresholdAcrossRelink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AI_LIMIT_NOTIFIER_SERVER_URL", "")
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, NotificationThreshold: 42}); err != nil {
		t.Fatal(err)
	}

	srv := fakePairServer(t, http.StatusOK, map[string]any{"linked": true, "device_id": "dev_new", "device_token": "alnd_new"})
	defer srv.Close()

	if code := runLink([]string{"ABCD-EFGH-JK", "--server-url", srv.URL}); code != 0 {
		t.Fatalf("runLink exit = %d", code)
	}

	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NotificationThreshold != 42 {
		t.Fatalf("relink must preserve the existing notification threshold, got %+v", cfg)
	}
}

// --- resolveMonitorThreshold -----------------------------------------------

func TestResolveMonitorThresholdExplicitFlagWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, NotificationThreshold: 42}); err != nil {
		t.Fatal(err)
	}

	if got := resolveMonitorThreshold(1); got != 1 {
		t.Fatalf("explicit --threshold must win over saved config, got %v", got)
	}

	// The override must never be persisted.
	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NotificationThreshold != 42 {
		t.Fatalf("a one-run --threshold override must not be saved, got %+v", cfg)
	}
}

func TestResolveMonitorThresholdFallsBackToConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := localconfig.Save(&localconfig.Config{SchemaVersion: 1, NotificationThreshold: 42}); err != nil {
		t.Fatal(err)
	}

	if got := resolveMonitorThreshold(unsetThreshold); got != 42 {
		t.Fatalf("unset flag should use the saved config threshold, got %v", got)
	}
}

func TestResolveMonitorThresholdDefaultsWithNoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if got := resolveMonitorThreshold(unsetThreshold); got != localconfig.DefaultNotificationThreshold {
		t.Fatalf("no config at all should default to %v, got %v", localconfig.DefaultNotificationThreshold, got)
	}
}

func TestRunMonitorRejectsOutOfRangeThresholdFlag(t *testing.T) {
	// This must fail fast, before entering the blocking poll/serve loop —
	// domain.ShouldScheduleReset silently never fires for threshold<=0,
	// which would otherwise look like a hung/silent monitor forever.
	for _, bad := range []string{"0", "-1", "101"} {
		if code := runMonitor([]string{"--threshold", bad, "--dry-run"}); code == 0 {
			t.Fatalf("--threshold %s should have been rejected", bad)
		}
	}
}

func TestResolveMonitorSinkLinkedNoTokenFails(t *testing.T) {
	t.Setenv("AI_LIMIT_NOTIFIER_DEVICE_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // no config saved -> no token from config either

	s, _, err := resolveMonitorSink(false, "https://notifier-api.example.com")
	if err == nil {
		t.Fatal("expected an error when a server is configured but no device token is found")
	}
	if s != nil {
		t.Fatalf("expected no sink on error, got %v", s)
	}
}
