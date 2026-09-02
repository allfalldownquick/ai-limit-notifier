package scheduler

import (
	"strings"
	"testing"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
)

// buildMessage must never claim a fact the server hasn't verified: it knows
// send_at (reset_at + 1 minute) has arrived, but it never re-queries the
// provider afterward to confirm the window actually rolled over. These
// tests pin the exact cautious wording so a future edit can't silently
// reintroduce an overclaim like "has reset".
func TestBuildMessageSingleProviderWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "codex", WindowKind: "five_hour"}
	got := buildMessage(ev, nil)
	want := "Your Codex 5-hour usage limit should be available again now."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageWeeklyWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "claude", WindowKind: "weekly"}
	got := buildMessage(ev, nil)
	want := "Your Claude weekly usage limit should be available again now."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageCombinedFiveHourWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "claude", WindowKind: "five_hour"}
	got := buildMessage(ev, []string{"codex"})
	want := "Your Claude and Codex 5-hour usage limits should be available again now."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageCombinedWeeklyWording(t *testing.T) {
	ev := store.NotificationEvent{Provider: "codex", WindowKind: "weekly"}
	got := buildMessage(ev, []string{"claude"})
	want := "Your Codex and Claude weekly usage limits should be available again now."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildMessageNeverOverclaims(t *testing.T) {
	cases := []store.NotificationEvent{
		{Provider: "codex", WindowKind: "five_hour"},
		{Provider: "claude", WindowKind: "weekly"},
	}
	forbidden := []string{"has reset", "was reset", "is reset"}
	for _, ev := range cases {
		for _, covered := range [][]string{nil, {"claude"}, {"codex"}} {
			msg := buildMessage(ev, covered)
			for _, phrase := range forbidden {
				if strings.Contains(strings.ToLower(msg), phrase) {
					t.Fatalf("message %q asserts an unverified fact (%q)", msg, phrase)
				}
			}
		}
	}
}
