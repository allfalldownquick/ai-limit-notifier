package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
)

const testPairingTTL = 10 * time.Minute

var testPairingSecret = []byte("test-pairing-secret-do-not-use-in-prod")

func mustPairingCode(t *testing.T, s *Store, userID string, now time.Time) (rawCode string, verifier []byte) {
	t.Helper()
	code, err := auth.GeneratePairingCode()
	if err != nil {
		t.Fatal(err)
	}
	normalized := auth.NormalizePairingCode(code)
	verifier = auth.PairingCodeVerifier(testPairingSecret, normalized)
	if _, err := s.CreatePairingCode(context.Background(), userID, verifier, now, testPairingTTL); err != nil {
		t.Fatal(err)
	}
	return code, verifier
}

func TestRedeemPairingCodeSuccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	now := time.Now().UTC()
	_, verifier := mustPairingCode(t, s, userID, now)

	gotUser, deviceID, rawToken, err := s.RedeemPairingCode(ctx, verifier, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if gotUser != userID {
		t.Fatalf("user = %q, want %q", gotUser, userID)
	}
	if deviceID == "" || rawToken == "" {
		t.Fatal("expected a non-empty device id and token")
	}

	d, err := s.AuthenticateDevice(ctx, rawToken)
	if err != nil {
		t.Fatalf("issued token did not authenticate: %v", err)
	}
	if d.ID != deviceID || d.UserID != userID {
		t.Fatalf("unexpected device: %+v", d)
	}
}

func TestRedeemPairingCodeVerifierIsNotPlaintext(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	now := time.Now().UTC()
	rawCode, verifier := mustPairingCode(t, s, userID, now)

	var stored []byte
	if err := s.db.QueryRowContext(ctx, `SELECT code_verifier FROM pairing_codes WHERE user_id = ?`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == rawCode {
		t.Fatal("the stored verifier must never equal the plaintext code")
	}
	if string(stored) != string(verifier) {
		t.Fatal("stored verifier doesn't match the computed one")
	}

	// The raw code (with or without formatting) must not appear anywhere
	// in the row's bytes.
	normalized := auth.NormalizePairingCode(rawCode)
	if containsSubslice(stored, []byte(rawCode)) || containsSubslice(stored, []byte(normalized)) {
		t.Fatal("stored verifier bytes contain the plaintext code")
	}
}

func containsSubslice(haystack, needle []byte) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestRedeemPairingCodeExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	now := time.Now().UTC()
	_, verifier := mustPairingCode(t, s, userID, now)

	_, _, _, err := s.RedeemPairingCode(ctx, verifier, now.Add(testPairingTTL+time.Second))
	if !errors.Is(err, auth.ErrInvalidPairingCode) {
		t.Fatalf("expected ErrInvalidPairingCode for an expired code, got %v", err)
	}
}

func TestRedeemPairingCodeAlreadyConsumed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	now := time.Now().UTC()
	_, verifier := mustPairingCode(t, s, userID, now)

	if _, _, _, err := s.RedeemPairingCode(ctx, verifier, now); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := s.RedeemPairingCode(ctx, verifier, now)
	if !errors.Is(err, auth.ErrInvalidPairingCode) {
		t.Fatalf("expected ErrInvalidPairingCode for a reused code, got %v", err)
	}
}

func TestRedeemPairingCodeUnknown(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fakeVerifier := auth.PairingCodeVerifier(testPairingSecret, "ZZZZZZZZZZ")
	_, _, _, err := s.RedeemPairingCode(ctx, fakeVerifier, time.Now().UTC())
	if !errors.Is(err, auth.ErrInvalidPairingCode) {
		t.Fatalf("expected ErrInvalidPairingCode for an unknown code, got %v", err)
	}
}

func TestRedeemPairingCodeUnknownExpiredConsumedSameError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	now := time.Now().UTC()

	_, expiredVerifier := mustPairingCode(t, s, userID, now.Add(-testPairingTTL-time.Minute))
	_, consumedVerifier := mustPairingCode(t, s, userID, now)
	if _, _, _, err := s.RedeemPairingCode(ctx, consumedVerifier, now); err != nil {
		t.Fatal(err)
	}
	unknownVerifier := auth.PairingCodeVerifier(testPairingSecret, "QQQQQQQQQQ")

	_, _, _, errExpired := s.RedeemPairingCode(ctx, expiredVerifier, now)
	_, _, _, errConsumed := s.RedeemPairingCode(ctx, consumedVerifier, now)
	_, _, _, errUnknown := s.RedeemPairingCode(ctx, unknownVerifier, now)

	for name, err := range map[string]error{"expired": errExpired, "consumed": errConsumed, "unknown": errUnknown} {
		if !errors.Is(err, auth.ErrInvalidPairingCode) {
			t.Fatalf("%s case: got %v, want auth.ErrInvalidPairingCode", name, err)
		}
	}
}

func TestInvalidateActivePairingCodes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	now := time.Now().UTC()
	_, oldVerifier := mustPairingCode(t, s, userID, now)

	if err := s.InvalidateActivePairingCodes(ctx, userID, now); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := s.RedeemPairingCode(ctx, oldVerifier, now)
	if !errors.Is(err, auth.ErrInvalidPairingCode) {
		t.Fatalf("expected the invalidated code to be rejected, got %v", err)
	}
}

func TestRedeemPairingCodeConcurrentSameCodeExactlyOneWinner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pairing-race.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	userID := mustUser(t, s)
	now := time.Now().UTC()
	_, verifier := mustPairingCode(t, s, userID, now)

	const attempts = 20
	type result struct {
		deviceID string
		err      error
	}
	results := make([]result, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, deviceID, _, err := s.RedeemPairingCode(context.Background(), verifier, now)
			results[i] = result{deviceID: deviceID, err: err}
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, r := range results {
		if r.err == nil {
			successes++
		} else if !errors.Is(r.err, auth.ErrInvalidPairingCode) {
			t.Fatalf("unexpected error: %v", r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one winner among %d concurrent redemptions of the same code, got %d", attempts, successes)
	}

	devices, err := s.ListDevicesForUser(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected exactly one device created, got %d", len(devices))
	}
}

func TestFindOrCreateTelegramUserIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id1, err := s.FindOrCreateTelegramUser(ctx, 555, 555)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.FindOrCreateTelegramUser(ctx, 555, 555)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("the same telegram_user_id must resolve to the same server user, got %q and %q", id1, id2)
	}

	chatID, err := s.TelegramChatID(ctx, id1)
	if err != nil {
		t.Fatal(err)
	}
	if chatID != "555" {
		t.Fatalf("chat id = %q, want 555", chatID)
	}
}

func TestFindOrCreateTelegramUserDistinctIdentities(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id1, err := s.FindOrCreateTelegramUser(ctx, 111, 111)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.FindOrCreateTelegramUser(ctx, 222, 222)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatal("different telegram_user_id values must resolve to different server users")
	}
}

func TestListAndRevokeDevicesForUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userA := mustUser(t, s)
	userB := mustUser(t, s)
	devA1 := mustDevice(t, s, userA)
	devA2 := mustDevice(t, s, userA)
	devB := mustDevice(t, s, userB)

	devices, err := s.ListDevicesForUser(ctx, userA)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices for userA, got %d", len(devices))
	}

	if err := s.RevokeDeviceForUser(ctx, userA, devA1); err != nil {
		t.Fatal(err)
	}

	err = s.RevokeDeviceForUser(ctx, userA, devB)
	if !errors.Is(err, ErrDeviceNotOwnedByUser) {
		t.Fatalf("expected ErrDeviceNotOwnedByUser when revoking another user's device, got %v", err)
	}

	// devB must remain unaffected by userA's rejected attempt.
	devicesB, err := s.ListDevicesForUser(ctx, userB)
	if err != nil {
		t.Fatal(err)
	}
	if len(devicesB) != 1 || devicesB[0].RevokedAt != nil {
		t.Fatalf("userB's device must remain active, got %+v", devicesB)
	}

	err = s.RevokeDeviceForUser(ctx, userA, "dev_does-not-exist")
	if !errors.Is(err, ErrDeviceNotOwnedByUser) {
		t.Fatalf("expected ErrDeviceNotOwnedByUser for a nonexistent device, got %v", err)
	}

	devicesA, err := s.ListDevicesForUser(ctx, userA)
	if err != nil {
		t.Fatal(err)
	}
	revokedCount := 0
	for _, d := range devicesA {
		if d.RevokedAt != nil {
			revokedCount++
		}
	}
	if revokedCount != 1 {
		t.Fatalf("expected exactly devA1 revoked among userA's devices, got %d revoked of %+v", revokedCount, devicesA)
	}
	_ = devA2
}
