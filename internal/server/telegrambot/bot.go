// Package telegrambot implements the P4 onboarding bot: /start issues a
// pairing code, /devices lists a user's own linked devices, /revoke lets
// them disconnect one. It only ever handles private-chat updates — groups,
// supergroups, and channels are ignored — and makes no AI/model calls.
//
// Long polling (getUpdates) rather than a webhook: it needs no new public
// HTTPS endpoint or certificate, which matters for a first beta that
// hasn't touched the reverse proxy yet. The tradeoff is one long-lived
// outbound connection per poll instead of Telegram pushing to us; at this
// project's expected message volume that cost is immaterial.
package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

const (
	defaultAPIBase     = "https://api.telegram.org"
	defaultPollTimeout = 30 * time.Second
	defaultPairingTTL  = 10 * time.Minute
	minBackoff         = time.Second
	maxBackoff         = 5 * time.Minute
	getUpdatesMaxBytes = 1 << 20 // one poll batch of updates; generous but bounded
)

// Bot runs the long-polling loop and command handlers. Construct with New;
// apiBase is overridable only for tests (see WithAPIBase) — production
// always uses the real Telegram API.
type Bot struct {
	Store         *store.Store
	Delivery      *delivery.TelegramDelivery // reused for every outbound message, including command replies
	PairingSecret []byte
	PairingTTL    time.Duration
	PollTimeout   time.Duration

	botToken string
	apiBase  string
	client   *http.Client
}

func New(botToken string, st *store.Store, pairingSecret []byte) *Bot {
	return &Bot{
		Store:         st,
		Delivery:      delivery.NewTelegramDelivery(botToken),
		PairingSecret: pairingSecret,
		PairingTTL:    defaultPairingTTL,
		PollTimeout:   defaultPollTimeout,
		botToken:      botToken,
		apiBase:       defaultAPIBase,
		client: &http.Client{
			// Comfortably longer than PollTimeout, which is a server-side
			// long-poll duration Telegram itself respects.
			Timeout: defaultPollTimeout + 10*time.Second,
			// Same reasoning as internal/sink and internal/server/delivery:
			// the bot token is in this request's URL.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// WithAPIBase points the bot (both getUpdates and outbound message
// delivery) at a different Telegram API base URL — tests only, so a fake
// server can stand in without ever calling the real API.
func (b *Bot) WithAPIBase(base string) *Bot {
	b.apiBase = base
	b.Delivery.SetAPIBase(base)
	return b
}

type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	From *fromUser `json:"from"`
	Chat chat      `json:"chat"`
	Text string    `json:"text"`
}

type fromUser struct {
	ID int64 `json:"id"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type getUpdatesResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
	Result []update `json:"result"`
}

// Run polls for updates until ctx is cancelled. It resumes from the
// durably-stored offset (internal/server/store's telegram_bot_state), so a
// restart neither reprocesses nor permanently skips updates.
func (b *Bot) Run(ctx context.Context) {
	offset, err := b.Store.LoadTelegramBotOffset(ctx)
	if err != nil {
		offset = 0
	}

	backoff := minBackoff
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := backoff
			var re *delivery.RetryableError
			if errors.As(err, &re) && re.After > 0 {
				wait = re.After
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = minBackoff

		for _, u := range updates {
			b.handleUpdate(ctx, u)
			offset = u.UpdateID + 1
			_ = b.Store.SaveTelegramBotOffset(ctx, offset) // best effort; see handleUpdate's idempotency note
		}
	}
}

type getUpdatesRequest struct {
	Offset         int64    `json:"offset"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	body, err := json.Marshal(getUpdatesRequest{
		Offset:         offset,
		Timeout:        int(b.PollTimeout.Seconds()),
		AllowedUpdates: []string{"message"},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/bot%s/getUpdates", b.apiBase, b.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, &delivery.RetryableError{Err: fmt.Errorf("telegram getUpdates: request failed: %w", err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, getUpdatesMaxBytes))
	if err != nil {
		return nil, &delivery.RetryableError{Err: fmt.Errorf("telegram getUpdates: reading response: %w", err)}
	}

	var gur getUpdatesResponse
	if jerr := json.Unmarshal(raw, &gur); jerr != nil {
		return nil, fmt.Errorf("telegram getUpdates: malformed response (status %d)", resp.StatusCode)
	}
	if gur.OK {
		return gur.Result, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		after := time.Second
		if gur.Parameters != nil && gur.Parameters.RetryAfter > 0 {
			after = time.Duration(gur.Parameters.RetryAfter) * time.Second
		}
		return nil, &delivery.RetryableError{Err: fmt.Errorf("telegram getUpdates: rate limited: %s", gur.Description), After: after}
	}
	if resp.StatusCode >= 500 {
		return nil, &delivery.RetryableError{Err: fmt.Errorf("telegram getUpdates: server error %d: %s", resp.StatusCode, gur.Description)}
	}
	return nil, fmt.Errorf("telegram getUpdates: rejected (%d): %s", resp.StatusCode, gur.Description)
}

// handleUpdate dispatches one update. Every handler below is written so
// that reprocessing the same update after a restart (if SaveTelegramBotOffset
// didn't reach disk before a crash) is harmless: /start just issues another
// code, /devices just re-lists, /revoke on an already-revoked device is a
// no-op.
func (b *Bot) handleUpdate(ctx context.Context, u update) {
	if u.Message == nil || u.Message.From == nil {
		return
	}
	if u.Message.Chat.Type != "private" {
		return // groups/supergroups/channels are out of scope for P4
	}

	text := strings.TrimSpace(u.Message.Text)
	telegramUserID := u.Message.From.ID
	chatID := u.Message.Chat.ID

	switch {
	case text == "/start" || strings.HasPrefix(text, "/start "):
		b.handleStart(ctx, telegramUserID, chatID)
	case text == "/devices":
		b.handleDevices(ctx, telegramUserID, chatID)
	case text == "/revoke" || strings.HasPrefix(text, "/revoke "):
		b.handleRevoke(ctx, telegramUserID, chatID, text)
	default:
		// Unknown command or plain text: silently ignored. No AI/model
		// call, no echo — P4's UX surface is exactly these three commands.
	}
}

func (b *Bot) handleStart(ctx context.Context, telegramUserID, chatID int64) {
	userID, err := b.Store.FindOrCreateTelegramUser(ctx, telegramUserID, chatID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if err := b.Store.InvalidateActivePairingCodes(ctx, userID, now); err != nil {
		return
	}

	code, err := auth.GeneratePairingCode()
	if err != nil {
		return
	}
	verifier := auth.PairingCodeVerifier(b.PairingSecret, auth.NormalizePairingCode(code))
	if _, err := b.Store.CreatePairingCode(ctx, userID, verifier, now, b.PairingTTL); err != nil {
		return
	}

	msg := fmt.Sprintf(
		"AI Limit Notifier\n\nConnection code:\n%s\n\nOn your computer, run:\nai-limit-notifier link %s\n\nThe code is valid for %d minutes and works once.",
		code, code, int(b.PairingTTL.Minutes()),
	)
	_ = b.Delivery.Send(ctx, fmt.Sprint(chatID), msg)
}

func (b *Bot) handleDevices(ctx context.Context, telegramUserID, chatID int64) {
	userID, err := b.Store.FindOrCreateTelegramUser(ctx, telegramUserID, chatID)
	if err != nil {
		return
	}
	devices, err := b.Store.ListDevicesForUser(ctx, userID)
	if err != nil {
		return
	}
	if len(devices) == 0 {
		_ = b.Delivery.Send(ctx, fmt.Sprint(chatID), "No linked devices yet. Send /start to connect one.")
		return
	}

	var sb strings.Builder
	sb.WriteString("Linked devices:\n\n")
	for _, d := range devices {
		status := "active"
		if d.RevokedAt != nil {
			status = "revoked"
		}
		fmt.Fprintf(&sb, "%s — %s, linked %s\n", d.ID, status, d.CreatedAt.Format("2006-01-02"))
	}
	sb.WriteString("\nTo remove a device: /revoke <device-id>")
	_ = b.Delivery.Send(ctx, fmt.Sprint(chatID), sb.String())
}

func (b *Bot) handleRevoke(ctx context.Context, telegramUserID, chatID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		_ = b.Delivery.Send(ctx, fmt.Sprint(chatID), "Usage: /revoke <device-id> (see /devices)")
		return
	}
	deviceID := fields[1]

	userID, err := b.Store.FindOrCreateTelegramUser(ctx, telegramUserID, chatID)
	if err != nil {
		return
	}
	err = b.Store.RevokeDeviceForUser(ctx, userID, deviceID)
	if errors.Is(err, store.ErrDeviceNotOwnedByUser) {
		_ = b.Delivery.Send(ctx, fmt.Sprint(chatID), "Device not found.")
		return
	}
	if err != nil {
		return
	}
	_ = b.Delivery.Send(ctx, fmt.Sprint(chatID), "Device revoked.")
}
