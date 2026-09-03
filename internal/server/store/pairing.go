package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
)

// CreatePairingCode inserts a new pairing code row for userID. Callers pass
// the already-computed verifier (auth.PairingCodeVerifier) — this package
// never sees the plaintext code.
func (s *Store) CreatePairingCode(ctx context.Context, userID string, verifier []byte, now time.Time, ttl time.Duration) (string, error) {
	id := "pc_" + uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pairing_codes (id, user_id, code_verifier, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, verifier, now.Unix(), now.Add(ttl).Unix(),
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// InvalidateActivePairingCodes marks every still-unconsumed pairing code
// for userID as consumed, without creating a device for any of them. /start
// calls this before issuing a fresh code so at most one code is usable at a
// time.
func (s *Store) InvalidateActivePairingCodes(ctx context.Context, userID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE pairing_codes SET consumed_at = ? WHERE user_id = ? AND consumed_at IS NULL`,
		now.Unix(), userID,
	)
	return err
}

// RedeemPairingCode atomically validates, consumes exactly once, and
// issues a new device credential for a pairing code, all in one
// transaction. Two concurrent redemptions of the same code race on the
// conditional UPDATE below (the same claim pattern as
// internal/server/store's ClaimEvent, proven safe under real concurrency
// in the P3 test suite); exactly one wins.
//
// Every failure — unknown code, expired, already consumed, or lost the
// concurrent race — returns the identical auth.ErrInvalidPairingCode, so a
// caller (an attacker probing codes, in particular) can never distinguish
// them.
func (s *Store) RedeemPairingCode(ctx context.Context, verifier []byte, now time.Time) (userID, deviceID, rawToken string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	var codeID string
	var expiresAt int64
	var consumedAt sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, expires_at, consumed_at FROM pairing_codes WHERE code_verifier = ?`,
		verifier,
	).Scan(&codeID, &userID, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", auth.ErrInvalidPairingCode
	}
	if err != nil {
		return "", "", "", err
	}
	if consumedAt.Valid || now.Unix() > expiresAt {
		return "", "", "", auth.ErrInvalidPairingCode
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE pairing_codes SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
		now.Unix(), codeID,
	)
	if err != nil {
		return "", "", "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", "", "", err
	}
	if n != 1 {
		return "", "", "", auth.ErrInvalidPairingCode // lost a concurrent redemption race
	}

	raw, hash, err := auth.GenerateToken()
	if err != nil {
		return "", "", "", err
	}
	deviceID = "dev_" + uuid.NewString()
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		deviceID, userID, hash, now.Unix(),
	); err != nil {
		return "", "", "", err
	}

	if err := tx.Commit(); err != nil {
		return "", "", "", err
	}
	return userID, deviceID, raw, nil
}
