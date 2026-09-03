package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

// observedAtSkew and the reset-horizon bounds below reject "absurd"
// timestamps without depending on domain.UsageWindow.Validate's own
// (looser, client-side-oriented) staleness check alone — the server adds
// its own explicit upper bound too.
const (
	observedAtSkew   = 24 * time.Hour
	resetAtStaleness = -5 * time.Minute
	resetAtHorizon   = 9 * 24 * time.Hour // a bit past the 7-day weekly window, with margin
)

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	device := deviceFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxUsageBodyBytes)

	ct := r.Header.Get("Content-Type")
	if ct != "application/json" && ct != "application/json; charset=utf-8" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "")
		return
	}

	var req usageRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "")
		return
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid_json", "trailing content after JSON body")
		return
	}

	now := time.Now().UTC()

	if req.SchemaVersion != ProtocolVersion {
		writeError(w, http.StatusBadRequest, "unsupported_schema_version", "")
		return
	}

	provider := domain.Provider(req.Provider)
	if provider != domain.ProviderCodex && provider != domain.ProviderClaude {
		writeError(w, http.StatusBadRequest, "unsupported_provider", "")
		return
	}

	observedAt, err := time.Parse(time.RFC3339, req.ObservedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_observed_at", "")
		return
	}
	if observedAt.After(now.Add(observedAtSkew)) || observedAt.Before(now.Add(-observedAtSkew)) {
		writeError(w, http.StatusBadRequest, "implausible_observed_at", "")
		return
	}

	if req.FiveHour == nil && req.Weekly == nil {
		writeError(w, http.StatusBadRequest, "no_usage_window", "")
		return
	}

	snapshot := domain.UsageSnapshot{Provider: provider}
	var parseErr string
	if req.FiveHour != nil {
		snapshot.FiveHour, parseErr = parseWindow(req.FiveHour, domain.WindowFiveHour, now)
		if parseErr != "" {
			writeError(w, http.StatusBadRequest, parseErr, "five_hour")
			return
		}
	}
	if req.Weekly != nil {
		snapshot.Weekly, parseErr = parseWindow(req.Weekly, domain.WindowWeekly, now)
		if parseErr != "" {
			writeError(w, http.StatusBadRequest, parseErr, "weekly")
			return
		}
	}

	if err := snapshot.Validate(now); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_snapshot", "")
		return
	}

	// No server-side percentage gate: an authenticated, schema-valid
	// submission has already crossed the device's own local notification
	// threshold (see cmd/ai-limit-notifier's `config threshold`) before the
	// agent ever sent it. The server's job is auth, validation, durable
	// dedup, and scheduling — not re-deciding whether the percentage
	// "counts", which would just be a second, server-side threshold
	// duplicating client-side config for no benefit.
	for _, w2 := range []*domain.UsageWindow{snapshot.FiveHour, snapshot.Weekly} {
		if w2 == nil {
			continue
		}
		_, _, err := s.Store.UpsertPendingEvent(r.Context(), store.EventInput{
			UserID:      device.UserID,
			DeviceID:    device.ID,
			Provider:    string(snapshot.Provider),
			WindowKind:  string(w2.Kind),
			ResetAt:     w2.ResetAt,
			UsedPercent: w2.UsedPercent,
		}, s.CombineWindow)
		if err != nil {
			// Not durably persisted: do not acknowledge success. The agent
			// keeps its own RAM-only retry state and will resubmit.
			writeError(w, http.StatusInternalServerError, "persistence_failed", "")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(usageResponse{Accepted: true, Persisted: true})
}

// parseWindow converts one wire-format window into the domain type, with
// its own explicit bounds in addition to whatever domain.UsageWindow.Validate
// checks later — belt and suspenders, not a replacement for it.
func parseWindow(w *usageWindowRequest, kind domain.WindowKind, now time.Time) (*domain.UsageWindow, string) {
	if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) || w.UsedPercent < 0 || w.UsedPercent > 100 {
		return nil, "invalid_used_percent"
	}
	resetAt, err := time.Parse(time.RFC3339, w.ResetAt)
	if err != nil {
		return nil, "invalid_reset_at"
	}
	if resetAt.Before(now.Add(resetAtStaleness)) || resetAt.After(now.Add(resetAtHorizon)) {
		return nil, "implausible_reset_at"
	}
	return &domain.UsageWindow{Kind: kind, UsedPercent: w.UsedPercent, ResetAt: resetAt}, ""
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:          "ok",
		ProtocolVersion: ProtocolVersion,
		ServerTime:      time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statusResponse{
		ProtocolVersion: ProtocolVersion,
		DeviceValid:     true, // requireAuth already rejected anything else
		ServerTime:      time.Now().UTC().Format(time.RFC3339),
	})
}
