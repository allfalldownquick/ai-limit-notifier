// Package store is the server's only persistence layer: SQLite, accessed
// exclusively through parameterized queries (never string-built SQL), with
// a small versioned migration runner. This is durable state for the
// hosted/self-hosted server — distinct from, and not in tension with, the
// local agent's "zero runtime writes" guarantee, which applies only to the
// device side (see SECURITY.md).
//
// # Durability policy
//
// The server acknowledges a usage submission ({"persisted": true}) only
// after the write commits, and that acknowledgement is only as honest as
// what actually reached stable storage. Connections use WAL journaling with
// PRAGMA synchronous=FULL: every commit is fsync'd before it returns,
// trading some write latency for surviving an OS crash or power loss
// without corruption or losing the just-acknowledged write. At v0.1's
// expected request volume (a handful of usage submissions per user per
// day) that latency is immaterial; NORMAL (WAL's usual recommended
// default, fsync-per-checkpoint rather than per-commit) would be the right
// tradeoff at a request volume large enough for the extra fsyncs to matter,
// which this project is not at yet.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite connection pool configured for durable, single-
// process server use: every physical connection in the pool — not just
// whichever one happens to run first — gets foreign keys enforced and a
// busy timeout, via modernc.org/sqlite's per-connection DSN pragmas rather
// than a one-shot Exec (PRAGMAs are connection-scoped state in SQLite; a
// db.Exec on a pooled *sql.DB only reaches whichever single connection the
// pool happened to hand it, not every connection the pool later opens).
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and
// applies any pending migrations. path may be ":memory:" for tests; each
// connection to a bare ":memory:" DSN is otherwise its own independent
// empty database, so the pool is capped at one connection for it — never
// rely on more than one for an in-memory Store.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		// SQLite serializes writers regardless; this just bounds
		// concurrent readers and matches "bounded concurrent handlers".
		db.SetMaxOpenConns(8)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// dsn builds the modernc.org/sqlite connection string so that every
// connection the pool opens — now or later, as it grows — carries the same
// pragmas. See Driver.Open's docs for the exact query-parameter contract.
func dsn(path string) string {
	params := url.Values{
		"_foreign_keys": {"1"},
		"_busy_timeout": {"5000"},
		// Every read-modify-write transaction in this package (see
		// pairing.go, events.go) reads before it writes. SQLite's default
		// "deferred" transaction only takes a read lock at BEGIN and tries
		// to upgrade to a write lock on the first write statement; if
		// another connection committed in between, that upgrade fails with
		// an immediate SQLITE_BUSY (a stale-snapshot conflict, not a
		// contended lock), which _busy_timeout's retry-and-wait does not
		// cover. "immediate" takes the write lock up front, so concurrent
		// transactions serialize through the busy handler instead.
		"_txlock": {"immediate"},
	}
	if path == ":memory:" {
		return path + "?" + params.Encode()
	}
	params.Set("_journal_mode", "WAL")
	params.Set("_synchronous", "FULL") // see package doc: durability over write micro-optimization at v0.1's scale
	return "file:" + path + "?" + params.Encode()
}

func (s *Store) Close() error { return s.db.Close() }
