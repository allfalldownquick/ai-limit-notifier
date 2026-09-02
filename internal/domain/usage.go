package domain

import (
	"errors"
	"math"
	"time"
)

type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

type WindowKind string

const (
	WindowFiveHour WindowKind = "five_hour"
	WindowWeekly   WindowKind = "weekly"
)

const DefaultScheduleThreshold = 80.0

type UsageWindow struct {
	Kind        WindowKind `json:"kind"`
	UsedPercent float64    `json:"used_percent"`
	ResetAt     time.Time  `json:"reset_at"`
}

type UsageSnapshot struct {
	Provider Provider     `json:"provider"`
	FiveHour *UsageWindow `json:"five_hour,omitempty"`
	Weekly   *UsageWindow `json:"weekly,omitempty"`
}

func NormalizeCodexLeft(leftPercent float64) float64 {
	return clampPercent(100 - leftPercent)
}

func NormalizeClaudeUsed(usedPercent float64) float64 {
	return clampPercent(usedPercent)
}

func ShouldScheduleReset(usedPercent, threshold float64) bool {
	if threshold <= 0 || threshold > 100 || math.IsNaN(usedPercent) || math.IsInf(usedPercent, 0) {
		return false
	}
	return usedPercent >= threshold
}

func (s UsageSnapshot) Validate(now time.Time) error {
	if s.Provider != ProviderCodex && s.Provider != ProviderClaude {
		return errors.New("unsupported provider")
	}
	if s.FiveHour == nil && s.Weekly == nil {
		return errors.New("at least one usage window is required")
	}
	if s.FiveHour != nil {
		if s.FiveHour.Kind != WindowFiveHour {
			return errors.New("five_hour field contains the wrong window kind")
		}
		if err := s.FiveHour.Validate(now); err != nil {
			return err
		}
	}
	if s.Weekly != nil {
		if s.Weekly.Kind != WindowWeekly {
			return errors.New("weekly field contains the wrong window kind")
		}
		if err := s.Weekly.Validate(now); err != nil {
			return err
		}
	}
	return nil
}

func (w UsageWindow) Validate(now time.Time) error {
	if w.Kind != WindowFiveHour && w.Kind != WindowWeekly {
		return errors.New("unsupported usage window")
	}
	if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) || w.UsedPercent < 0 || w.UsedPercent > 100 {
		return errors.New("used_percent must be between 0 and 100")
	}
	if w.ResetAt.IsZero() {
		return errors.New("reset_at is required")
	}
	// A small amount of clock skew is harmless, but stale windows should never
	// create new server-side notifications.
	if w.ResetAt.Before(now.Add(-5 * time.Minute)) {
		return errors.New("reset_at is stale")
	}
	return nil
}

type WeeklyPace struct {
	ExpectedPercent float64
	DeltaPercent    float64
	Remaining       float64
}

func CalculateWeeklyPace(usedPercent float64, resetAt, now time.Time) WeeklyPace {
	usedPercent = clampPercent(usedPercent)
	windowStart := resetAt.Add(-7 * 24 * time.Hour)
	elapsed := now.Sub(windowStart)
	window := 7 * 24 * time.Hour

	expected := 0.0
	if elapsed > 0 {
		expected = float64(elapsed) / float64(window) * 100
	}
	expected = clampPercent(expected)

	return WeeklyPace{
		ExpectedPercent: expected,
		DeltaPercent:    usedPercent - expected,
		Remaining:       100 - usedPercent,
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
