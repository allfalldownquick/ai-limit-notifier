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

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

var testPairingSecret = []byte("test-pairing-secret-not-for-production")

func newPairTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(s)
	srv.SetPairingSecret(testPairingSecret)
	return srv, s
}

func issueTestCode(t *testing.T, s *store.Store, userID string) string {
	t.Helper()
	code, err := auth.GeneratePairingCode()
	if err != nil {
		t.Fatal(err)
	}
	verifier := auth.PairingCodeVerifier(testPairingSecret, auth.NormalizePairingCode(code))
	if _, err := s.CreatePairingCode(context.Background(), userID, verifier, time.Now().UTC(), 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	return code
}

func doPair(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pair", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func pairBody(code string) string {
	return fmt.Sprintf(`{"code":%q,"client_version":"0.1.0","platform":"linux-wsl-amd64"}`, code)
}

func TestPairSuccess(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code := issueTestCode(t, s, userID)

	rec := doPair(t, srv, pairBody(code))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp pairResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Linked || resp.DeviceID == "" || resp.DeviceToken == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	d, err := s.AuthenticateDevice(ctx, resp.DeviceToken)
	if err != nil {
		t.Fatalf("issued token failed to authenticate: %v", err)
	}
	if d.UserID != userID {
		t.Fatalf("unexpected user: %s", d.UserID)
	}
}

func TestPairWithoutFormattingStillWorks(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, _ := s.CreateUser(ctx)
	code := issueTestCode(t, s, userID)
	unformatted := strings.ToLower(strings.ReplaceAll(code, "-", ""))

	rec := doPair(t, srv, pairBody(unformatted))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestPairUnknownExpiredConsumedSameError(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, _ := s.CreateUser(ctx)

	unknownCode := "ABCDEFGHJK"

	expiredCodeRaw, err := auth.GeneratePairingCode()
	if err != nil {
		t.Fatal(err)
	}
	expiredVerifier := auth.PairingCodeVerifier(testPairingSecret, auth.NormalizePairingCode(expiredCodeRaw))
	if _, err := s.CreatePairingCode(ctx, userID, expiredVerifier, time.Now().Add(-20*time.Minute).UTC(), 10*time.Minute); err != nil {
		t.Fatal(err)
	}

	consumedCode := issueTestCode(t, s, userID)
	doPair(t, srv, pairBody(consumedCode)) // consume it once

	recUnknown := doPair(t, srv, pairBody(unknownCode))
	recExpired := doPair(t, srv, pairBody(expiredCodeRaw))
	recConsumed := doPair(t, srv, pairBody(consumedCode))

	for name, rec := range map[string]*httptest.ResponseRecorder{"unknown": recUnknown, "expired": recExpired, "consumed": recConsumed} {
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400, body=%s", name, rec.Code, rec.Body.String())
		}
	}
	if recUnknown.Body.String() != recExpired.Body.String() || recExpired.Body.String() != recConsumed.Body.String() {
		t.Fatalf("responses must be identical to avoid a code oracle:\nunknown=%s\nexpired=%s\nconsumed=%s",
			recUnknown.Body.String(), recExpired.Body.String(), recConsumed.Body.String())
	}
}

func TestPairRejectsUnknownFields(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, _ := s.CreateUser(ctx)
	code := issueTestCode(t, s, userID)

	for _, field := range []string{
		`"telegram_user_id": 12345`,
		`"telegram_chat_id": 12345`,
		`"user_id": "usr_evil"`,
		`"chat_id": "12345"`,
		`"message": "hello"`,
		`"command": "rm -rf /"`,
		`"url": "https://evil.example"`,
	} {
		body := fmt.Sprintf(`{"code":%q,"client_version":"0.1.0","platform":"linux-wsl-amd64",%s}`, code, field)
		rec := doPair(t, srv, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("field %s: status = %d, want 400", field, rec.Code)
		}
	}
}

func TestPairRejectsOversizedBody(t *testing.T) {
	srv, _ := newPairTestServer(t)
	huge := strings.Repeat("a", maxPairBodyBytes*2)
	body := fmt.Sprintf(`{"code":"ABCDEFGHJK","client_version":"0.1.0","platform":"linux","padding":"%s"}`, huge)
	rec := doPair(t, srv, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestPairRejectsMalformedJSON(t *testing.T) {
	srv, _ := newPairTestServer(t)
	rec := doPair(t, srv, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPairRejectsTrailingJSON(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, _ := s.CreateUser(ctx)
	code := issueTestCode(t, s, userID)
	rec := doPair(t, srv, pairBody(code)+`{"extra":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPairRejectsInvalidClientVersion(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, _ := s.CreateUser(ctx)
	code := issueTestCode(t, s, userID)
	body := fmt.Sprintf(`{"code":%q,"client_version":"","platform":"linux-wsl-amd64"}`, code)
	rec := doPair(t, srv, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for empty client_version", rec.Code)
	}

	tooLong := strings.Repeat("x", maxClientVersionLen+1)
	body = fmt.Sprintf(`{"code":%q,"client_version":%q,"platform":"linux-wsl-amd64"}`, code, tooLong)
	rec = doPair(t, srv, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for oversized client_version", rec.Code)
	}
}

func TestPairRejectsInvalidPlatform(t *testing.T) {
	srv, s := newPairTestServer(t)
	ctx := context.Background()
	userID, _ := s.CreateUser(ctx)

	for _, platform := range []string{"", "Linux WSL", "linux/amd64", "../../etc/passwd", strings.Repeat("a", 60)} {
		code := issueTestCode(t, s, userID)
		body := fmt.Sprintf(`{"code":%q,"client_version":"0.1.0","platform":%q}`, code, platform)
		rec := doPair(t, srv, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("platform %q: status = %d, want 400", platform, rec.Code)
		}
	}
}

func TestPairFailsClosedWithoutPairingSecret(t *testing.T) {
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := New(s) // no SetPairingSecret call

	rec := doPair(t, srv, pairBody("ABCDEFGHJK"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no pairing secret is configured", rec.Code)
	}
}

func TestPairRateLimited(t *testing.T) {
	srv, _ := newPairTestServer(t)
	srv.ipLimiter = newIPLimiter(1, 1)

	rec1 := doPair(t, srv, pairBody("ABCDEFGHJK"))
	if rec1.Code == http.StatusTooManyRequests {
		t.Fatal("first request should not be rate limited")
	}
	rec2 := doPair(t, srv, pairBody("ABCDEFGHJK"))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second immediate request: status = %d, want 429", rec2.Code)
	}
}

func TestPairDoesNotLeakTokenOnFailure(t *testing.T) {
	srv, _ := newPairTestServer(t)
	rec := doPair(t, srv, pairBody("ZZZZZZZZZZ"))
	if bytes.Contains(rec.Body.Bytes(), []byte("device_token")) {
		t.Fatalf("a failed pairing response must not mention device_token at all: %s", rec.Body.String())
	}
}
