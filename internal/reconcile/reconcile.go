// Package reconcile provides logic for reconciling run state between the database and the filesystem.
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"
)

var stepDirPattern = regexp.MustCompile(`^(\d+)-([a-z]+)$`)

// Run reconciles the database with the filesystem by inserting missing step
// rows and corresponding timeline events for step directories found on disk.
func Run(ctx context.Context, db *sql.DB, normaDir string) error {
	runsDir := filepath.Join(normaDir, "runs")
	runEntries, err := os.ReadDir(runsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read runs directory: %w", err)
	}

	slices.SortFunc(runEntries, func(a, b os.DirEntry) int {
		return cmpName(a.Name(), b.Name())
	})

	for _, runEntry := range runEntries {
		if !runEntry.IsDir() {
			continue
		}
		runID := runEntry.Name()
		stepRoot := filepath.Join(runsDir, runID, "steps")
		if err := reconcileRunSteps(ctx, db, runID, stepRoot); err != nil {
			return err
		}
	}

	return nil
}

func reconcileRunSteps(ctx context.Context, db *sql.DB, runID, stepRoot string) error {
	stepEntries, err := os.ReadDir(stepRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read step directory for run %s: %w", runID, err)
	}

	slices.SortFunc(stepEntries, func(a, b os.DirEntry) int {
		return cmpName(a.Name(), b.Name())
	})

	for _, stepEntry := range stepEntries {
		if !stepEntry.IsDir() {
			continue
		}
		stepIndex, role, ok := parseStepDirName(stepEntry.Name())
		if !ok {
			continue
		}
		stepDir := filepath.Join(stepRoot, stepEntry.Name())
		if err := ensureStepRecord(ctx, db, runID, stepIndex, role, stepDir); err != nil {
			return err
		}
	}

	return nil
}

func ensureStepRecord(ctx context.Context, db *sql.DB, runID string, stepIndex int, role, stepDir string) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM steps WHERE run_id=? AND step_index=?`, runID, stepIndex).Scan(&exists); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing step record for run %s step %d: %w", runID, stepIndex, err)
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin reconcile transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var iteration int
	if err := tx.QueryRowContext(ctx, `SELECT iteration FROM runs WHERE run_id=?`, runID).Scan(&iteration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load run iteration for %s: %w", runID, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	summary := "reconciled missing step record"
	if _, err := tx.ExecContext(ctx, `INSERT INTO steps(run_id, step_index, role, iteration, status, step_dir, started_at, ended_at, summary)
		VALUES(?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
		runID, stepIndex, role, iteration, "fail", stepDir, now, summary); err != nil {
		return fmt.Errorf("insert reconciled step for run %s step %d: %w", runID, stepIndex, err)
	}

	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id=?`, runID).Scan(&seq); err != nil {
		return fmt.Errorf("calculate event sequence for run %s: %w", runID, err)
	}
	message := "Step dir exists but DB record was missing; inserted during recovery"
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(run_id, seq, ts, type, message, data_json)
		VALUES(?, ?, ?, ?, ?, NULL)`, runID, seq, now, "reconciled_step", message); err != nil {
		return fmt.Errorf("insert reconciled event for run %s step %d: %w", runID, stepIndex, err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE runs
		SET current_step_index = CASE WHEN current_step_index < ? THEN ? ELSE current_step_index END
		WHERE run_id=?`, stepIndex, stepIndex, runID); err != nil {
		return fmt.Errorf("update run cursor for run %s: %w", runID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reconcile transaction for run %s step %d: %w", runID, stepIndex, err)
	}

	return nil
}

func parseStepDirName(name string) (stepIndex int, role string, ok bool) {
	matches := stepDirPattern.FindStringSubmatch(name)
	if len(matches) != 3 {
		return 0, "", false
	}
	index, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, "", false
	}
	return index, matches[2], true
}

func cmpName(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
