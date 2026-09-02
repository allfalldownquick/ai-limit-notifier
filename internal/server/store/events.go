package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type EventStatus string

const (
	EventPending EventStatus = "pending"
	EventSending EventStatus = "sending" // claimed by one deliverer; see ClaimEvent
	EventSent    EventStatus = "sent"
	EventCovered EventStatus = "covered"
)

// staleSendingTimeout bounds how long an event may sit in "sending" before
// it is treated as abandoned (the process that claimed it crashed or was
// killed between the claim and a definitive success/failure) and becomes
// claimable again. It must comfortably exceed how long a real delivery
// attempt can take (HTTP client timeouts in this codebase top out around
// 10s) so a still-in-flight attempt is never mistaken for abandoned.
const staleSendingTimeout = 2 * time.Minute

type NotificationEvent struct {
	ID            string
	UserID        string
	DeviceID      string
	Provider      string
	WindowKind    string
	ResetAt       time.Time
	UsedPercent   float64
	SendAt        time.Time
	Status        EventStatus
	CoveredBy     string
	Attempts      int
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	SentAt        *time.Time
}

// EventInput is the minimal, already-validated data needed to schedule a
// reset notification. It carries no device-supplied text or destination —
// those never exist on this path (see docs/PROTOCOL_V1.md).
type EventInput struct {
	UserID      string
	DeviceID    string
	Provider    string
	WindowKind  string
	ResetAt     time.Time
	UsedPercent float64
}

// UpsertPendingEvent applies the idempotency/combine rules from
// docs/PROTOCOL_V1.md:
//
//   - a retry of the same (user, provider, window, reset_at) is a no-op;
//   - a window whose reset_at already has a sent/covered historical row is
//     never recreated;
//   - a still-pending event for the same (user, provider, window) whose
//     reset_at changed is updated in place (the provider's window rolled
//     over before the old one was sent), not duplicated;
//   - a fresh pending event is combine-linked against any other pending
//     event for the same user/window_kind (different provider) whose
//     reset_at is within combineWindow — the later one becomes `covered`.
//
// Everything happens in one transaction so a crash mid-way never leaves a
// half-applied state.
func (s *Store) UpsertPendingEvent(ctx context.Context, in EventInput, combineWindow time.Duration) (id string, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	now := time.Now().UTC()
	sendAt := in.ResetAt.Add(time.Minute)

	var existingID string
	var existingResetAt int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, reset_at FROM notification_events
		 WHERE user_id = ? AND provider = ? AND window_kind = ? AND status = ?`,
		in.UserID, in.Provider, in.WindowKind, EventPending,
	).Scan(&existingID, &existingResetAt)

	switch {
	case err == nil && existingResetAt == in.ResetAt.Unix():
		// Idempotent retry of the same still-pending window.
		if _, uerr := tx.ExecContext(ctx,
			`UPDATE notification_events SET used_percent = ?, updated_at = ? WHERE id = ?`,
			in.UsedPercent, now.Unix(), existingID,
		); uerr != nil {
			return "", false, uerr
		}
		if cerr := tx.Commit(); cerr != nil {
			return "", false, cerr
		}
		return existingID, false, nil

	case err == nil:
		// Same provider/window, but the authoritative reset_at moved
		// forward before the old one was sent: update in place.
		if _, uerr := tx.ExecContext(ctx,
			`UPDATE notification_events
			 SET reset_at = ?, used_percent = ?, send_at = ?, attempts = 0,
			     next_attempt_at = ?, updated_at = ?
			 WHERE id = ?`,
			in.ResetAt.Unix(), in.UsedPercent, sendAt.Unix(), sendAt.Unix(), now.Unix(), existingID,
		); uerr != nil {
			return "", false, uerr
		}
		id = existingID
		created = false

	case errors.Is(err, sql.ErrNoRows):
		// No pending row for this (user, provider, window_kind). If this
		// exact reset_at was already handled historically, don't recreate.
		var historicalID string
		herr := tx.QueryRowContext(ctx,
			`SELECT id FROM notification_events
			 WHERE user_id = ? AND provider = ? AND window_kind = ? AND reset_at = ?`,
			in.UserID, in.Provider, in.WindowKind, in.ResetAt.Unix(),
		).Scan(&historicalID)
		if herr == nil {
			if cerr := tx.Commit(); cerr != nil {
				return "", false, cerr
			}
			return historicalID, false, nil
		}
		if !errors.Is(herr, sql.ErrNoRows) {
			return "", false, herr
		}

		id = "evt_" + uuid.NewString()
		_, ierr := tx.ExecContext(ctx,
			`INSERT INTO notification_events
			 (id, user_id, device_id, provider, window_kind, reset_at, used_percent,
			  send_at, status, attempts, next_attempt_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
			id, in.UserID, in.DeviceID, in.Provider, in.WindowKind, in.ResetAt.Unix(), in.UsedPercent,
			sendAt.Unix(), EventPending, sendAt.Unix(), now.Unix(), now.Unix(),
		)
		if ierr != nil {
			return "", false, ierr
		}
		created = true

	default:
		return "", false, err
	}

	if err := linkCombinable(ctx, tx, id, in.UserID, in.WindowKind, in.ResetAt, combineWindow, now); err != nil {
		return "", false, err
	}

	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return id, created, nil
}

// linkCombinable covers the later of two same-user, same-window-kind,
// different-provider pending events whose reset_at fall within
// combineWindow of each other. Only five_hour-with-five_hour or
// weekly-with-weekly ever match, because window_kind is part of the query.
func linkCombinable(ctx context.Context, tx *sql.Tx, eventID, userID, windowKind string, resetAt time.Time, combineWindow time.Duration, now time.Time) error {
	lo := resetAt.Add(-combineWindow).Unix()
	hi := resetAt.Add(combineWindow).Unix()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, reset_at, provider, created_at FROM notification_events
		 WHERE user_id = ? AND window_kind = ? AND status = ? AND id != ?
		   AND reset_at BETWEEN ? AND ?`,
		userID, windowKind, EventPending, eventID, lo, hi,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var thisProvider string
	if err := tx.QueryRowContext(ctx, `SELECT provider FROM notification_events WHERE id = ?`, eventID).Scan(&thisProvider); err != nil {
		return err
	}

	type candidate struct {
		id        string
		resetAt   int64
		provider  string
		createdAt int64
	}
	var partner *candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.resetAt, &c.provider, &c.createdAt); err != nil {
			return err
		}
		if c.provider == thisProvider {
			continue // never combine same-provider events with each other
		}
		partner = &c
		break
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if partner == nil {
		return nil
	}

	// The event with the later reset_at (tie-broken by created_at, then id)
	// is the one marked covered; the earlier one carries the notification.
	laterID := eventID
	earlierID := partner.id
	if resetAt.Unix() < partner.resetAt ||
		(resetAt.Unix() == partner.resetAt && now.Unix() < partner.createdAt) {
		laterID, earlierID = earlierID, laterID
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE notification_events SET status = ?, covered_by = ?, updated_at = ? WHERE id = ?`,
		EventCovered, earlierID, now.Unix(), laterID,
	)
	return err
}

// DueEvents returns events ready for a delivery attempt, oldest first:
// pending events whose next_attempt_at has arrived, plus any event stuck in
// "sending" longer than staleSendingTimeout (a claim whose claimer crashed
// or was killed before recording success or failure). SQLite is the source
// of truth for scheduling — this same query is correct whether the process
// just started, is recovering from a restart, or is on a normal tick.
//
// A row returned here is not yet claimed: callers must still call
// ClaimEvent before attempting delivery, since another concurrent
// caller (a second goroutine, or a second server process sharing this
// database file) may be racing to claim the same row.
func (s *Store) DueEvents(ctx context.Context, now time.Time, limit int) ([]NotificationEvent, error) {
	staleCutoff := now.Add(-staleSendingTimeout).Unix()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, device_id, provider, window_kind, reset_at, used_percent,
		        send_at, status, COALESCE(covered_by, ''), attempts, next_attempt_at,
		        created_at, updated_at, sent_at
		 FROM notification_events
		 WHERE (status = ? AND next_attempt_at <= ?)
		    OR (status = ? AND updated_at <= ?)
		 ORDER BY next_attempt_at ASC
		 LIMIT ?`,
		EventPending, now.Unix(), EventSending, staleCutoff, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []NotificationEvent
	for rows.Next() {
		var e NotificationEvent
		var resetAt, sendAtUnix, nextAttempt, createdAt, updatedAt int64
		var sentAt sql.NullInt64
		var status string
		if err := rows.Scan(&e.ID, &e.UserID, &e.DeviceID, &e.Provider, &e.WindowKind, &resetAt, &e.UsedPercent,
			&sendAtUnix, &status, &e.CoveredBy, &e.Attempts, &nextAttempt, &createdAt, &updatedAt, &sentAt); err != nil {
			return nil, err
		}
		e.Status = EventStatus(status)
		e.ResetAt = time.Unix(resetAt, 0).UTC()
		e.SendAt = time.Unix(sendAtUnix, 0).UTC()
		e.NextAttemptAt = time.Unix(nextAttempt, 0).UTC()
		e.CreatedAt = time.Unix(createdAt, 0).UTC()
		e.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if sentAt.Valid {
			t := time.Unix(sentAt.Int64, 0).UTC()
			e.SentAt = &t
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CoveredProviders returns the providers of any events that name eventID as
// their covered_by, so a combined message can name every provider it
// covers.
func (s *Store) CoveredProviders(ctx context.Context, eventID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider FROM notification_events WHERE covered_by = ?`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

// ClaimEvent atomically transitions one due event from "pending" (or a
// stale "sending" — crash recovery) to "sending". claimed is false, with no
// error, when another concurrent caller won the race first — expected and
// harmless: the caller should just skip this event this round, since
// whoever did claim it is responsible for it now.
//
// This is a plain conditional UPDATE, not a SELECT ... FOR UPDATE (SQLite
// has no row locking), but it doesn't need one: SQLite serializes writers
// against one database file — even across separate OS processes sharing it
// — so two concurrent claim attempts for the same row still resolve one
// RowsAffected=1 and one RowsAffected=0, never both succeeding.
func (s *Store) ClaimEvent(ctx context.Context, eventID string, now time.Time) (claimed bool, err error) {
	staleCutoff := now.Add(-staleSendingTimeout).Unix()
	res, err := s.db.ExecContext(ctx,
		`UPDATE notification_events
		 SET status = ?, updated_at = ?
		 WHERE id = ?
		   AND (status = ? OR (status = ? AND updated_at <= ?))`,
		EventSending, now.Unix(), eventID, EventPending, EventSending, staleCutoff,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// MarkSent durably records successful delivery. Called only after the
// delivery transport itself reported success. If the process crashes after
// the transport call succeeds but before this commits, the event is still
// "sending" and staleSendingTimeout later becomes claimable again — a rare
// duplicate send on recovery, which is the documented at-least-once
// tradeoff (see package scheduler's doc comment), not a bug this method
// tries to prevent.
func (s *Store) MarkSent(ctx context.Context, eventID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_events SET status = ?, sent_at = ?, updated_at = ? WHERE id = ?`,
		EventSent, now.Unix(), now.Unix(), eventID,
	)
	return err
}

// RecordAttemptFailure returns a claimed event to "pending" and schedules
// the next attempt (bounded exponential backoff, or honoring a
// transport-specified delay such as Telegram's retry_after), so a later
// tick or a process restart finds and retries it.
func (s *Store) RecordAttemptFailure(ctx context.Context, eventID string, nextAttemptAt time.Time, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_events SET status = ?, attempts = attempts + 1, next_attempt_at = ?, updated_at = ? WHERE id = ?`,
		EventPending, nextAttemptAt.Unix(), now.Unix(), eventID,
	)
	return err
}
