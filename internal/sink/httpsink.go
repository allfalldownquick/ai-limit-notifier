// Package sink implements the local agent's production delivery
// destination: an internal/agent.Sink that POSTs to the hosted/self-hosted
// server's /api/v1/usage endpoint from docs/PROTOCOL_V1.md.
//
// It sends only the normalized fields already present on internal/agent.
// Event — never Claude/OpenAI credentials, never a raw provider payload,
// never anything read from disk. Retry/backoff across attempts remains
// internal/agent.Core's job (RAM-only, per docs/ARCHITECTURE.md); this type
// performs exactly one HTTP attempt per Send call.
package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/agent"
)

const schemaVersion = 1

// HTTPSink is the production internal/agent.Sink. Construct with New, which
// validates the endpoint requires HTTPS unless it is loopback (so local
// integration testing against a plain-HTTP dev server stays possible
// without weakening the production requirement).
type HTTPSink struct {
	baseURL string
	token   string
	client  *http.Client
}

// New validates endpoint and returns a ready-to-use sink. token is the
// device bearer credential; it is held only in memory and is never logged.
func New(endpoint, token string) (*HTTPSink, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid server endpoint: %w", err)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return nil, errors.New("server endpoint must use https:// (loopback http:// is allowed for local testing only)")
	}
	if token == "" {
		return nil, errors.New("device token is required")
	}
	return &HTTPSink{
		baseURL: strings.TrimSuffix(endpoint, "/"),
		token:   token,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// The bearer token must never leave for a redirect target we
			// didn't choose: net/http only strips Authorization on a
			// cross-host redirect, still forwards it same-host, and a
			// same-host redirect to plain HTTP would leak it on the wire
			// either way. Refuse to follow at all — a 3xx is treated as a
			// failure by Send below — rather than depend on that
			// case-by-case stdlib behavior.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type usageWindowWire struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at"`
}

type usageRequestWire struct {
	SchemaVersion int              `json:"schema_version"`
	Provider      string           `json:"provider"`
	ObservedAt    string           `json:"observed_at"`
	FiveHour      *usageWindowWire `json:"five_hour,omitempty"`
	Weekly        *usageWindowWire `json:"weekly,omitempty"`
}

// buildPayload is exported at the package level (not a method) so a test —
// or a future `show-payload --live-server` — can assert on the exact bytes
// that would leave the machine without needing a real HTTP round trip.
func buildPayload(ev agent.Event) usageRequestWire {
	w := &usageWindowWire{
		UsedPercent: ev.UsedPercent,
		ResetAt:     ev.ResetAt.UTC().Format(time.RFC3339),
	}
	req := usageRequestWire{
		SchemaVersion: schemaVersion,
		Provider:      string(ev.Provider),
		ObservedAt:    ev.ObservedAt.UTC().Format(time.RFC3339),
	}
	switch ev.Window {
	case "weekly":
		req.Weekly = w
	default:
		req.FiveHour = w
	}
	return req
}

type usageResponseWire struct {
	Accepted  bool `json:"accepted"`
	Persisted bool `json:"persisted"`
}

func (s *HTTPSink) Send(ctx context.Context, ev agent.Event) error {
	body, err := json.Marshal(buildPayload(ev))
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/v1/usage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("usage submission failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		// The server's error body is small and sanitized (see
		// internal/server/api's errorResponse); safe to surface as-is.
		return fmt.Errorf("server rejected usage submission: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed usageResponseWire
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("malformed server response: %w", err)
	}
	if !parsed.Accepted || !parsed.Persisted {
		return errors.New("server did not durably acknowledge the submission")
	}
	return nil
}
