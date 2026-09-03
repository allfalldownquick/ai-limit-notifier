package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

// maxUsageBodyBytes is generous for docs/PROTOCOL_V1.md's tiny schema and
// still small enough that no client can use the usage endpoint to push
// meaningful amounts of data at the server.
const maxUsageBodyBytes = 8 * 1024

type deviceContextKey struct{}

func deviceFromContext(ctx context.Context) *store.Device {
	d, _ := ctx.Value(deviceContextKey{}).(*store.Device)
	return d
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: code, Detail: detail})
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

const (
	ipLimiterTTL        = 10 * time.Minute // idle entries older than this are purged
	ipLimiterSweepEvery = time.Minute      // how often allow() bothers scanning for stale entries
	ipLimiterMaxEntries = 10_000           // hard cap regardless of TTL/sweep timing
)

type ipLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// ipLimiter throttles by TCP peer address only — never X-Forwarded-For,
// which would require an explicit reverse-proxy trust model this
// deployment does not have (see the P3 deployment plan's trust-boundary
// note). It exists mainly to slow down repeated bad-token guesses.
//
// A public endpoint can be hit from an unbounded number of distinct
// addresses, so the entry map is itself bounded two ways: a TTL sweep
// (opportunistic, on allow()) evicts addresses idle longer than
// ipLimiterTTL, and a hard ipLimiterMaxEntries cap makes allow() fail
// closed for a brand-new address once reached — a burst of distinct
// addresses beyond the cap is itself abuse-shaped, so refusing rather than
// growing unbounded is the safe default.
type ipLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*ipLimiterEntry
	r         rate.Limit
	b         int
	max       int
	lastSweep time.Time
}

func newIPLimiter(r rate.Limit, b int) *ipLimiter {
	return &ipLimiter{
		limiters:  make(map[string]*ipLimiterEntry),
		r:         r,
		b:         b,
		max:       ipLimiterMaxEntries,
		lastSweep: time.Now(),
	}
}

// allow takes an already-resolved client host (no port) — see Server.clientIP,
// which is what every caller uses to get one.
func (l *ipLimiter) allow(host string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > ipLimiterSweepEvery {
		for addr, entry := range l.limiters {
			if now.Sub(entry.lastSeen) > ipLimiterTTL {
				delete(l.limiters, addr)
			}
		}
		l.lastSweep = now
	}

	entry, ok := l.limiters[host]
	if !ok {
		if len(l.limiters) >= l.max {
			return false
		}
		entry = &ipLimiterEntry{limiter: rate.NewLimiter(l.r, l.b)}
		l.limiters[host] = entry
	}
	entry.lastSeen = now
	return entry.limiter.Allow()
}

// size reports the current entry count; used only by tests to assert the
// map stays bounded.
func (l *ipLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.limiters)
}

// deviceLimiter throttles per authenticated device. The set of keys is
// bounded by the number of provisioned devices (never attacker-controlled,
// since a row only exists here after successful auth), so no eviction is
// needed the way ipLimiter needs one.
type deviceLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newDeviceLimiter(r rate.Limit, b int) *deviceLimiter {
	return &deviceLimiter{limiters: make(map[string]*rate.Limiter), r: r, b: b}
}

func (l *deviceLimiter) allow(deviceID string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[deviceID]
	if !ok {
		lim = rate.NewLimiter(l.r, l.b)
		l.limiters[deviceID] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}

// requireAuth resolves the bearer token to a device, applying the
// pre-auth IP limiter and the post-auth per-device limiter. Auth failure
// (missing header, unknown token, revoked token) always returns the same
// generic 401 so a client can't distinguish those cases.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.ipLimiter.allow(s.clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "")
			return
		}

		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}

		device, err := s.Store.AuthenticateDevice(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}

		if !s.deviceLimiter.allow(device.ID) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "")
			return
		}

		ctx := context.WithValue(r.Context(), deviceContextKey{}, device)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// rateLimitByIP applies only the pre-auth IP limiter — for endpoints like
// /api/v1/pair that have no device/bearer token to check yet.
func (s *Server) rateLimitByIP(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.ipLimiter.allow(s.clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// concurrencyLimit bounds in-flight handler goroutines so a burst of
// requests can't spawn unbounded work.
func concurrencyLimit(max int) func(http.Handler) http.Handler {
	sem := make(chan struct{}, max)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusServiceUnavailable, "server_busy", "")
			}
		})
	}
}
