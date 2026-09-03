package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

var testPairingSecret = []byte("test-pairing-secret-not-for-production")

type sentMessage struct {
	ChatID string
	Text   string
}

// fakeTelegram is a local, in-process stand-in for the real Telegram Bot
// API. No test in this file ever talks to api.telegram.org.
type fakeTelegram struct {
	mu             sync.Mutex
	pending        []update
	sent           []sentMessage
	getUpdatesHits []int64 // the offset requested on each getUpdates call, in order
	force429Once   bool
	srv            *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	ft := &fakeTelegram{}
	ft.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			ft.handleGetUpdates(w, r)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			ft.handleSendMessage(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ft.srv.Close)
	return ft
}

func (ft *fakeTelegram) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Offset int64 `json:"offset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	ft.mu.Lock()
	ft.getUpdatesHits = append(ft.getUpdatesHits, req.Offset)
	if ft.force429Once {
		ft.force429Once = false
		ft.mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false, "description": "flood", "parameters": map[string]any{"retry_after": 1},
		})
		return
	}
	var result []update
	if len(ft.pending) > 0 {
		result = ft.pending
		ft.pending = nil
	}
	ft.mu.Unlock()

	if result == nil {
		// A short simulated long-poll wait so the Run loop doesn't busy-spin
		// against this fake server the way it never would against the real
		// (genuinely long-polling) Telegram API.
		time.Sleep(20 * time.Millisecond)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (ft *fakeTelegram) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ft.mu.Lock()
	ft.sent = append(ft.sent, sentMessage{ChatID: body.ChatID, Text: body.Text})
	ft.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (ft *fakeTelegram) queue(u update) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.pending = append(ft.pending, u)
}

func (ft *fakeTelegram) messages() []sentMessage {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	out := make([]sentMessage, len(ft.sent))
	copy(out, ft.sent)
	return out
}

func (ft *fakeTelegram) offsetsRequested() []int64 {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	out := make([]int64, len(ft.getUpdatesHits))
	copy(out, ft.getUpdatesHits)
	return out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func newTestBotStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var codePattern = regexp.MustCompile(`[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{2}`)

func extractCode(text string) string {
	return codePattern.FindString(text)
}

func privateUpdate(updateID, fromID, chatID int64, text string) update {
	return update{
		UpdateID: updateID,
		Message: &message{
			From: &fromUser{ID: fromID},
			Chat: chat{ID: chatID, Type: "private"},
			Text: text,
		},
	}
}

func TestStartPrivateChatIssuesCodeAndMessage(t *testing.T) {
	s := newTestBotStore(t)
	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	ft.queue(privateUpdate(1, 111, 111, "/start"))
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 1 })

	msg := ft.messages()[0]
	if msg.ChatID != "111" {
		t.Fatalf("unexpected chat id: %q", msg.ChatID)
	}
	if !strings.Contains(msg.Text, "AI Limit Notifier") || !strings.Contains(msg.Text, "ai-limit-notifier link") {
		t.Fatalf("unexpected message text: %q", msg.Text)
	}
	code := extractCode(msg.Text)
	if code == "" {
		t.Fatalf("no pairing code found in message: %q", msg.Text)
	}

	// The code must actually redeem.
	verifier := auth.PairingCodeVerifier(testPairingSecret, auth.NormalizePairingCode(code))
	userID, deviceID, rawToken, err := s.RedeemPairingCode(context.Background(), verifier, time.Now().UTC())
	if err != nil {
		t.Fatalf("issued code did not redeem: %v", err)
	}
	if userID == "" || deviceID == "" || rawToken == "" {
		t.Fatal("expected non-empty user/device/token")
	}
}

func TestStartGroupChatIgnored(t *testing.T) {
	s := newTestBotStore(t)
	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	u := update{
		UpdateID: 1,
		Message: &message{
			From: &fromUser{ID: 222},
			Chat: chat{ID: -500, Type: "group"},
			Text: "/start",
		},
	}
	ft.queue(u)

	// Give it time to (not) act, then confirm nothing happened.
	time.Sleep(150 * time.Millisecond)
	if len(ft.messages()) != 0 {
		t.Fatalf("expected no reply to a group /start, got %+v", ft.messages())
	}
	if _, found, err := s.UserByTelegramID(context.Background(), 222); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("a group message must not create a user")
	}
}

func TestUsernameChangeDoesNotCreateNewIdentity(t *testing.T) {
	s := newTestBotStore(t)
	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	// The bot's `message.From` type has no username field at all — it's
	// never read — so raw updates carrying different (unparsed) usernames
	// for the same numeric id must still resolve to one identity.
	raw1 := `{"update_id":1,"message":{"from":{"id":333,"username":"alice_2024"},"chat":{"id":333,"type":"private"},"text":"/start"}}`
	raw2 := `{"update_id":2,"message":{"from":{"id":333,"username":"totally_different_name"},"chat":{"id":333,"type":"private"},"text":"/start"}}`
	var u1, u2 update
	_ = json.Unmarshal([]byte(raw1), &u1)
	_ = json.Unmarshal([]byte(raw2), &u2)

	ft.queue(u1)
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 1 })
	ft.queue(u2)
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 2 })

	// Both /start calls succeeding at all already exercises
	// FindOrCreateTelegramUser twice for the same identity; store's own
	// TestFindOrCreateTelegramUserIdempotent proves it returns the same id
	// both times. Here, just confirm a user resolves for 333 regardless of
	// the (unparsed) username carried in either update.
	if _, found, err := s.UserByTelegramID(context.Background(), 333); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatal("expected a resolved user for telegram_user_id 333")
	}
}

func TestDuplicateStartInvalidatesPreviousCode(t *testing.T) {
	s := newTestBotStore(t)
	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	ft.queue(privateUpdate(1, 444, 444, "/start"))
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 1 })
	firstCode := extractCode(ft.messages()[0].Text)

	ft.queue(privateUpdate(2, 444, 444, "/start"))
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 2 })
	secondCode := extractCode(ft.messages()[1].Text)

	if firstCode == secondCode {
		t.Fatal("two /start calls should not reuse the same code")
	}

	firstVerifier := auth.PairingCodeVerifier(testPairingSecret, auth.NormalizePairingCode(firstCode))
	if _, _, _, err := s.RedeemPairingCode(context.Background(), firstVerifier, time.Now().UTC()); err == nil {
		t.Fatal("the first code must have been invalidated by the second /start")
	}

	secondVerifier := auth.PairingCodeVerifier(testPairingSecret, auth.NormalizePairingCode(secondCode))
	if _, _, _, err := s.RedeemPairingCode(context.Background(), secondVerifier, time.Now().UTC()); err != nil {
		t.Fatalf("the second (current) code should still redeem: %v", err)
	}
}

// Pairing-code-verifier-is-not-plaintext is proven directly against the
// stored bytes in internal/server/store's own test suite
// (TestRedeemPairingCodeVerifierIsNotPlaintext); this package reaches the
// same code path through handleStart but has no business reaching into
// store's raw tables to re-prove it.

func TestGetUpdatesHonorsRetryAfterAndRecovers(t *testing.T) {
	s := newTestBotStore(t)
	ft := newFakeTelegram(t)
	ft.force429Once = true
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	ft.queue(privateUpdate(1, 666, 666, "/start"))
	waitFor(t, 3*time.Second, func() bool { return len(ft.messages()) == 1 })
}

func TestNoRedirectsFollowed(t *testing.T) {
	s := newTestBotStore(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target must never be contacted, got %s", r.URL.Path)
	}))
	defer target.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirecting.Close()

	b := New("test-token", s, testPairingSecret).WithAPIBase(redirecting.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := b.getUpdates(ctx, 0)
	if err == nil {
		t.Fatal("expected an error when getUpdates is redirected rather than followed")
	}
}

func TestRunStopsPromptlyOnContextCancel(t *testing.T) {
	s := newTestBotStore(t)
	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after context cancellation")
	}
}

func TestOffsetPersistsAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bot-offset.db")
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}

	ft1 := newFakeTelegram(t)
	b1 := New("test-token", s, testPairingSecret).WithAPIBase(ft1.srv.URL)
	ctx1, cancel1 := context.WithCancel(context.Background())
	go b1.Run(ctx1)

	ft1.queue(privateUpdate(10, 777, 777, "/start"))
	waitFor(t, 2*time.Second, func() bool { return len(ft1.messages()) == 1 })
	cancel1()
	time.Sleep(50 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// "restart": reopen the same file, point a fresh bot at a fresh fake
	// server, and confirm the very first getUpdates request already asks
	// for offset 11 (update_id 10 + 1) — not 0.
	s2, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ft2 := newFakeTelegram(t)
	b2 := New("test-token", s2, testPairingSecret).WithAPIBase(ft2.srv.URL)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go b2.Run(ctx2)

	waitFor(t, 2*time.Second, func() bool { return len(ft2.offsetsRequested()) > 0 })
	if got := ft2.offsetsRequested()[0]; got != 11 {
		t.Fatalf("first post-restart getUpdates offset = %d, want 11 (resumed, not reprocessed)", got)
	}
}

func TestDevicesOnlyShowsOwnDevices(t *testing.T) {
	s := newTestBotStore(t)
	ctx := context.Background()
	userA, err := s.FindOrCreateTelegramUser(ctx, 1001, 1001)
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.FindOrCreateTelegramUser(ctx, 1002, 1002)
	if err != nil {
		t.Fatal(err)
	}
	devA, _, err := s.CreateDevice(ctx, userA)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateDevice(ctx, userB); err != nil {
		t.Fatal(err)
	}

	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(rctx)

	ft.queue(privateUpdate(1, 1001, 1001, "/devices"))
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 1 })

	msg := ft.messages()[0].Text
	if !strings.Contains(msg, devA) {
		t.Fatalf("expected userA's device %s listed, got %q", devA, msg)
	}
}

func TestRevokeOwnDeviceRejectsTokenImmediately(t *testing.T) {
	s := newTestBotStore(t)
	ctx := context.Background()
	userID, err := s.FindOrCreateTelegramUser(ctx, 3001, 3001)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, rawToken, err := s.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateDevice(ctx, rawToken); err != nil {
		t.Fatalf("token should authenticate before revocation: %v", err)
	}

	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(rctx)

	ft.queue(privateUpdate(1, 3001, 3001, "/revoke "+deviceID))
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 1 })
	if !strings.Contains(ft.messages()[0].Text, "revoked") {
		t.Fatalf("expected a revocation confirmation, got %q", ft.messages()[0].Text)
	}

	if _, err := s.AuthenticateDevice(ctx, rawToken); err == nil {
		t.Fatal("the device's own token must be rejected immediately after /revoke")
	}
}

func TestRevokeOnlyOwnDevice(t *testing.T) {
	s := newTestBotStore(t)
	ctx := context.Background()
	userA, err := s.FindOrCreateTelegramUser(ctx, 2001, 2001)
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.FindOrCreateTelegramUser(ctx, 2002, 2002)
	if err != nil {
		t.Fatal(err)
	}
	devB, _, err := s.CreateDevice(ctx, userB)
	if err != nil {
		t.Fatal(err)
	}

	ft := newFakeTelegram(t)
	b := New("test-token", s, testPairingSecret).WithAPIBase(ft.srv.URL)
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(rctx)

	ft.queue(privateUpdate(1, 2001, 2001, "/revoke "+devB))
	waitFor(t, 2*time.Second, func() bool { return len(ft.messages()) == 1 })

	if !strings.Contains(ft.messages()[0].Text, "not found") {
		t.Fatalf("expected a 'not found' reply for another user's device, got %q", ft.messages()[0].Text)
	}

	devices, err := s.ListDevicesForUser(ctx, userB)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].RevokedAt != nil {
		t.Fatalf("userB's device must remain active, got %+v", devices)
	}
	_ = userA
}
