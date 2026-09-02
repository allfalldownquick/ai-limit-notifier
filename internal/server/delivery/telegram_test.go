package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestTelegram points a TelegramDelivery at a local fake server — never
// the real Telegram API.
func newTestTelegram(t *testing.T, handler http.HandlerFunc) *TelegramDelivery {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	td := NewTelegramDelivery("test-token-not-real")
	td.apiBase = srv.URL
	return td
}

func TestTelegramSendSuccess(t *testing.T) {
	var gotBody sendMessageRequest
	td := newTestTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(telegramResponse{OK: true})
	})

	if err := td.Send(context.Background(), "12345", "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody.ChatID != "12345" || gotBody.Text != "hello" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
}

func TestTelegramHonorsRetryAfter(t *testing.T) {
	td := newTestTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"description": "Too Many Requests",
			"parameters":  map[string]any{"retry_after": 7},
		})
	})

	err := td.Send(context.Background(), "1", "hi")
	var re *RetryableError
	if err == nil {
		t.Fatal("expected an error")
	}
	if !asRetryable(err, &re) {
		t.Fatalf("expected a RetryableError, got %T: %v", err, err)
	}
	if re.After != 7*time.Second {
		t.Fatalf("After = %v, want 7s", re.After)
	}
}

func TestTelegramServerErrorIsRetryable(t *testing.T) {
	td := newTestTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(telegramResponse{OK: false, Description: "boom"})
	})

	var re *RetryableError
	if err := td.Send(context.Background(), "1", "hi"); !asRetryable(err, &re) {
		t.Fatalf("expected a RetryableError for a 5xx, got %v", err)
	}
}

func TestTelegramBadRequestIsPermanent(t *testing.T) {
	td := newTestTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(telegramResponse{OK: false, Description: "chat not found"})
	})

	var re *RetryableError
	err := td.Send(context.Background(), "1", "hi")
	if err == nil {
		t.Fatal("expected an error")
	}
	if asRetryable(err, &re) {
		t.Fatal("a 400 must not be treated as retryable")
	}
}

func TestTelegramMalformedResponse(t *testing.T) {
	td := newTestTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if err := td.Send(context.Background(), "1", "hi"); err == nil {
		t.Fatal("expected an error for a malformed response")
	}
}

func asRetryable(err error, target **RetryableError) bool {
	re, ok := err.(*RetryableError)
	if ok {
		*target = re
	}
	return ok
}
