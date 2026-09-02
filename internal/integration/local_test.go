// Package integration holds real, local, cross-package proof that the P3
// pipeline actually works end to end: a real Codex read on the machine
// running this test, over a real HTTP request into a real SQLite file,
// producing a durable event that survives a simulated restart and is then
// delivered to a FakeDelivery.
//
// It is a black-box (external) test package on purpose: it only uses the
// same public surface a real deployment would (internal/provider/codex,
// internal/sink, internal/server/api/store/scheduler/delivery), so it can't
// accidentally pass by reaching into unexported internals no real client
// has access to.
package integration_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/agent"
	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
	"github.com/allfalldownquick/ai-limit-notifier/internal/provider/codex"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/api"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/scheduler"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
	"github.com/allfalldownquick/ai-limit-notifier/internal/sink"
)

// controlledTestThresholdPercent is used only when the real live Codex
// usage at test time is below the 80% default threshold, so the pipeline
// can still be demonstrated without waiting hours/days for real usage to
// cross it naturally. The provider and reset_at stay 100% real either way
// — only used_percent is substituted, and only when necessary.
const controlledTestThresholdPercent = 85.0

func TestLocalRealAgentToServerToSQLiteToFakeDelivery(t *testing.T) {
	ctx := context.Background()

	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snap, err := codex.New().Read(readCtx)
	if err != nil {
		t.Skipf("codex not available on this machine: %v", err)
	}
	window := snap.FiveHour
	if window == nil {
		window = snap.Weekly
	}
	if window == nil {
		t.Skip("codex returned no usable window right now")
	}

	usedPercent := window.UsedPercent
	controlled := usedPercent < domain.DefaultScheduleThreshold
	if controlled {
		usedPercent = controlledTestThresholdPercent
	}

	dbPath := filepath.Join(t.TempDir(), "integration.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	userID, err := st.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const destination = "integration-test-chat"
	if err := st.SetTelegramChatID(ctx, userID, destination); err != nil {
		t.Fatal(err)
	}
	_, rawToken, err := st.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}

	apiServer := api.New(st)
	httpSrv := httptest.NewServer(apiServer.Handler())
	defer httpSrv.Close()

	httpSink, err := sink.New(httpSrv.URL, rawToken)
	if err != nil {
		t.Fatal(err)
	}

	ev := agent.Event{
		Provider:    snap.Provider,
		Window:      window.Kind,
		UsedPercent: usedPercent,
		ResetAt:     window.ResetAt, // real, unmodified provider reset_at
		ObservedAt:  time.Now().UTC(),
	}
	if err := httpSink.Send(ctx, ev); err != nil {
		t.Fatalf("real HTTP submission to the real local server failed: %v", err)
	}

	// "server stop": close the store. Durability lives in the SQLite file,
	// not in this process, so this is the faithful place to draw the line
	// rather than literally killing an OS process.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// "server start": reopen from the same file.
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()

	// The production scheduling rule (send_at = reset_at + 1 minute) is
	// untouched — only the notion of "now" passed to DueEvents/the
	// scheduler is fast-forwarded past that real moment. This is the
	// sanctioned "inject clock, not reset_at" test technique.
	fastForwardedNow := window.ResetAt.Add(2 * time.Minute)

	due, err := st2.DueEvents(ctx, fastForwardedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected exactly one recovered durable event after 'restart', got %d", len(due))
	}
	if due[0].UserID != userID || string(due[0].Provider) != string(snap.Provider) || due[0].WindowKind != string(window.Kind) {
		t.Fatalf("unexpected recovered event: %+v", due[0])
	}

	fake := delivery.NewFakeDelivery()
	sch := scheduler.New(st2, fake)
	sch.Now = func() time.Time { return fastForwardedNow }
	sch.Tick(ctx)

	sent := fake.All()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one delivery after restart recovery, got %d", len(sent))
	}
	if sent[0].Destination != destination {
		t.Fatalf("unexpected destination: %q", sent[0].Destination)
	}

	stillDue, err := st2.DueEvents(ctx, fastForwardedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stillDue) != 0 {
		t.Fatal("a delivered event must not still be due")
	}

	if controlled {
		t.Logf("real Codex %s/%s was %.0f%% (below threshold) at test time; used a controlled %.0f%% to demonstrate the pipeline, with the real observed reset_at (%s)",
			snap.Provider, window.Kind, window.UsedPercent, controlledTestThresholdPercent, window.ResetAt.Format(time.RFC3339))
	} else {
		t.Logf("real Codex %s/%s was %.0f%% (already above threshold) at test time; no controlled substitution needed", snap.Provider, window.Kind, window.UsedPercent)
	}
}
