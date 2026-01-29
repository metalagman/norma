// Package db provides database connectivity and migration logic for norma.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store provides persistence for runs and steps.
type Store struct {
	db *sql.DB
}

// NewStore creates a store for run/step persistence.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// CreateRun inserts the run record and a run_started event.
func (s *Store) CreateRun(ctx context.Context, runID, goal, runDir string, iteration int) error {
	createdAt := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin create run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(run_id, created_at, goal, status, iteration, current_step_index, verdict, run_dir)
		VALUES(?, ?, ?, ?, ?, ?, NULL, ?)`,
		runID, createdAt, goal, "running", iteration, 0, runDir); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert run: %w", err)
	}
	if err := s.insertEvent(ctx, tx, runID, "run_started", "run started", ""); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create run: %w", err)
	}
	return nil
}

// StepRecord represents a committed step in the database.
type StepRecord struct {
	RunID     string
	StepIndex int
	Role      string
	Iteration int
	Status    string
	StepDir   string
	StartedAt string
	EndedAt   string
	Summary   string
}

// Update contains updates for a run record.
type Update struct {
	CurrentStepIndex int
	Iteration        int
	Status           string
	Verdict          *string
}

// Event represents a timeline event for a run.
type Event struct {
	Type     string
	Message  string
	DataJSON string
}

// UpdateRun applies a run update and optional event without inserting a step.
func (s *Store) UpdateRun(ctx context.Context, runID string, update Update, event *Event) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update run: %w", err)
	}
	if event != nil {
		if err := s.insertEvent(ctx, tx, runID, event.Type, event.Message, event.DataJSON); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET current_step_index=?, iteration=?, status=?, verdict=? WHERE run_id=?`,
		update.CurrentStepIndex, update.Iteration, update.Status, nullableStringPtr(update.Verdict), runID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update run: %w", err)
	}
	return nil
}

// CommitStep inserts the step record, events, and updates the run in one transaction.
func (s *Store) CommitStep(ctx context.Context, step StepRecord, events []Event, update Update) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin commit step: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO steps(run_id, step_index, role, iteration, status, step_dir, started_at, ended_at, summary)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		step.RunID, step.StepIndex, step.Role, step.Iteration, step.Status, step.StepDir, step.StartedAt, step.EndedAt, step.Summary); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert step: %w", err)
	}
	for _, ev := range events {
		if err := s.insertEvent(ctx, tx, step.RunID, ev.Type, ev.Message, ev.DataJSON); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET current_step_index=?, iteration=?, status=?, verdict=? WHERE run_id=?`,
		update.CurrentStepIndex, update.Iteration, update.Status, nullableStringPtr(update.Verdict), step.RunID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit step: %w", err)
	}
	return nil
}

func (s *Store) insertEvent(ctx context.Context, tx *sql.Tx, runID, typ, message, dataJSON string) error {
	seq, err := s.nextSeq(ctx, tx, runID)
	if err != nil {
		return err
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(run_id, seq, ts, type, message, data_json) VALUES(?, ?, ?, ?, ?, ?)`,
		runID, seq, ts, typ, message, nullableString(dataJSON)); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func (s *Store) nextSeq(ctx context.Context, tx *sql.Tx, runID string) (int, error) {
	var seq int
	row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events WHERE run_id=?`, runID)
	if err := row.Scan(&seq); err != nil {
		return 0, fmt.Errorf("read event seq: %w", err)
	}
	return seq + 1, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// GetRunStatus returns the status for a run id, or empty if missing.
func (s *Store) GetRunStatus(ctx context.Context, runID string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT status FROM runs WHERE run_id=?`, runID)
	var status string
	if err := row.Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("read run status: %w", err)
	}
	return status, nil
}
