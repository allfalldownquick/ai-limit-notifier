package claude

import (
	"errors"
	"testing"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
)

func TestParsePayloadBothWindows(t *testing.T) {
	raw := []byte(`{"rate_limits":{"five_hour":{"used_percentage":65,"resets_at":1788388800},"seven_day":{"used_percentage":27,"resets_at":1788850800}}}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Provider != domain.ProviderClaude {
		t.Fatalf("unexpected provider: %v", snap.Provider)
	}
	if snap.FiveHour == nil || snap.FiveHour.UsedPercent != 65 {
		t.Fatalf("unexpected five_hour: %+v", snap.FiveHour)
	}
	if snap.Weekly == nil || snap.Weekly.UsedPercent != 27 {
		t.Fatalf("unexpected weekly: %+v", snap.Weekly)
	}
}

func TestParsePayloadMissingFiveHour(t *testing.T) {
	raw := []byte(`{"rate_limits":{"seven_day":{"used_percentage":27,"resets_at":1788850800}}}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FiveHour != nil {
		t.Fatalf("expected five_hour to be unknown, got %+v", snap.FiveHour)
	}
	if snap.Weekly == nil {
		t.Fatal("expected weekly to still be present")
	}
}

func TestParsePayloadMissingSevenDay(t *testing.T) {
	raw := []byte(`{"rate_limits":{"five_hour":{"used_percentage":65,"resets_at":1788388800}}}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Weekly != nil {
		t.Fatalf("expected weekly to be unknown, got %+v", snap.Weekly)
	}
	if snap.FiveHour == nil {
		t.Fatal("expected five_hour to still be present")
	}
}

func TestParsePayloadInvalidUsedPercentage(t *testing.T) {
	raw := []byte(`{"rate_limits":{"five_hour":{"used_percentage":150,"resets_at":1788388800}}}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FiveHour != nil {
		t.Fatalf("used_percentage > 100 must stay unknown, got %+v", snap.FiveHour)
	}
}

func TestParsePayloadInvalidResetTimestamp(t *testing.T) {
	raw := []byte(`{"rate_limits":{"five_hour":{"used_percentage":65,"resets_at":0}}}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FiveHour != nil {
		t.Fatalf("resets_at <= 0 must stay unknown, got %+v", snap.FiveHour)
	}
}

func TestParsePayloadNoWindowsIsNotAnError(t *testing.T) {
	raw := []byte(`{"rate_limits":{}}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FiveHour != nil || snap.Weekly != nil {
		t.Fatalf("expected both windows unknown, got %+v", snap)
	}
}

func TestParsePayloadMalformedJSON(t *testing.T) {
	_, err := ParsePayload([]byte("not json"))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

func TestParsePayloadNoExtraFieldRetention(t *testing.T) {
	// The real payload also carries workspace/context_window/model/other
	// rate_limits fields; confirm parsing a superset payload only yields the
	// two known windows and nothing else leaks through the typed struct.
	raw := []byte(`{
		"workspace": {"current_dir": "/home/user/secret-project"},
		"context_window": {"used_percentage": 12},
		"model": {"display_name": "some-model"},
		"rate_limits": {
			"five_hour": {"used_percentage": 65, "resets_at": 1788388800},
			"seven_day": {"used_percentage": 27, "resets_at": 1788850800}
		}
	}`)
	snap, err := ParsePayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FiveHour.UsedPercent != 65 || snap.Weekly.UsedPercent != 27 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}
