package store

import (
	"context"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

// migrations is applied in order, each inside its own transaction, and
// recorded in schema_migrations so a restart never re-applies one. Add new
// migrations by appending — never edit an already-shipped one.
var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE users (
	id TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL,
	telegram_chat_id TEXT
);

CREATE TABLE devices (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id),
	token_hash BLOB NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	revoked_at INTEGER
);
CREATE INDEX idx_devices_user ON devices(user_id);

-- One row per (user, provider, window_kind, reset_at): the unique
-- constraint is the idempotency key from docs/PROTOCOL_V1.md ("linked
-- user/device + provider + window kind + reset timestamp"), scoped to the
-- user rather than the device so two devices reporting the same provider
-- never produce two notifications for one real reset window.
CREATE TABLE notification_events (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id),
	device_id TEXT NOT NULL REFERENCES devices(id),
	provider TEXT NOT NULL,
	window_kind TEXT NOT NULL,
	reset_at INTEGER NOT NULL,
	used_percent REAL NOT NULL,
	send_at INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending', -- pending | sent | covered
	covered_by TEXT REFERENCES notification_events(id),
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	sent_at INTEGER,
	UNIQUE(user_id, provider, window_kind, reset_at)
);
CREATE INDEX idx_events_due ON notification_events(status, next_attempt_at);
CREATE INDEX idx_events_combine ON notification_events(user_id, window_kind, status);
`,
	},
	{
		version: 2,
		sql: `
-- The real Telegram identity. A partial unique index (rather than a column
-- constraint) because pre-P4 users created by bootstrap-device have no
-- Telegram identity at all yet, and SQLite's UNIQUE treats every NULL as
-- distinct anyway -- the WHERE clause just makes that explicit.
ALTER TABLE users ADD COLUMN telegram_user_id INTEGER;
CREATE UNIQUE INDEX idx_users_telegram_user_id ON users(telegram_user_id) WHERE telegram_user_id IS NOT NULL;

-- One row per issued pairing code. code_verifier is HMAC-SHA256(pairing
-- secret, normalized code) -- never the plaintext code (see
-- internal/server/auth's pairing code doc comment for why a plain hash
-- isn't enough for a human-enterable, lower-entropy secret). consumed_at
-- doubles as "invalidated": /start replacing a still-valid previous code
-- marks it consumed without ever creating a device for it.
CREATE TABLE pairing_codes (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id),
	code_verifier BLOB NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	consumed_at INTEGER
);
CREATE INDEX idx_pairing_codes_user ON pairing_codes(user_id);

-- Singleton row: the last processed Telegram getUpdates offset, so a
-- restarted bot worker resumes instead of reprocessing (or permanently
-- skipping) updates. Server-side runtime persistence is allowed here --
-- SECURITY.md's "no local runtime writes" guarantee is about the local
-- agent, not this server.
CREATE TABLE telegram_bot_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	last_update_id INTEGER NOT NULL DEFAULT 0
);
INSERT INTO telegram_bot_state (id, last_update_id) VALUES (1, 0);
`,
	},
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}

	for _, m := range migrations {
		var already int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, m.version).Scan(&already); err != nil {
			return err
		}
		if already > 0 {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, unixepoch())`, m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: record: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", m.version, err)
		}
	}
	return nil
}
