package scheduler

import (
	"strings"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

// buildMessage must never claim a fact the server hasn't verified: it knows
// send_at (reset_at + 1 minute) has arrived, but it never re-queries the
// provider afterward to confirm the window actually rolled over. These
// tests pin the exact cautious wording so a future edit can't silently
// reintroduce an overclaim like "сброшен" ("has reset").
func TestBuildMessageSingleProviderWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "codex", WindowKind: "five_hour"}
	got := buildMessage(ev, nil, nil)
	want := "🔵 Кодекс 5 час. доступен"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageWeeklyWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "claude", WindowKind: "weekly"}
	got := buildMessage(ev, nil, nil)
	want := "🟠 Клод Недельный доступен"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageCombinedFiveHourWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "claude", WindowKind: "five_hour"}
	got := buildMessage(ev, []string{"codex"}, nil)
	want := "🟠 Клод и 🔵 Кодекс 5 час. доступны"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageCombinedWeeklyWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "codex", WindowKind: "weekly"}
	got := buildMessage(ev, []string{"claude"}, nil)
	want := "🔵 Кодекс и 🟠 Клод Недельный доступны"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// 2026-09-16 is a Wednesday.
func TestBuildMessageIncludesWeeklyWeekdayHint(t *testing.T) {
	ev := store.NotificationEvent{Provider: "codex", WindowKind: "five_hour"}
	weekly := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	got := buildMessage(ev, nil, &weekly)
	want := "🔵 Кодекс 5 час. доступен (Недельный Ср)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A near-midnight-UTC reset_at must land on the correct weekday in Moscow
// time (UTC+3), not UTC -- e.g. 2026-09-16 22:00 UTC is already Thursday
// 2026-09-17 01:00 in Moscow.
func TestWeekdayRUUsesMoscowTime(t *testing.T) {
	t.Parallel()
	lateUTC := time.Date(2026, 9, 16, 22, 0, 0, 0, time.UTC) // Wed 22:00 UTC = Thu 01:00 MSK
	if got := weekdayRU(lateUTC); got != "Чт" {
		t.Fatalf("got %q, want Чт", got)
	}
}

func TestBuildMessageOmitsWeeklyHintForWeeklyEventItself(t *testing.T) {
	ev := store.NotificationEvent{Provider: "codex", WindowKind: "weekly"}
	weekly := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	got := buildMessage(ev, nil, &weekly)
	if strings.Contains(got, "(") {
		t.Fatalf("weekly notification must not repeat its own weekday hint, got %q", got)
	}
}

func TestBuildMessageNeverOverclaims(t *testing.T) {
	cases := []store.NotificationEvent{
		{Provider: "codex", WindowKind: "five_hour"},
		{Provider: "claude", WindowKind: "weekly"},
	}
	forbidden := []string{"сброшен", "сброшено", "было сброшено"}
	for _, ev := range cases {
		for _, covered := range [][]string{nil, {"claude"}, {"codex"}} {
			msg := buildMessage(ev, covered, nil)
			for _, phrase := range forbidden {
				if strings.Contains(strings.ToLower(msg), phrase) {
					t.Fatalf("message %q asserts an unverified fact (%q)", msg, phrase)
				}
			}
		}
	}
}
