package sink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/agent"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

func TestNewRejectsNonHTTPSNonLoopback(t *testing.T) {
	if _, err := New("http://example.com", "tok"); err == nil {
		t.Fatal("expected non-HTTPS non-loopback endpoint to be rejected")
	}
}

func TestNewAcceptsHTTPSAndLoopback(t *testing.T) {
	if _, err := New("https://example.com", "tok"); err != nil {
		t.Fatalf("unexpected error for https endpoint: %v", err)
	}
	if _, err := New("http://127.0.0.1:8080", "tok"); err != nil {
		t.Fatalf("unexpected error for loopback http endpoint: %v", err)
	}
	if _, err := New("http://localhost:8080", "tok"); err != nil {
		t.Fatalf("unexpected error for localhost http endpoint: %v", err)
	}
}

func TestBuildPayloadExactSchemaFiveHour(t *testing.T) {
	resetAt := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 9, 2, 20, 0, 0, 0, time.UTC)
	ev := agent.Event{
		Provider:    domain.ProviderCodex,
		Window:      domain.WindowFiveHour,
		UsedPercent: 83,
		ResetAt:     resetAt,
		ObservedAt:  observedAt,
	}

	b, err := json.Marshal(buildPayload(ev))
	if err != nil {
		t.Fatal(err)
	}

	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"schema_version": float64(1),
		"provider":       "codex",
		"observed_at":    "2026-09-02T20:00:00Z",
		"five_hour": map[string]any{
			"used_percent": float64(83),
			"reset_at":     "2026-09-08T12:00:00Z",
		},
	}
	gotJSON, _ := json.Marshal(generic)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("outbound schema mismatch:\n got:  %s\n want: %s", gotJSON, wantJSON)
	}

	// Exactly the fields docs/PROTOCOL_V1.md allows — nothing else.
	if _, ok := generic["weekly"]; ok {
		t.Fatal("five_hour-only event must not include a weekly field")
	}
}

func TestBuildPayloadExactSchemaWeekly(t *testing.T) {
	ev := agent.Event{
		Provider:    domain.ProviderClaude,
		Window:      domain.WindowWeekly,
		UsedPercent: 57,
		ResetAt:     time.Date(2026, 9, 8, 8, 12, 0, 0, time.UTC),
		ObservedAt:  time.Date(2026, 9, 2, 18, 15, 0, 0, time.UTC),
	}
	b, _ := json.Marshal(buildPayload(ev))
	var generic map[string]any
	json.Unmarshal(b, &generic)
	if _, ok := generic["five_hour"]; ok {
		t.Fatal("weekly-only event must not include a five_hour field")
	}
	weekly, ok := generic["weekly"].(map[string]any)
	if !ok || weekly["used_percent"] != float64(57) {
		t.Fatalf("unexpected weekly field: %v", generic["weekly"])
	}
}

func TestSendSuccessSetsAuthHeaderAndBody(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]bool{"accepted": true, "persisted": true})
	}))
	defer srv.Close()

	s, err := New(srv.URL, "alnd_test-token")
	if err != nil {
		t.Fatal(err)
	}
	ev := agent.Event{Provider: domain.ProviderCodex, Window: domain.WindowFiveHour, UsedPercent: 90, ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	if err := s.Send(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer alnd_test-token" {
		t.Fatalf("unexpected Authorization header: %q", gotAuth)
	}
	if gotBody["provider"] != "codex" {
		t.Fatalf("unexpected body: %v", gotBody)
	}
}

func TestSendServerRejectionSurfacesErrorWithoutLeakingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_snapshot"})
	}))
	defer srv.Close()

	secretToken := "alnd_super-secret-should-not-leak"
	s, err := New(srv.URL, secretToken)
	if err != nil {
		t.Fatal(err)
	}
	ev := agent.Event{Provider: domain.ProviderCodex, Window: domain.WindowFiveHour, UsedPercent: 90, ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	err = s.Send(context.Background(), ev)
	if err == nil {
		t.Fatal("expected an error for a rejected submission")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("token leaked into error text: %v", err)
	}
}

func TestSendNotAcceptedIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"accepted": false, "persisted": false})
	}))
	defer srv.Close()

	s, err := New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	ev := agent.Event{Provider: domain.ProviderCodex, Window: domain.WindowFiveHour, UsedPercent: 90, ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	if err := s.Send(context.Background(), ev); err == nil {
		t.Fatal("expected an error when the server does not durably acknowledge")
	}
}

func TestSendRespectsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	s, err := New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ev := agent.Event{Provider: domain.ProviderCodex, Window: domain.WindowFiveHour, UsedPercent: 90, ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}
	start := time.Now()
	if err := s.Send(ctx, ev); err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Send did not respect the context deadline")
	}
}
