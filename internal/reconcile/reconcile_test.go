package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	dbpkg "github.com/metalagman/norma/internal/db"
)

func TestRunInsertsMissingStepRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := t.TempDir()
	normaDir := filepath.Join(rootDir, ".norma")
	runID := "run-1"
	runDir := filepath.Join(normaDir, "runs", runID)
	stepDir := filepath.Join(runDir, "steps", "001-plan")

	if err := os.MkdirAll(stepDir, 0o755); err != nil {
		t.Fatalf("create step dir: %v", err)
	}

	dbPath := filepath.Join(normaDir, "norma.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := dbpkg.NewStore(db)
	if err := store.CreateRun(ctx, runID, "goal", runDir, 1); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := Run(ctx, db, normaDir); err != nil {
		t.Fatalf("reconcile run: %v", err)
	}

	var role, status, storedStepDir string
	var iteration int
	if err := db.QueryRowContext(ctx, `SELECT role, status, iteration, step_dir FROM steps WHERE run_id=? AND step_index=?`, runID, 1).
		Scan(&role, &status, &iteration, &storedStepDir); err != nil {
		t.Fatalf("query reconciled step: %v", err)
	}
	if role != "plan" {
		t.Fatalf("role = %q, want %q", role, "plan")
	}
	if status != "fail" {
		t.Fatalf("status = %q, want %q", status, "fail")
	}
	if iteration != 1 {
		t.Fatalf("iteration = %d, want %d", iteration, 1)
	}
	if storedStepDir != stepDir {
		t.Fatalf("step_dir = %q, want %q", storedStepDir, stepDir)
	}

	var eventType, eventMessage string
	if err := db.QueryRowContext(ctx, `SELECT type, message FROM events WHERE run_id=? AND type=?`, runID, "reconciled_step").
		Scan(&eventType, &eventMessage); err != nil {
		t.Fatalf("query reconciled event: %v", err)
	}
	if eventType != "reconciled_step" {
		t.Fatalf("event type = %q, want %q", eventType, "reconciled_step")
	}
	expectedMessage := "Step dir exists but DB record was missing; inserted during recovery"
	if eventMessage != expectedMessage {
		t.Fatalf("event message = %q, want %q", eventMessage, expectedMessage)
	}

	var currentStepIndex int
	if err := db.QueryRowContext(ctx, `SELECT current_step_index FROM runs WHERE run_id=?`, runID).Scan(&currentStepIndex); err != nil {
		t.Fatalf("query run current_step_index: %v", err)
	}
	if currentStepIndex != 1 {
		t.Fatalf("current_step_index = %d, want %d", currentStepIndex, 1)
	}

	// Re-running reconciliation should be idempotent.
	if err := Run(ctx, db, normaDir); err != nil {
		t.Fatalf("reconcile run second pass: %v", err)
	}

	var stepCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM steps WHERE run_id=?`, runID).Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if stepCount != 1 {
		t.Fatalf("step count = %d, want %d", stepCount, 1)
	}
}

func TestRunSkipsStepDirsForMissingRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := t.TempDir()
	normaDir := filepath.Join(rootDir, ".norma")
	stepDir := filepath.Join(normaDir, "runs", "missing-run", "steps", "001-plan")

	if err := os.MkdirAll(stepDir, 0o755); err != nil {
		t.Fatalf("create step dir: %v", err)
	}

	dbPath := filepath.Join(normaDir, "norma.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Run(ctx, db, normaDir); err != nil {
		t.Fatalf("reconcile run: %v", err)
	}

	var stepCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM steps`).Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if stepCount != 0 {
		t.Fatalf("step count = %d, want %d", stepCount, 0)
	}
}
