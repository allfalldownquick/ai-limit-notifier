package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/allfalldownquick/ai-limit-notifier/internal/server/auth"
)

type Device struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// CreateDevice generates a new device credential for userID. rawToken is
// the plaintext token to hand back to the client exactly once; only its
// SHA-256 hash is persisted.
func (s *Store) CreateDevice(ctx context.Context, userID string) (deviceID, rawToken string, err error) {
	raw, hash, err := auth.GenerateToken()
	if err != nil {
		return "", "", err
	}
	id := "dev_" + uuid.NewString()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		id, userID, hash, time.Now().UTC().Unix(),
	)
	if err != nil {
		return "", "", err
	}
	return id, raw, nil
}

// AuthenticateDevice resolves a bearer token to its device, rejecting an
// unknown or revoked token identically (ErrInvalidToken) so a caller can't
// distinguish "never existed" from "revoked" via the response.
func (s *Store) AuthenticateDevice(ctx context.Context, rawToken string) (*Device, error) {
	hash := auth.HashToken(rawToken)

	var d Device
	var storedHash []byte
	var revokedAt sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, revoked_at FROM devices WHERE token_hash = ?`,
		hash,
	).Scan(&d.ID, &d.UserID, &storedHash, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !auth.Equal(storedHash, hash) {
		return nil, auth.ErrInvalidToken // defense in depth; the WHERE clause already matched
	}
	if revokedAt.Valid {
		return nil, auth.ErrInvalidToken
	}
	return &d, nil
}

// RevokeDevice marks a device's credential as no longer valid. It is
// idempotent: revoking an already-revoked device is not an error.
func (s *Store) RevokeDevice(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC().Unix(), deviceID,
	)
	return err
}

// ListDevicesForUser returns every device (active or revoked) belonging to
// userID, newest first. It never returns a token — this package never
// reads token_hash back after CreateDevice/RedeemPairingCode write it.
func (s *Store) ListDevicesForUser(ctx context.Context, userID string) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, created_at, revoked_at FROM devices WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		var createdAt int64
		var revokedAt sql.NullInt64
		if err := rows.Scan(&d.ID, &d.UserID, &createdAt, &revokedAt); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(createdAt, 0).UTC()
		if revokedAt.Valid {
			t := time.Unix(revokedAt.Int64, 0).UTC()
			d.RevokedAt = &t
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ErrDeviceNotOwnedByUser is returned by RevokeDeviceForUser when deviceID
// doesn't belong to userID — including when it doesn't exist at all. The
// two cases are deliberately indistinguishable, so a Telegram user can't
// use /revoke to probe for other users' device ids.
var ErrDeviceNotOwnedByUser = errors.New("device not found for this user")

// RevokeDeviceForUser revokes deviceID only if it belongs to userID.
// Revoking an already-revoked device of your own is still a success
// (idempotent, matching RevokeDevice).
func (s *Store) RevokeDeviceForUser(ctx context.Context, userID, deviceID string) error {
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE id = ?`, deviceID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeviceNotOwnedByUser
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrDeviceNotOwnedByUser
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC().Unix(), deviceID,
	)
	return err
}
