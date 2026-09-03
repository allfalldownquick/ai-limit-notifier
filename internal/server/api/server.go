// Package api implements the strict, data-only device HTTP API from
// docs/PROTOCOL_V1.md: usage submission plus narrow health/status. It never
// accepts or returns shell commands, arbitrary URLs, executable payloads,
// or Telegram destination/text — see docs/DISTRIBUTION.md's "no remote
// execution" boundary.
package api

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

const (
	defaultCombineWindow  = 10 * time.Minute
	defaultPairingTTL     = 10 * time.Minute
	maxConcurrentHandlers = 64

	// Per-device: generous for a monitor process polling every 30s+, tight
	// enough to bound a malfunctioning/hostile client.
	deviceRateLimit = rate.Limit(1) // 1 req/s sustained
	deviceRateBurst = 5

	// Per-IP: mainly a brake on repeated bad-token guessing. /api/v1/pair
	// shares this limiter — it's unauthenticated by nature, so this is its
	// only per-request brake besides the code's own entropy/TTL/one-use.
	ipRateLimit = rate.Limit(2)
	ipRateBurst = 10
)

// Server holds everything the HTTP handlers need. Construct with New and
// call Handler() to get an http.Handler, or Serve to run a fully configured
// *http.Server.
type Server struct {
	Store *store.Store

	// CombineWindow is how close two same-window-kind, different-provider
	// reset_at values must be to produce one combined notification.
	CombineWindow time.Duration

	// PairingSecret keys pairing-code verifiers (see internal/server/auth's
	// PairingCodeVerifier). Required for /api/v1/pair to work at all —
	// New leaves it nil, and the handler fails closed until SetPairingSecret
	// is called.
	pairingSecret []byte
	pairingTTL    time.Duration

	ipLimiter      *ipLimiter
	deviceLimiter  *deviceLimiter
	trustedProxies *trustedProxies
}

func New(s *store.Store) *Server {
	return &Server{
		Store:          s,
		CombineWindow:  defaultCombineWindow,
		pairingTTL:     defaultPairingTTL,
		ipLimiter:      newIPLimiter(ipRateLimit, ipRateBurst),
		deviceLimiter:  newDeviceLimiter(deviceRateLimit, deviceRateBurst),
		trustedProxies: &trustedProxies{},
	}
}

// SetPairingSecret configures the key /api/v1/pair uses to verify pairing
// codes. Must be called before the endpoint is used; see cmd/ai-limit-server,
// which fails closed at startup if the secret isn't configured.
func (s *Server) SetPairingSecret(secret []byte) { s.pairingSecret = secret }

// SetTrustedProxies configures which immediate TCP peers are trusted to
// supply an accurate X-Forwarded-For header (see clientIP). Called with an
// empty/nil list, nothing is trusted and X-Forwarded-For is always ignored
// — the default from New.
func (s *Server) SetTrustedProxies(cidrs []string) error {
	tp, err := parseTrustedProxies(cidrs)
	if err != nil {
		return err
	}
	s.trustedProxies = tp
	return nil
}

// Handler builds the routed, middleware-wrapped http.Handler. Uses the
// standard library's method-aware ServeMux patterns (Go 1.22+) rather than
// pulling in a routing dependency for three routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/v1/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("POST /api/v1/usage", s.requireAuth(s.handleUsage))
	mux.HandleFunc("POST /api/v1/pair", s.rateLimitByIP(s.handlePair))

	return concurrencyLimit(maxConcurrentHandlers)(mux)
}

// NewHTTPServer wraps Handler() in an *http.Server with bounded timeouts
// and header size, matching "reasonable server HTTP timeouts" / "no
// unbounded resource use per connection".
func (s *Server) NewHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
}
