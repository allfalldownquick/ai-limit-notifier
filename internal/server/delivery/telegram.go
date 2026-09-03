package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultTelegramAPIBase = "https://api.telegram.org"

// TelegramDelivery sends notifications via the Telegram Bot API. It never
// decides the destination or message text — those are handed in by the
// caller (the scheduler, from persisted/templated data), matching
// docs/PROTOCOL_V1.md's "device never picks Telegram text/destination".
//
// It performs exactly one HTTP attempt per Send call; the scheduler's
// durable per-event backoff (internal/server/store's attempts/
// next_attempt_at) is what turns a RetryableError into an eventual retry —
// this type does not loop internally.
type TelegramDelivery struct {
	botToken string
	client   *http.Client
	apiBase  string // overridable in tests; production always uses the default
}

// NewTelegramDelivery builds a transport for the given bot token. The token
// must come from server-side config (env/secret store) — never a literal
// in source, never logged.
func NewTelegramDelivery(botToken string) *TelegramDelivery {
	return &TelegramDelivery{
		botToken: botToken,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Same reasoning as internal/sink's HTTPSink: the bot token is
			// in this request's URL, so a followed redirect could hand it
			// to a host we didn't choose. Refuse to follow; see Send's
			// handling of a non-OK/non-2xx response.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		apiBase: defaultTelegramAPIBase,
	}
}

// SetAPIBase points this delivery at a different Telegram API base URL —
// for tests, so a fake server can stand in without ever calling the real
// API. Production code has no reason to call this.
func (t *TelegramDelivery) SetAPIBase(base string) { t.apiBase = base }

type sendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (t *TelegramDelivery) Send(ctx context.Context, destination, message string) error {
	body, err := json.Marshal(sendMessageRequest{ChatID: destination, Text: message})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, t.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// Never include req/url (carries the bot token) in the wrapped
		// error text that might get logged upstream.
		return &RetryableError{Err: fmt.Errorf("telegram: request failed: %w", err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return &RetryableError{Err: fmt.Errorf("telegram: reading response: %w", err)}
	}

	var tr telegramResponse
	if jerr := json.Unmarshal(raw, &tr); jerr != nil {
		return fmt.Errorf("telegram: malformed response (status %d)", resp.StatusCode)
	}
	if tr.OK {
		return nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		after := time.Second
		if tr.Parameters != nil && tr.Parameters.RetryAfter > 0 {
			after = time.Duration(tr.Parameters.RetryAfter) * time.Second
		}
		return &RetryableError{Err: fmt.Errorf("telegram: rate limited: %s", tr.Description), After: after}
	}
	if resp.StatusCode >= 500 {
		return &RetryableError{Err: fmt.Errorf("telegram: server error %d: %s", resp.StatusCode, tr.Description)}
	}
	return fmt.Errorf("telegram: rejected (%d): %s", resp.StatusCode, tr.Description)
}
