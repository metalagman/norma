package reconcile

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Run performs cleanup of temp step dirs and reconciles missing DB step records.
func Run(ctx context.Context, db *sql.DB, normaDir string) error {
	runsDir := filepath.Join(normaDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list runs: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		runDir := filepath.Join(runsDir, runID)
		stepsDir := filepath.Join(runDir, "steps")
		if err := cleanupTemps(stepsDir); err != nil {
			return err
		}
		if err := reconcileRun(ctx, db, runID, stepsDir); err != nil {
			return err
		}
	}
	return nil
}

func cleanupTemps(stepsDir string) error {
	entries, err := os.ReadDir(stepsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list steps: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.Contains(name, ".tmp-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(stepsDir, name)); err != nil {
			return fmt.Errorf("remove temp step dir %q: %w", name, err)
		}
	}
	return nil
}

func reconcileRun(ctx context.Context, db *sql.DB, runID, stepsDir string) error {
	entries, err := os.ReadDir(stepsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list step dirs: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stepIndex, role, ok := parseStepDir(entry.Name())
		if !ok {
			continue
		}
		var exists int
		row := db.QueryRowContext(ctx, `SELECT 1 FROM steps WHERE run_id=? AND step_index=?`, runID, stepIndex)
		scanErr := row.Scan(&exists)
		if scanErr == nil {
			continue
		}
		if scanErr != sql.ErrNoRows {
			return fmt.Errorf("check step existence: %w", scanErr)
		}

		if err := insertReconciledStep(ctx, db, runID, stepIndex, role, filepath.Join(stepsDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func parseStepDir(name string) (int, string, bool) {
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	idx, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	return idx, parts[1], true
}

func insertReconciledStep(ctx context.Context, db *sql.DB, runID string, stepIndex int, role, stepDir string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin reconcile tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO steps(run_id, step_index, role, iteration, status, step_dir, started_at, ended_at, summary)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, stepIndex, role, 0, "fail", stepDir, now, now, "reconciled step missing DB record"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert reconciled step: %w", err)
	}
	seq, err := nextSeq(ctx, tx, runID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(run_id, seq, ts, type, message)
		VALUES(?, ?, ?, ?, ?)`,
		runID, seq, now, "reconciled_step", "Step dir exists but DB record was missing; inserted during recovery"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert reconcile event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET current_step_index = CASE WHEN current_step_index < ? THEN ? ELSE current_step_index END WHERE run_id=?`, stepIndex, stepIndex, runID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update run current_step_index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reconcile tx: %w", err)
	}
	return nil
}

func nextSeq(ctx context.Context, tx *sql.Tx, runID string) (int, error) {
	var seq int
	row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events WHERE run_id=?`, runID)
	if err := row.Scan(&seq); err != nil {
		return 0, fmt.Errorf("read event seq: %w", err)
	}
	return seq + 1, nil
}
