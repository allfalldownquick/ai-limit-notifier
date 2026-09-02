package sink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/agent"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

func sampleEvent() agent.Event {
	return agent.Event{
		Provider:    domain.ProviderCodex,
		Window:      domain.WindowFiveHour,
		UsedPercent: 90,
		ResetAt:     time.Now().Add(time.Hour),
		ObservedAt:  time.Now(),
	}
}

// neverCalledServer fails the test the instant it receives any request —
// used as the redirect target to prove Send never actually follows there.
func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("redirect target must never be contacted, but received %s %s (Authorization=%q)",
			r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}))
}

func TestSendRejectsSameHostRedirect(t *testing.T) {
	target := neverCalledServer(t)
	defer target.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/usage", http.StatusFound)
	}))
	defer primary.Close()

	s, err := New(primary.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), sampleEvent()); err == nil {
		t.Fatal("expected a redirect (3xx) to be treated as a failure, not followed")
	}
}

func TestSendRejectsRedirectToDifferentHost(t *testing.T) {
	target := neverCalledServer(t)
	defer target.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A distinct origin, simulating an attacker-controlled redirect target.
		http.Redirect(w, r, target.URL+"/steal", http.StatusMovedPermanently)
	}))
	defer primary.Close()

	s, err := New(primary.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), sampleEvent()); err == nil {
		t.Fatal("expected a cross-host redirect to be rejected, not followed")
	}
}

func TestSendRejectsRedirectToPlainHTTPTarget(t *testing.T) {
	target := neverCalledServer(t)
	defer target.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Location explicitly names an insecure scheme.
		w.Header().Set("Location", "http://"+target.Listener.Addr().String()+"/api/v1/usage")
		w.WriteHeader(http.StatusSeeOther)
	}))
	defer primary.Close()

	s, err := New(primary.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), sampleEvent()); err == nil {
		t.Fatal("expected a redirect to a plain-HTTP target to be rejected, not followed")
	}
}

func TestSendNeverSendsBearerTokenToRedirectTarget(t *testing.T) {
	secretToken := "alnd_must-never-reach-the-redirect-target"
	tokenSeenAtTarget := false

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			tokenSeenAtTarget = true
		}
		t.Fatalf("redirect target must never be contacted at all")
	}))
	defer target.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/usage", http.StatusTemporaryRedirect)
	}))
	defer primary.Close()

	s, err := New(primary.URL, secretToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Send(context.Background(), sampleEvent())

	if tokenSeenAtTarget {
		t.Fatal("bearer token reached the redirect target")
	}
}
