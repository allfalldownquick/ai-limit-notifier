package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func reqFrom(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestClientIPDirectClientFakeXFFIgnored(t *testing.T) {
	srv := &Server{trustedProxies: &trustedProxies{}} // nothing trusted, matching New's default
	r := reqFrom("203.0.113.9:5555", "1.2.3.4")       // an untrusted peer claiming to be someone else
	if got := srv.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want the real peer 203.0.113.9 (XFF from an untrusted peer must be ignored)", got)
	}
}

func TestClientIPTrustedProxyWithXFFExtractsRealClient(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	// A genuine single Caddy hop with no prior proxy in front of it: Caddy
	// sets X-Forwarded-For to exactly what it saw, one entry.
	r := reqFrom("127.0.0.1:9999", "198.51.100.7")
	if got := srv.clientIP(r); got != "198.51.100.7" {
		t.Fatalf("clientIP = %q, want 198.51.100.7 extracted from XFF via the trusted proxy", got)
	}
}

// TestClientIPTrustedProxyIgnoresSpoofedLeadingXFFEntry is the crux of the
// single-hop trust model: a reverse proxy (Caddy) APPENDS the address it
// itself observed rather than replacing whatever X-Forwarded-For value a
// client sent it, so anyone connecting directly to Caddy can put an
// arbitrary value in the *leading* position. Only the trailing entry — the
// one Caddy itself added — may be trusted.
func TestClientIPTrustedProxyIgnoresSpoofedLeadingXFFEntry(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	// "9.9.9.9" is an attacker-supplied header sent straight to Caddy;
	// "203.0.113.50" is what Caddy itself appended as the peer it actually saw.
	r := reqFrom("127.0.0.1:9999", "9.9.9.9, 203.0.113.50")
	if got := srv.clientIP(r); got != "203.0.113.50" {
		t.Fatalf("clientIP = %q, want 203.0.113.50 (the trusted proxy's own appended entry); a spoofed leading entry must never be trusted", got)
	}
}

func TestClientIPUntrustedProxyXFFIgnored(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	// A different peer entirely, not in the trusted CIDR.
	r := reqFrom("198.51.100.1:4444", "1.2.3.4")
	if got := srv.clientIP(r); got != "198.51.100.1" {
		t.Fatalf("clientIP = %q, want the real (untrusted) peer 198.51.100.1", got)
	}
}

func TestClientIPMalformedXFFSafeFallback(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	r := reqFrom("127.0.0.1:9999", "not-an-ip-address")
	if got := srv.clientIP(r); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want fallback to the trusted peer 127.0.0.1 on malformed XFF", got)
	}
}

// TestClientIPMalformedTrailingEntryDoesNotFallThroughToEarlierEntry proves
// that a malformed *last* entry falls all the way back to the trusted
// peer's own address — never to an earlier, untrusted entry, which would
// silently reopen the spoofing hole a valid trailing entry closes.
func TestClientIPMalformedTrailingEntryDoesNotFallThroughToEarlierEntry(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	r := reqFrom("127.0.0.1:9999", "203.0.113.50, not-an-ip-address")
	if got := srv.clientIP(r); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want fallback to the trusted peer 127.0.0.1, not the earlier untrusted entry 203.0.113.50", got)
	}
}

func TestClientIPNoXFFFromTrustedProxyFallsBackToPeer(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	r := reqFrom("127.0.0.1:9999", "")
	if got := srv.clientIP(r); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want 127.0.0.1 when the trusted proxy sent no XFF at all", got)
	}
}

func TestSetTrustedProxiesRejectsInvalidCIDR(t *testing.T) {
	srv := &Server{}
	if err := srv.SetTrustedProxies([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected an error for an invalid CIDR")
	}
}

func TestNewDefaultsToTrustingNothing(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := reqFrom("127.0.0.1:9999", "198.51.100.7")
	if got := srv.clientIP(r); got != "127.0.0.1" {
		t.Fatalf("New() must default to trusting no proxy; got clientIP = %q", got)
	}
}
