package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store, string, string) {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	userID, err := s.CreateUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deviceID, rawToken, err := s.CreateDevice(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	_ = deviceID

	return New(s), s, userID, rawToken
}

func doUsage(t *testing.T, srv *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/usage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func validPayload(usedPercent float64) string {
	now := time.Now().UTC()
	resetAt := now.Add(2 * time.Hour).Format(time.RFC3339)
	return fmt.Sprintf(`{
		"schema_version": 1,
		"provider": "codex",
		"observed_at": %q,
		"five_hour": {"used_percent": %v, "reset_at": %q}
	}`, now.Format(time.RFC3339), usedPercent, resetAt)
}

// --- AUTH --------------------------------------------------------------

func TestUsageValidToken(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	rec := doUsage(t, srv, token, validPayload(50))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUsageInvalidToken(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := doUsage(t, srv, "alnd_totally-not-real", validPayload(50))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUsageRevokedToken(t *testing.T) {
	srv, s, userID, _ := newTestServer(t)
	deviceID, raw, err := s.CreateDevice(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(context.Background(), deviceID); err != nil {
		t.Fatal(err)
	}
	rec := doUsage(t, srv, raw, validPayload(50))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUsageAbsentToken(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := doUsage(t, srv, "", validPayload(50))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUsageInvalidAndRevokedTokenIndistinguishable(t *testing.T) {
	srv, s, userID, _ := newTestServer(t)
	deviceID, raw, err := s.CreateDevice(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(context.Background(), deviceID); err != nil {
		t.Fatal(err)
	}

	revokedRec := doUsage(t, srv, raw, validPayload(50))
	invalidRec := doUsage(t, srv, "alnd_never-existed", validPayload(50))

	if revokedRec.Code != invalidRec.Code || revokedRec.Body.String() != invalidRec.Body.String() {
		t.Fatalf("revoked vs unknown token responses must be identical: %d/%q vs %d/%q",
			revokedRec.Code, revokedRec.Body.String(), invalidRec.Code, invalidRec.Body.String())
	}
}

func TestTokenNeverAppearsInErrorBody(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	secret := "alnd_super-secret-value-should-not-leak"
	rec := doUsage(t, srv, secret, validPayload(50))
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("token leaked into response body: %s", rec.Body.String())
	}
}

// --- JSON strictness -----------------------------------------------------

func TestUsageRejectsUnknownField(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	now := time.Now().UTC()
	body := fmt.Sprintf(`{
		"schema_version": 1, "provider": "codex", "observed_at": %q,
		"five_hour": {"used_percent": 50, "reset_at": %q},
		"telegram_chat_id": "12345"
	}`, now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	rec := doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown field; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUsageRejectsOversizedBody(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	huge := bytes.Repeat([]byte("a"), maxUsageBodyBytes*2)
	body := fmt.Sprintf(`{"schema_version":1,"provider":"codex","padding":"%s"}`, huge)
	rec := doUsage(t, srv, token, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestUsageRejectsMalformedJSON(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	rec := doUsage(t, srv, token, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUsageRejectsTrailingJSON(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	rec := doUsage(t, srv, token, validPayload(50)+`{"extra":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for trailing JSON", rec.Code)
	}
}

func TestUsageRejectsInvalidProvider(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	now := time.Now().UTC()
	body := fmt.Sprintf(`{"schema_version":1,"provider":"gemini","observed_at":%q,"five_hour":{"used_percent":50,"reset_at":%q}}`,
		now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	rec := doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUsageRejectsInvalidSchemaVersion(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	now := time.Now().UTC()
	body := fmt.Sprintf(`{"schema_version":99,"provider":"codex","observed_at":%q,"five_hour":{"used_percent":50,"reset_at":%q}}`,
		now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	rec := doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUsageRejectsNoWindows(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	now := time.Now().UTC()
	body := fmt.Sprintf(`{"schema_version":1,"provider":"codex","observed_at":%q}`, now.Format(time.RFC3339))
	rec := doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUsageRejectsInvalidPercent(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	rec := doUsage(t, srv, token, validPayload(150))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for >100%%", rec.Code)
	}
	rec = doUsage(t, srv, token, validPayload(-5))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for negative percent", rec.Code)
	}
}

func TestUsageRejectsInvalidTimestamps(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	now := time.Now().UTC()
	body := fmt.Sprintf(`{"schema_version":1,"provider":"codex","observed_at":"not-a-time","five_hour":{"used_percent":50,"reset_at":%q}}`,
		now.Add(time.Hour).Format(time.RFC3339))
	rec := doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for bad observed_at", rec.Code)
	}

	body = fmt.Sprintf(`{"schema_version":1,"provider":"codex","observed_at":%q,"five_hour":{"used_percent":50,"reset_at":"not-a-time"}}`,
		now.Format(time.RFC3339))
	rec = doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for bad reset_at", rec.Code)
	}

	// Absurdly far future reset_at.
	body = fmt.Sprintf(`{"schema_version":1,"provider":"codex","observed_at":%q,"five_hour":{"used_percent":50,"reset_at":%q}}`,
		now.Format(time.RFC3339), now.Add(365*24*time.Hour).Format(time.RFC3339))
	rec = doUsage(t, srv, token, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for absurd reset_at", rec.Code)
	}
}

func TestUsageRejectsWrongContentType(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/usage", strings.NewReader(validPayload(50)))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

// --- EVENT / persistence behavior ----------------------------------------

func TestUsageBelowThresholdCreatesNoEvent(t *testing.T) {
	srv, s, _, token := newTestServer(t)
	rec := doUsage(t, srv, token, validPayload(79))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	due, err := s.DueEvents(context.Background(), time.Now().Add(3*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("79%% must not create a durable event, got %d", len(due))
	}
}

func TestUsageAtThresholdCreatesOneDurableEvent(t *testing.T) {
	srv, s, _, token := newTestServer(t)
	rec := doUsage(t, srv, token, validPayload(80))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp usageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted || !resp.Persisted {
		t.Fatalf("expected accepted+persisted true, got %+v", resp)
	}

	due, err := s.DueEvents(context.Background(), time.Now().Add(3*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("80%% must create exactly one durable event, got %d", len(due))
	}
}

func TestUsageRetrySamePayloadIsIdempotent(t *testing.T) {
	srv, s, _, token := newTestServer(t)
	body := validPayload(85)

	for i := 0; i < 3; i++ {
		rec := doUsage(t, srv, token, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d", i, rec.Code)
		}
	}

	due, err := s.DueEvents(context.Background(), time.Now().Add(3*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("retrying the same snapshot must not duplicate events, got %d", len(due))
	}
}

// --- SECURITY: no destination/text/command channel ------------------------

func TestUsageCannotSupplyTelegramDestinationOrCommands(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	now := time.Now().UTC()
	forbidden := []string{
		`"chat_id": "12345"`,
		`"message": "hello"`,
		`"command": "rm -rf /"`,
		`"url": "https://evil.example/payload"`,
		`"path": "/etc/passwd"`,
	}
	for _, field := range forbidden {
		body := fmt.Sprintf(`{"schema_version":1,"provider":"codex","observed_at":%q,"five_hour":{"used_percent":50,"reset_at":%q},%s}`,
			now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339), field)
		rec := doUsage(t, srv, token, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("field %s: status = %d, want 400 (strict decoder must reject it)", field, rec.Code)
		}
	}
}

// --- rate limiting ---------------------------------------------------------

func TestUsageDeviceRateLimitEnforced(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	srv.deviceLimiter = newDeviceLimiter(1, 1) // 1 token, refilling at 1/s: the 2nd immediate request must be limited

	rec1 := doUsage(t, srv, token, validPayload(10))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, body=%s", rec1.Code, rec1.Body.String())
	}
	rec2 := doUsage(t, srv, token, validPayload(10))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second immediate request: status = %d, want 429", rec2.Code)
	}
}

func TestUsageIPRateLimitEnforcedOnBadAuth(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	srv.ipLimiter = newIPLimiter(1, 1)

	rec1 := doUsage(t, srv, "alnd_wrong-1", validPayload(10))
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("first bad-token request: status = %d, want 401", rec1.Code)
	}
	rec2 := doUsage(t, srv, "alnd_wrong-2", validPayload(10))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second immediate bad-token request: status = %d, want 429", rec2.Code)
	}
}

// --- health/status ---------------------------------------------------------

func TestHealthzNoAuthRequired(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestStatusRequiresAuth(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a token", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
