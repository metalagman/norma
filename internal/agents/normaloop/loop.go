package normaloop

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	runpkg "github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
)

const (
	statusDoing    = "doing"
	statusTodo     = "todo"
	statusPlanning = "planning"
)

const maxLoopIterations uint = 1_000_000

type runStatusStore interface {
	GetRunStatus(ctx context.Context, runID string) (string, error)
	CreateRun(ctx context.Context, runID, goal, runDir string, iteration int) error
	UpdateRun(ctx context.Context, runID string, update db.Update, event *db.Event) error
	DB() *sql.DB
}

type loopRuntime struct {
	logger               zerolog.Logger
	cfg                  config.Config
	workingDir           string
	normaDir             string
	tracker              task.Tracker
	runStore             runStatusStore
	factory              runpkg.AgentFactory
	continueOnFail       bool
	policy               task.SelectionPolicy
	overrideBackoffSteps []time.Duration
}

// New constructs the normaloop ADK loop agent runtime.
func New(logger zerolog.Logger, cfg config.Config, workingDir string, tracker task.Tracker, runStore runStatusStore, factory runpkg.AgentFactory, continueOnFail bool, policy task.SelectionPolicy) (agent.Agent, error) {
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute working dir: %w", err)
	}

	w := &loopRuntime{
		logger:         logger.With().Str("component", "normaloop").Logger(),
		cfg:            cfg,
		workingDir:     absWorkingDir,
		normaDir:       filepath.Join(absWorkingDir, ".norma"),
		tracker:        tracker,
		runStore:       runStore,
		factory:        factory,
		continueOnFail: continueOnFail,
		policy:         policy,
	}

	iterationAgent, err := w.newIterationAgent()
	if err != nil {
		return nil, fmt.Errorf("create normaloop iteration agent: %w", err)
	}
	selectorAgent, err := w.newSelectorAgent()
	if err != nil {
		return nil, fmt.Errorf("create normaloop selector agent: %w", err)
	}
	loopAgent, err := w.newLoopAgent(selectorAgent, iterationAgent)
	if err != nil {
		return nil, fmt.Errorf("create normaloop loop agent: %w", err)
	}

	return loopAgent, nil
}

func (w *loopRuntime) newLoopAgent(selectorAgent, iterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: maxLoopIterations,
		AgentConfig: agent.Config{
			Name:        "NormaLoopAgent",
			Description: "Reads ready tasks and runs PDCA workflow per selected task.",
			SubAgents:   []agent.Agent{selectorAgent, iterationAgent},
		},
	})
}
