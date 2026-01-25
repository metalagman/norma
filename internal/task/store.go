package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/metalagman/norma/internal/model"
)

// Store manages task persistence.
type Store struct {
	db *sql.DB
}

// NewStore creates a task store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Task describes a task record.
type Task struct {
	ID        int64
	Title     string
	Goal      string
	Criteria  []model.AcceptanceCriterion
	Status    string
	RunID     *string
	CreatedAt string
	UpdatedAt string
}

// Add inserts a new task.
func (s *Store) Add(ctx context.Context, title, goal string, criteria []model.AcceptanceCriterion, runID *string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if criteria == nil {
		criteria = []model.AcceptanceCriterion{}
	}
	criteriaJSON, err := json.Marshal(criteria)
	if err != nil {
		return 0, fmt.Errorf("marshal criteria: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO tasks(title, goal, acceptance_criteria_json, status, run_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, title, goal, string(criteriaJSON), "todo", runID, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read task id: %w", err)
	}
	return id, nil
}

// List returns tasks filtered by status (optional).
func (s *Store) List(ctx context.Context, status *string) ([]Task, error) {
	query := `SELECT id, title, goal, acceptance_criteria_json, status, run_id, created_at, updated_at FROM tasks`
	args := []any{}
	if status != nil {
		query += " WHERE status=?"
		args = append(args, *status)
	}
	query += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var runID sql.NullString
		var criteriaJSON string
		if err := rows.Scan(&t.ID, &t.Title, &t.Goal, &criteriaJSON, &t.Status, &runID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		if runID.Valid {
			t.RunID = &runID.String
		}
		if err := json.Unmarshal([]byte(criteriaJSON), &t.Criteria); err != nil {
			return nil, fmt.Errorf("parse criteria: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return out, nil
}

// Get fetches a task by id.
func (s *Store) Get(ctx context.Context, id int64) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, title, goal, acceptance_criteria_json, status, run_id, created_at, updated_at FROM tasks WHERE id=?`, id)
	var t Task
	var runID sql.NullString
	var criteriaJSON string
	if err := row.Scan(&t.ID, &t.Title, &t.Goal, &criteriaJSON, &t.Status, &runID, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Task{}, fmt.Errorf("task %d not found", id)
		}
		return Task{}, fmt.Errorf("read task: %w", err)
	}
	if runID.Valid {
		t.RunID = &runID.String
	}
	if err := json.Unmarshal([]byte(criteriaJSON), &t.Criteria); err != nil {
		return Task{}, fmt.Errorf("parse criteria: %w", err)
	}
	return t, nil
}

// MarkDone sets a task status to done.
func (s *Store) MarkDone(ctx context.Context, id int64) error {
	return s.MarkStatus(ctx, id, "done")
}

// MarkStatus updates a task status and updated_at.
func (s *Store) MarkStatus(ctx context.Context, id int64, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?, updated_at=? WHERE id=?`, status, now, id)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("task %d not found", id)
	}
	return nil
}

// SetRun associates a run id to a task.
func (s *Store) SetRun(ctx context.Context, id int64, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET run_id=?, updated_at=? WHERE id=?`, runID, now, id)
	if err != nil {
		return fmt.Errorf("update task run_id: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("task %d not found", id)
	}
	return nil
}

// AddDependency links task->dependsOn.
func (s *Store) AddDependency(ctx context.Context, taskID, dependsOnID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO task_edges(task_id, depends_on_id) VALUES(?, ?)`, taskID, dependsOnID)
	if err != nil {
		return fmt.Errorf("insert dependency: %w", err)
	}
	return nil
}

// LeafTasks returns tasks that are todo and whose dependencies are all done.
func (s *Store) LeafTasks(ctx context.Context) ([]Task, error) {
	query := `
SELECT t.id, t.title, t.goal, t.acceptance_criteria_json, t.status, t.run_id, t.created_at, t.updated_at
FROM tasks t
WHERE t.status = 'todo'
AND NOT EXISTS (
  SELECT 1 FROM task_edges e
  JOIN tasks d ON d.id = e.depends_on_id
  WHERE e.task_id = t.id AND d.status != 'done'
)
ORDER BY t.id`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query leaf tasks: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var runID sql.NullString
		var criteriaJSON string
		if err := rows.Scan(&t.ID, &t.Title, &t.Goal, &criteriaJSON, &t.Status, &runID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan leaf task: %w", err)
		}
		if runID.Valid {
			t.RunID = &runID.String
		}
		if err := json.Unmarshal([]byte(criteriaJSON), &t.Criteria); err != nil {
			return nil, fmt.Errorf("parse criteria: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate leaf tasks: %w", err)
	}
	return out, nil
}

// RunStatus returns the status for a run id, or empty if missing.
func (s *Store) RunStatus(ctx context.Context, runID string) (string, error) {
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
