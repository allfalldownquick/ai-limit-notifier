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
	maxConcurrentHandlers = 64

	// Per-device: generous for a monitor process polling every 30s+, tight
	// enough to bound a malfunctioning/hostile client.
	deviceRateLimit = rate.Limit(1) // 1 req/s sustained
	deviceRateBurst = 5

	// Per-IP: mainly a brake on repeated bad-token guessing.
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

	ipLimiter     *ipLimiter
	deviceLimiter *deviceLimiter
}

func New(s *store.Store) *Server {
	return &Server{
		Store:         s,
		CombineWindow: defaultCombineWindow,
		ipLimiter:     newIPLimiter(ipRateLimit, ipRateBurst),
		deviceLimiter: newDeviceLimiter(deviceRateLimit, deviceRateBurst),
	}
}

// Handler builds the routed, middleware-wrapped http.Handler. Uses the
// standard library's method-aware ServeMux patterns (Go 1.22+) rather than
// pulling in a routing dependency for three routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/v1/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("POST /api/v1/usage", s.requireAuth(s.handleUsage))

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
