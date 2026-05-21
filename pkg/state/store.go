// Package state persists per-task inspection state in SQLite: status and attempt count keyed
// by task position. The inspector gate ([[inspector-gate-fork]]) reads and writes it so that a
// worker's repeated attempts at one task, and the escalation to the oracle after repeated
// rejections, survive the fresh-session-per-task model where the worker itself holds no state.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/mattn/go-sqlite3" // sqlite driver, registered via init() side effect
)

// Status is the lifecycle state of a single task under inspection.
type Status string

const (
	StatusPending       Status = "pending"        // not yet accepted; eligible for a worker attempt
	StatusRejected      Status = "rejected"       // last attempt rejected by the inspector; worker retries
	StatusNeedsRevision Status = "needs_revision" // escalated to the oracle (3 rejects or update verdict)
	StatusDone          Status = "done"           // accepted by the inspector
)

// TaskState is the persisted record for one task, identified by its 1-based plan position.
type TaskState struct {
	Position     int
	Status       Status
	AttemptCount int
	LastCommit   string // HEAD captured when the worker last proposed completion
}

// Store is a SQLite-backed per-task state store.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	position      INTEGER PRIMARY KEY,
	status        TEXT    NOT NULL,
	attempt_count INTEGER NOT NULL DEFAULT 0,
	last_commit   TEXT    NOT NULL DEFAULT ''
);`

// Open opens (creating if needed) the SQLite state store at path and ensures the schema exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db at %s: %w", path, err)
	}
	// single connection: keeps an in-memory db alive for the store's lifetime and serializes
	// writes (sqlite is single-writer regardless), avoiding SQLITE_BUSY under this low-volume load.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close state store: %w", err)
	}
	return nil
}

// Get returns the state for the task at position. A task that has never been saved is reported
// as pending with zero attempts, so callers can treat "absent" and "fresh" uniformly.
func (s *Store) Get(position int) (TaskState, error) {
	var (
		status     string
		attempts   int
		lastCommit string
	)
	err := s.db.QueryRowContext(context.Background(),
		`SELECT status, attempt_count, last_commit FROM tasks WHERE position = ?`, position,
	).Scan(&status, &attempts, &lastCommit)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskState{Position: position, Status: StatusPending}, nil
	}
	if err != nil {
		return TaskState{}, fmt.Errorf("get task %d: %w", position, err)
	}
	return TaskState{Position: position, Status: Status(status), AttemptCount: attempts, LastCommit: lastCommit}, nil
}

// Save upserts the full state for a task, keyed by position.
func (s *Store) Save(ts TaskState) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tasks (position, status, attempt_count, last_commit)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(position) DO UPDATE SET
		   status        = excluded.status,
		   attempt_count = excluded.attempt_count,
		   last_commit   = excluded.last_commit`,
		ts.Position, string(ts.Status), ts.AttemptCount, ts.LastCommit)
	if err != nil {
		return fmt.Errorf("save task %d: %w", ts.Position, err)
	}
	return nil
}
