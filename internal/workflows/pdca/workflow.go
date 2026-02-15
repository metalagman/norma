package pdca

import (
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/normaloop"
)

// Workflow is a compatibility alias to normaloop.Workflow.
type Workflow = normaloop.Workflow

// NewWorkflow preserves the historical pdca constructor while delegating to normaloop.
func NewWorkflow(cfg config.Config, store *db.Store, tracker task.Tracker) *normaloop.Workflow {
	return normaloop.NewWorkflow(cfg, store, tracker)
}
