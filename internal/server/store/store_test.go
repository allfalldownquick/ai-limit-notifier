package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustUser(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.CreateUser(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func mustDevice(t *testing.T, s *Store, userID string) string {
	t.Helper()
	id, _, err := s.CreateDevice(context.Background(), userID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	return id
}

const combineWindow = 10 * time.Minute

func TestDeviceAuthLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)

	deviceID, raw, err := s.CreateDevice(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}

	d, err := s.AuthenticateDevice(ctx, raw)
	if err != nil {
		t.Fatalf("valid token should authenticate: %v", err)
	}
	if d.ID != deviceID || d.UserID != userID {
		t.Fatalf("unexpected device: %+v", d)
	}

	if _, err := s.AuthenticateDevice(ctx, "alnd_not-a-real-token"); err == nil {
		t.Fatal("expected an unknown token to be rejected")
	}

	if err := s.RevokeDevice(ctx, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateDevice(ctx, raw); err == nil {
		t.Fatal("expected a revoked token to be rejected")
	}
}

func TestUpsertPendingEventIdempotentRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	resetAt := time.Now().Add(2 * time.Hour).UTC()

	in := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: resetAt, UsedPercent: 85}

	id1, created1, err := s.UpsertPendingEvent(ctx, in, combineWindow)
	if err != nil || !created1 {
		t.Fatalf("first upsert: id=%v created=%v err=%v", id1, created1, err)
	}
	id2, created2, err := s.UpsertPendingEvent(ctx, in, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("retry of the same snapshot must not create a second event")
	}
	if id1 != id2 {
		t.Fatalf("retry must resolve to the same event id: %s vs %s", id1, id2)
	}

	due, err := s.DueEvents(ctx, resetAt.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected exactly one due event, got %d", len(due))
	}
}

func TestUpsertPendingEventNewResetAtUpdatesInPlace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)

	first := time.Now().Add(2 * time.Hour).UTC()
	second := first.Add(5 * time.Hour).UTC() // provider window rolled over

	in1 := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: first, UsedPercent: 85}
	id1, _, err := s.UpsertPendingEvent(ctx, in1, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	in2 := in1
	in2.ResetAt = second
	in2.UsedPercent = 40
	id2, created2, err := s.UpsertPendingEvent(ctx, in2, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("a still-pending event's reset_at rolling forward must update in place, not create a new row")
	}
	if id1 != id2 {
		t.Fatalf("expected the same event id to be reused, got %s vs %s", id1, id2)
	}

	due, err := s.DueEvents(ctx, second.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ResetAt.Unix() != second.Unix() {
		t.Fatalf("expected one due event at the new reset_at, got %+v", due)
	}
}

func TestUpsertPendingEventDoesNotRecreateAfterSent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	resetAt := time.Now().Add(2 * time.Hour).UTC()

	in := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: resetAt, UsedPercent: 90}
	id, _, err := s.UpsertPendingEvent(ctx, in, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSent(ctx, id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// 80 -> 90 -> 100 style resubmission for the same already-sent window.
	in.UsedPercent = 100
	id2, created2, err := s.UpsertPendingEvent(ctx, in, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("resubmitting a window that was already sent must not create a new pending event")
	}
	if id2 != id {
		t.Fatalf("expected the historical sent event id, got %s vs %s", id2, id)
	}

	due, err := s.DueEvents(ctx, resetAt.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("a sent event must never become due again, got %d", len(due))
	}
}

func TestCombineWithinWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	base := time.Now().Add(2 * time.Hour).UTC()

	codexIn := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: base, UsedPercent: 85}
	claudeIn := EventInput{UserID: userID, DeviceID: deviceID, Provider: "claude", WindowKind: "five_hour", ResetAt: base.Add(5 * time.Minute), UsedPercent: 88}

	codexID, _, err := s.UpsertPendingEvent(ctx, codexIn, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	claudeID, _, err := s.UpsertPendingEvent(ctx, claudeIn, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	due, err := s.DueEvents(ctx, base.Add(10*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected exactly one uncovered event after combining, got %d: %+v", len(due), due)
	}
	if due[0].ID != codexID {
		t.Fatalf("expected the earlier event (codex) to carry the notification, got %s", due[0].ID)
	}

	covered, err := s.CoveredProviders(ctx, codexID)
	if err != nil {
		t.Fatal(err)
	}
	if len(covered) != 1 || covered[0] != "claude" {
		t.Fatalf("expected claude to be recorded as covered by codex's event, got %v", covered)
	}
	_ = claudeID
}

func TestNoCombineOutsideWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	base := time.Now().Add(2 * time.Hour).UTC()

	codexIn := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: base, UsedPercent: 85}
	claudeIn := EventInput{UserID: userID, DeviceID: deviceID, Provider: "claude", WindowKind: "five_hour", ResetAt: base.Add(15 * time.Minute), UsedPercent: 88}

	if _, _, err := s.UpsertPendingEvent(ctx, codexIn, combineWindow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertPendingEvent(ctx, claudeIn, combineWindow); err != nil {
		t.Fatal(err)
	}

	due, err := s.DueEvents(ctx, base.Add(20*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 {
		t.Fatalf("events more than combineWindow apart must stay separate, got %d", len(due))
	}
}

func TestNoCombineAcrossWindowKinds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	base := time.Now().Add(2 * time.Hour).UTC()

	fiveHour := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: base, UsedPercent: 85}
	weekly := EventInput{UserID: userID, DeviceID: deviceID, Provider: "claude", WindowKind: "weekly", ResetAt: base.Add(2 * time.Minute), UsedPercent: 88}

	if _, _, err := s.UpsertPendingEvent(ctx, fiveHour, combineWindow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertPendingEvent(ctx, weekly, combineWindow); err != nil {
		t.Fatal(err)
	}

	due, err := s.DueEvents(ctx, base.Add(10*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 {
		t.Fatalf("five_hour and weekly must never combine even when close in time, got %d", len(due))
	}
}

func TestFiveHourAndWeeklyIndependentPerProvider(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	resetAt := time.Now().Add(2 * time.Hour).UTC()

	fiveHour := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: resetAt, UsedPercent: 85}
	weekly := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "weekly", ResetAt: resetAt.Add(48 * time.Hour), UsedPercent: 30}

	if _, _, err := s.UpsertPendingEvent(ctx, fiveHour, combineWindow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertPendingEvent(ctx, weekly, combineWindow); err != nil {
		t.Fatal(err)
	}

	due, err := s.DueEvents(ctx, resetAt.Add(50*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 {
		t.Fatalf("five_hour and weekly for the same provider must be independent events, got %d", len(due))
	}
}

// holdDistinctConns forces database/sql to actually open n separate
// physical connections (rather than reusing one idle connection
// repeatedly) by acquiring them all before releasing any — the only
// reliable way to prove a per-connection setting reaches every connection,
// not just whichever one happened to run first.
func holdDistinctConns(t *testing.T, s *Store, n int) []*sql.Conn {
	t.Helper()
	ctx := context.Background()
	conns := make([]*sql.Conn, n)
	for i := range conns {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquiring connection %d: %v", i, err)
		}
		conns[i] = c
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})
	return conns
}

func TestEveryPooledConnectionGetsPragmas(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pragma-pool.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	conns := holdDistinctConns(t, s, 5)
	ctx := context.Background()
	for i, c := range conns {
		var fk, busyTimeout int
		var journalMode string
		var synchronous int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if fk != 1 {
			t.Fatalf("connection %d: foreign_keys = %d, want 1", i, fk)
		}
		if busyTimeout != 5000 {
			t.Fatalf("connection %d: busy_timeout = %d, want 5000", i, busyTimeout)
		}
		if journalMode != "wal" {
			t.Fatalf("connection %d: journal_mode = %q, want wal", i, journalMode)
		}
		if synchronous != 2 { // SQLite's numeric code for FULL
			t.Fatalf("connection %d: synchronous = %d, want 2 (FULL)", i, synchronous)
		}
	}
}

func TestForeignKeysEnforcedOnEveryPooledConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fk-pool.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	conns := holdDistinctConns(t, s, 5)
	ctx := context.Background()
	for i, c := range conns {
		_, err := c.ExecContext(ctx,
			`INSERT INTO devices (id, user_id, token_hash, created_at) VALUES (?, 'usr_does_not_exist', X'00', 0)`,
			fmt.Sprintf("dev_fk_probe_%d", i),
		)
		if err == nil {
			t.Fatalf("connection %d allowed a foreign-key-violating insert; foreign_keys is not enforced on every pooled connection", i)
		}
	}
}

func TestSendAtIsExactlyResetAtPlusOneMinute(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	resetAt := time.Now().Add(2 * time.Hour).UTC()

	in := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: resetAt, UsedPercent: 90}
	if _, _, err := s.UpsertPendingEvent(ctx, in, combineWindow); err != nil {
		t.Fatal(err)
	}

	// One second before reset_at+1m: must not be due yet.
	due, err := s.DueEvents(ctx, resetAt.Add(59*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatal("event became due before reset_at + 1 minute")
	}

	// One second after: must be due.
	due, err = s.DueEvents(ctx, resetAt.Add(61*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatal("event was not due at reset_at + 1 minute")
	}
}

func TestCoveredEventNeverResendsAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "covered-restart.db")
	ctx := context.Background()

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	base := time.Now().Add(2 * time.Hour).UTC()

	codexIn := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: base, UsedPercent: 85}
	claudeIn := EventInput{UserID: userID, DeviceID: deviceID, Provider: "claude", WindowKind: "five_hour", ResetAt: base.Add(4 * time.Minute), UsedPercent: 88}
	codexID, _, err := s.UpsertPendingEvent(ctx, codexIn, combineWindow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertPendingEvent(ctx, claudeIn, combineWindow); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSent(ctx, codexID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// "restart": close and reopen from the same file.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	due, err := s2.DueEvents(ctx, base.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("a covered event must never become due, even after restart, got %d", len(due))
	}
}

func TestRecordAttemptFailureDefersRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := mustUser(t, s)
	deviceID := mustDevice(t, s, userID)
	resetAt := time.Now().Add(2 * time.Hour).UTC()

	in := EventInput{UserID: userID, DeviceID: deviceID, Provider: "codex", WindowKind: "five_hour", ResetAt: resetAt, UsedPercent: 85}
	id, _, err := s.UpsertPendingEvent(ctx, in, combineWindow)
	if err != nil {
		t.Fatal(err)
	}

	dueNow := resetAt.Add(2 * time.Minute)
	future := dueNow.Add(time.Hour)
	if err := s.RecordAttemptFailure(ctx, id, future, dueNow); err != nil {
		t.Fatal(err)
	}

	due, err := s.DueEvents(ctx, dueNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("a deferred retry must not be due yet, got %d", len(due))
	}

	due, err = s.DueEvents(ctx, future.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("expected the event due again with attempts=1, got %+v", due)
	}
}
