package run

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RetentionPolicy controls run cleanup.
type RetentionPolicy struct {
	KeepLast int
	KeepDays int
}

// PruneResult summarizes a prune operation.
type PruneResult struct {
	Considered int
	Kept       int
	Deleted    int
	Skipped    int
}

// PruneRuns deletes old run records and their directories.
func PruneRuns(ctx context.Context, db *sql.DB, runsDir string, policy RetentionPolicy, dryRun bool) (PruneResult, error) {
	if policy.KeepLast <= 0 && policy.KeepDays <= 0 {
		return PruneResult{}, nil
	}
	cutoff := time.Time{}
	if policy.KeepDays > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(policy.KeepDays) * 24 * time.Hour)
	}
	rows, err := db.QueryContext(ctx, `SELECT run_id, created_at, status, run_dir FROM runs ORDER BY created_at DESC`)
	if err != nil {
		return PruneResult{}, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	type runRow struct {
		id        string
		createdAt time.Time
		status    string
		runDir    string
		parseErr  error
	}
	var runs []runRow
	for rows.Next() {
		var id, createdAt, status, runDir string
		if err := rows.Scan(&id, &createdAt, &status, &runDir); err != nil {
			return PruneResult{}, fmt.Errorf("scan run: %w", err)
		}
		parsed, parseErr := time.Parse(time.RFC3339, createdAt)
		runs = append(runs, runRow{id: id, createdAt: parsed, status: status, runDir: runDir, parseErr: parseErr})
	}
	if err := rows.Err(); err != nil {
		return PruneResult{}, fmt.Errorf("iterate runs: %w", err)
	}

	res := PruneResult{Considered: len(runs)}
	for idx, row := range runs {
		keep := false
		if row.status == "running" {
			keep = true
		}
		if !keep && policy.KeepLast > 0 && idx < policy.KeepLast {
			keep = true
		}
		if !keep && policy.KeepDays > 0 {
			if row.parseErr != nil {
				keep = true
			} else if row.createdAt.After(cutoff) {
				keep = true
			}
		}
		if keep {
			res.Kept++
			continue
		}
		if dryRun {
			res.Deleted++
			continue
		}
		targetDir := row.runDir
		if targetDir == "" {
			targetDir = filepath.Join(runsDir, row.id)
		}
		if err := os.RemoveAll(targetDir); err != nil && !os.IsNotExist(err) {
			res.Skipped++
			continue
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM runs WHERE run_id=?`, row.id); err != nil {
			return res, fmt.Errorf("delete run %s: %w", row.id, err)
		}
		res.Deleted++
	}
	return res, nil
}
