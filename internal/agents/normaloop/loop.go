package normaloop

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

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

// Loop orchestrates repeated task execution for `norma loop`.
type Loop struct {
	agent.Agent
	logger         zerolog.Logger
	cfg            config.Config
	repoRoot       string
	normaDir       string
	tracker        task.Tracker
	runStore       runStatusStore
	factory        runpkg.AgentFactory
	continueOnFail bool
	policy         task.SelectionPolicy
}

// NewLoop constructs the normaloop ADK loop agent runtime.
func NewLoop(logger zerolog.Logger, cfg config.Config, repoRoot string, tracker task.Tracker, runStore runStatusStore, factory runpkg.AgentFactory, continueOnFail bool, policy task.SelectionPolicy) (*Loop, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute repo root: %w", err)
	}

	w := &Loop{
		logger:         logger.With().Str("component", "normaloop").Logger(),
		cfg:            cfg,
		repoRoot:       absRoot,
		normaDir:       filepath.Join(absRoot, ".norma"),
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

	w.Agent = loopAgent
	return w, nil
}

func (w *Loop) newLoopAgent(selectorAgent, iterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: maxLoopIterations,
		AgentConfig: agent.Config{
			Name:        "NormaLoopAgent",
			Description: "Reads ready tasks and runs PDCA workflow per selected task.",
			SubAgents:   []agent.Agent{selectorAgent, iterationAgent},
		},
	})
}
