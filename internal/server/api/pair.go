package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
)

const (
	maxPairBodyBytes    = 2 * 1024
	maxClientVersionLen = 32
)

// platformPattern is a strict bounded identifier rather than a fixed
// enum: docs/PROJECT_STATUS.md's supported-platform list is still growing
// (P7 native Windows, macOS), and a hard enum here would need a server
// change every time a new one ships. Lowercase alnum plus '-'/'_', 1-40
// chars, matching the shape of the "linux-wsl-amd64" style identifiers
// already used elsewhere in the docs.
var platformPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// handlePair implements POST /api/v1/pair from docs/PROTOCOL_V1.md.
// Unauthenticated by nature (this is how a device gets its first
// credential) — protected instead by the pairing code's own entropy/TTL/
// one-use guarantees plus the per-IP rate limiter applied in Handler().
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if len(s.pairingSecret) == 0 {
		// Fail closed: never accept a pairing attempt without a configured
		// secret to verify it against.
		writeError(w, http.StatusServiceUnavailable, "pairing_unavailable", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPairBodyBytes)

	ct := r.Header.Get("Content-Type")
	if ct != "application/json" && ct != "application/json; charset=utf-8" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "")
		return
	}

	var req pairRequest
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

	if req.ClientVersion == "" || len(req.ClientVersion) > maxClientVersionLen {
		writeError(w, http.StatusBadRequest, "invalid_client_version", "")
		return
	}
	if !platformPattern.MatchString(req.Platform) {
		writeError(w, http.StatusBadRequest, "invalid_platform", "")
		return
	}

	normalized := auth.NormalizePairingCode(req.Code)
	if !auth.ValidatePairingCodeFormat(normalized) {
		// Same error the DB-backed rejection below uses: a malformed code
		// and a well-formed-but-wrong one must look identical externally.
		writeError(w, http.StatusBadRequest, "invalid_code", "")
		return
	}
	verifier := auth.PairingCodeVerifier(s.pairingSecret, normalized)

	_, deviceID, rawToken, err := s.Store.RedeemPairingCode(r.Context(), verifier, time.Now().UTC())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidPairingCode) {
			// Unknown, expired, consumed, and "lost a concurrent redemption
			// race" are all indistinguishable here on purpose — see
			// Store.RedeemPairingCode's doc comment.
			writeError(w, http.StatusBadRequest, "invalid_code", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "pairing_failed", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(pairResponse{Linked: true, DeviceID: deviceID, DeviceToken: rawToken})
}
