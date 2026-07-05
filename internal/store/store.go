package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps an *sql.DB connection to a SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path, runs the schema migration,
// sets WAL + foreign_keys PRAGMAs, and returns a Store ready for queries.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}

	// The sql.Open call is lazy — verify the connection is reachable.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store ping: %w", err)
	}

	s := &Store{db: db}

	if err := s.setup(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store setup: %w", err)
	}

	return s, nil
}

// Close shuts down the database connection pool.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for advanced use (transactions, raw queries)
// by internal callers within this package only.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) setup(ctx context.Context) error {
	// Enable WAL mode for concurrent reads during continuous writes.
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL;"); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}

	// Enforce foreign key constraints (SQLite defaults to OFF).
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys=ON;"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	// Idempotent schema creation — CREATE IF NOT EXISTS for all tables and indexes.
	if err := s.migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY,
		project TEXT,
		phase INTEGER,
		mode TEXT,
		model_name TEXT,
		base_url TEXT,
		context_max_tokens INTEGER,
		resume_count INTEGER DEFAULT 0,
		started_at TEXT,
		ended_at TEXT,
		status TEXT
	);

	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY,
		session_id INTEGER REFERENCES sessions(id),
		path TEXT NOT NULL,
		content_hash TEXT,
		last_event_id INTEGER,
		write_count INTEGER DEFAULT 0,
		UNIQUE(session_id, path)
	);

	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY,
		session_id INTEGER REFERENCES sessions(id),
		turn_index INTEGER,
		event_type TEXT,
		tool_name TEXT,
		file_id INTEGER REFERENCES files(id),
		args_json TEXT,
		result_json TEXT,
		tokens_used INTEGER,
		created_at TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id, turn_index);
	CREATE INDEX IF NOT EXISTS idx_files_session_path ON files(session_id, path);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

// NowUTC returns the current UTC time formatted as RFC3339.
// All timestamp fields should use this format.
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
